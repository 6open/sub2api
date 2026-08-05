#!/usr/bin/env python3
"""Fast-path Feishu alerts for severe sub2api failures."""

import base64
import concurrent.futures
import copy
import fcntl
import hashlib
import hmac
import json
import os
import subprocess
import sys
import time
import urllib.request


CONFIG_PATH = os.environ.get(
    "SUB2API_FAST_MONITOR_CONFIG",
    "/etc/sub2api-feishu-fast-monitor/config.json",
)
NOTIFICATION_RATE_STATE_PATH = "/var/lib/sub2api-feishu-monitor/notification-rate-limit.json"

CATEGORY_STYLES = {
    "error": ("🔴", "错误"),
    "warning": ("🟠", "警告"),
    "reminder": ("🟡", "提醒"),
    "normal": ("🟢", "正常"),
}
CATEGORY_PRIORITY = {"normal": 0, "reminder": 1, "warning": 2, "error": 3}


def load_json(path, default):
    try:
        with open(path, "r", encoding="utf-8") as handle:
            return json.load(handle)
    except FileNotFoundError:
        return default


def save_json(path, data):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    temporary_path = f"{path}.tmp"
    with open(temporary_path, "w", encoding="utf-8") as handle:
        json.dump(data, handle, ensure_ascii=False, indent=2)
        handle.write("\n")
    os.replace(temporary_path, path)


def notification_category(issues, resolved):
    if not issues:
        return "normal"
    categories = [
        issue.get("category", "error")
        for issue in issues
        if issue.get("category", "error") in CATEGORY_STYLES
    ]
    return max(categories or ["error"], key=lambda value: CATEGORY_PRIORITY[value])


def sign(secret, timestamp):
    string_to_sign = f"{timestamp}\n{secret}".encode("utf-8")
    digest = hmac.new(string_to_sign, b"", digestmod=hashlib.sha256).digest()
    return base64.b64encode(digest).decode("utf-8")


def send_feishu(feishu_config, monitor_config, issues, resolved):
    category = notification_category(issues, resolved)
    icon, category_label = CATEGORY_STYLES[category]
    now = int(time.time())
    timestamp = str(now)
    service_label = monitor_config.get(
        "service_label", feishu_config.get("service_label", "sub2api")
    )
    lines = [
        f"{icon} {category_label}｜{service_label} 快速监控",
        "",
        f"时间：{time.strftime('%Y-%m-%d %H:%M:%S %z')}",
    ]
    for issue in issues:
        lines.extend(["", f"- {issue['title']}", f"  {issue['body']}"])
    for issue in resolved:
        lines.extend(["", f"- 已恢复：{issue['title']}"])

    payload = {
        "timestamp": timestamp,
        "msg_type": "text",
        "content": {"text": "\n".join(lines)},
    }
    secret = feishu_config.get("secret", "")
    if secret:
        payload["sign"] = sign(secret, timestamp)

    rate_state_path = feishu_config.get(
        "notification_rate_limit_state_path", NOTIFICATION_RATE_STATE_PATH
    )
    interval = max(
        60, int(feishu_config.get("non_normal_min_interval_seconds", 60))
    )
    os.makedirs(os.path.dirname(rate_state_path), exist_ok=True)
    with open(f"{rate_state_path}.lock", "a+", encoding="utf-8") as lock_handle:
        fcntl.flock(lock_handle.fileno(), fcntl.LOCK_EX)
        rate_state = load_json(rate_state_path, {})
        last_sent_at = int(rate_state.get("last_non_normal_sent_at", 0))
        if category != "normal" and now - last_sent_at < interval:
            wait_seconds = interval - (now - last_sent_at)
            print(f"defer {category} notification for {wait_seconds}s due to rate limit")
            return False

        request = urllib.request.Request(
            feishu_config["webhook"],
            data=json.dumps(payload).encode("utf-8"),
            headers={"Content-Type": "application/json"},
            method="POST",
        )
        with urllib.request.urlopen(request, timeout=10) as response:
            body = response.read().decode("utf-8", "replace")
            if response.status >= 300:
                raise RuntimeError(body)
            result = json.loads(body)
            if result.get("code") != 0:
                raise RuntimeError(body)

        if category != "normal":
            save_json(rate_state_path, {
                "last_non_normal_sent_at": now,
                "last_category": category,
                "last_service": service_label,
            })
        return True


def probe_http(check):
    timeout = max(1, int(check.get("timeout_seconds", 5)))
    expected_content = str(check.get("expected_content", ""))
    command = [
        "curl",
        "-sS",
        "-o",
        "-" if expected_content else "/dev/null",
        "-w",
        "\n__SUB2API_MONITOR_META__%{http_code}|%{time_total}"
        if expected_content
        else "%{http_code}|%{time_total}",
        "--connect-timeout",
        str(min(timeout, 3)),
        "--max-time",
        str(timeout),
    ]
    proxy = check.get("proxy")
    if proxy:
        command.extend(["--proxy", proxy])
    resolve = check.get("resolve")
    if resolve:
        command.extend(["--resolve", str(resolve)])
    if check.get("insecure_skip_verify", False):
        command.append("--insecure")
    command.append(check["url"])

    completed = subprocess.run(
        command,
        universal_newlines=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=False,
        timeout=timeout + 2,
    )
    raw = completed.stdout.strip()
    response_body = ""
    if expected_content:
        response_body, marker, raw = raw.rpartition("__SUB2API_MONITOR_META__")
        if not marker:
            raw = ""
    status_text, _, duration_text = raw.partition("|")
    try:
        status = int(status_text)
    except ValueError:
        status = 0
    try:
        latency_ms = int(float(duration_text) * 1000)
    except ValueError:
        latency_ms = timeout * 1000

    expected = check.get("expected_statuses")
    if expected:
        ok = completed.returncode == 0 and status in {int(item) for item in expected}
    else:
        ok = completed.returncode == 0 and 100 <= status < 500
    error = completed.stderr.strip()[-240:]
    if ok and expected_content and expected_content not in response_body:
        ok = False
        error = f"expected content marker missing: {expected_content}"[-240:]
    return check, ok, status, latency_ms, error


def collect_probe_issues(config, state):
    checks = [check for check in config.get("checks", []) if check.get("enabled", True)]
    issues = {}
    if not checks:
        return issues

    probe_state = state.setdefault("probes", {})
    with concurrent.futures.ThreadPoolExecutor(max_workers=len(checks)) as executor:
        results = list(executor.map(probe_http, checks))

    for check, ok, status, latency_ms, error in results:
        key = str(check["key"])
        item_state = probe_state.setdefault(key, {})
        if ok:
            item_state["fail_count"] = 0
            item_state["last_ok_at"] = int(time.time())
        else:
            item_state["fail_count"] = int(item_state.get("fail_count", 0)) + 1
        item_state["last_status"] = status
        item_state["last_latency_ms"] = latency_ms
        item_state["last_error"] = error

        threshold = max(1, int(check.get("fail_threshold", 2)))
        if item_state["fail_count"] >= threshold:
            label = check.get("label", key)
            issues[f"probe:{key}"] = {
                "category": "error",
                "title": f"{label}连续失败",
                "body": (
                    f"连续失败 {item_state['fail_count']} 次，"
                    f"HTTP={status}，耗时={latency_ms}ms，"
                    f"错误={error or '无响应'}"
                ),
                "activity_token": str(item_state["fail_count"]),
            }
    return issues


def query_severe_errors(database_config):
    runtime = database_config.get("runtime", "docker")
    container = database_config.get("container", "sub2api-postgres")
    seconds = max(10, int(database_config.get("window_seconds", 60)))
    sql = f"""
    select coalesce(json_agg(row_to_json(t)), '[]'::json)
    from (
      select coalesce(nullif(platform, ''), 'unknown') as platform,
             coalesce(nullif(model, ''), 'unknown') as model,
             coalesce(status_code, 0) as status_code,
             coalesce(error_type, 'unknown') as error_type,
             count(*) as errors,
             max(id) as max_error_id
      from ops_error_logs
      where created_at > now() - interval '{seconds} seconds'
        and is_count_tokens = false
        and is_business_limited = false
        and status_code >= 500
        and (
          coalesce(upstream_status_code, status_code) >= 500
          or network_error_type is not null
          or error_type in ('network_error', 'timeout', 'connection_error')
        )
      group by 1, 2, 3, 4
      order by count(*) desc
    ) t;
    """
    command = [
        runtime,
        "exec",
        "-i",
        container,
        "sh",
        "-lc",
        'psql -X -U "$POSTGRES_USER" -d "$POSTGRES_DB" -t -A',
    ]
    completed = subprocess.run(
        command,
        input=sql,
        universal_newlines=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=False,
        timeout=10,
    )
    if completed.returncode != 0:
        raise RuntimeError(completed.stderr.strip() or completed.stdout.strip())
    output = completed.stdout.strip()
    return json.loads(output or "[]")


def collect_error_burst_issues(config, state):
    database_config = config.get("database_error_burst", {})
    if not database_config.get("enabled", False):
        return {}

    issues = {}
    database_state = state.setdefault("database_error_burst", {})
    try:
        rows = query_severe_errors(database_config)
        database_state["query_fail_count"] = 0
        database_state["last_ok_at"] = int(time.time())
        database_state.pop("last_error", None)
    except Exception as exc:
        database_state["query_fail_count"] = int(
            database_state.get("query_fail_count", 0)
        ) + 1
        database_state["last_error"] = str(exc)[-300:]
        threshold = max(1, int(database_config.get("query_fail_threshold", 2)))
        if database_state["query_fail_count"] >= threshold:
            issues["database:query"] = {
                "category": "error",
                "title": "监控数据库连续不可访问",
                "body": (
                    f"连续失败 {database_state['query_fail_count']} 次，"
                    f"错误={database_state['last_error']}"
                ),
                "activity_token": str(database_state["query_fail_count"]),
            }
        return issues

    total_errors = sum(int(row.get("errors") or 0) for row in rows)
    minimum = max(1, int(database_config.get("min_severe_errors", 5)))
    if total_errors < minimum:
        return issues

    details = []
    for row in rows[:5]:
        details.append(
            f"{row.get('platform')}/{row.get('model')} "
            f"HTTP {row.get('status_code')} x{row.get('errors')}"
        )
    window_seconds = max(10, int(database_config.get("window_seconds", 60)))
    max_error_id = max(int(row.get("max_error_id") or 0) for row in rows)
    affected_scopes = sorted(
        f"{row.get('platform')}/{row.get('model')}/{row.get('status_code')}"
        for row in rows
    )
    severity_bucket = max(
        threshold
        for threshold in (1, 5, 10, 20, 50, 100)
        if total_errors >= threshold
    )
    issues["database:severe_error_burst"] = {
        "category": "error",
        "title": "一分钟内严重错误爆发",
        "body": (
            f"最近 {window_seconds} 秒严重错误 {total_errors} 个；"
            + "，".join(details)
        ),
        "activity_token": str(max_error_id),
        "scope_items": affected_scopes,
        "severity_level": severity_bucket,
    }
    return issues


def query_slow_ai_requests(database_config):
    runtime = database_config.get("runtime", "docker")
    container = database_config.get("container", "sub2api-postgres")
    seconds = max(30, int(database_config.get("window_seconds", 180)))
    threshold_ms = max(1000, int(database_config.get("ttft_threshold_ms", 30000)))
    sql = f"""
    select coalesce(json_agg(row_to_json(t)), '[]'::json)
    from (
      select account_id,
             coalesce(nullif(model, ''), 'unknown') as model,
             count(*) as slow_requests,
             round(avg(first_token_ms)) as avg_ttft_ms,
             max(first_token_ms) as max_ttft_ms,
             max(id) as max_usage_id
      from usage_logs
      where created_at > now() - interval '{seconds} seconds'
        and first_token_ms >= {threshold_ms}
      group by account_id, 2
      order by count(*) desc, max(first_token_ms) desc
    ) t;
    """
    command = [
        runtime,
        "exec",
        "-i",
        container,
        "sh",
        "-lc",
        'psql -X -U "$POSTGRES_USER" -d "$POSTGRES_DB" -t -A',
    ]
    completed = subprocess.run(
        command,
        input=sql,
        universal_newlines=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=False,
        timeout=10,
    )
    if completed.returncode != 0:
        raise RuntimeError(completed.stderr.strip() or completed.stdout.strip())
    return json.loads(completed.stdout.strip() or "[]")


def collect_latency_burst_issues(config, state):
    latency_config = config.get("database_latency_burst", {})
    if not latency_config.get("enabled", False):
        return {}

    latency_state = state.setdefault("database_latency_burst", {})
    try:
        rows = query_slow_ai_requests(latency_config)
        latency_state["query_fail_count"] = 0
        latency_state["last_ok_at"] = int(time.time())
        latency_state.pop("last_error", None)
    except Exception as exc:
        latency_state["query_fail_count"] = int(
            latency_state.get("query_fail_count", 0)
        ) + 1
        latency_state["last_error"] = str(exc)[-300:]
        return {}

    total_slow = sum(int(row.get("slow_requests") or 0) for row in rows)
    max_ttft = max((int(row.get("max_ttft_ms") or 0) for row in rows), default=0)
    minimum = max(1, int(latency_config.get("min_slow_requests", 2)))
    critical_ms = max(
        int(latency_config.get("ttft_threshold_ms", 30000)),
        int(latency_config.get("critical_ttft_ms", 45000)),
    )
    if total_slow < minimum and max_ttft < critical_ms:
        return {}

    details = [
        (
            f"账号 {row.get('account_id') or 'unknown'}/{row.get('model')} "
            f"x{row.get('slow_requests')}，平均 {row.get('avg_ttft_ms')}ms，"
            f"最高 {row.get('max_ttft_ms')}ms"
        )
        for row in rows[:5]
    ]
    max_usage_id = max(int(row.get("max_usage_id") or 0) for row in rows)
    affected_scopes = sorted(
        f"{row.get('account_id') or 'unknown'}/{row.get('model')}" for row in rows
    )
    severity_bucket = max(
        threshold
        for threshold in (1, 2, 5, 10, 20, 50)
        if total_slow >= threshold
    )
    window_seconds = max(30, int(latency_config.get("window_seconds", 180)))
    return {
        "database:ai_ttft_degraded": {
            "category": "warning",
            "title": "AI 首 Token 延迟持续过高",
            "body": (
                f"最近 {window_seconds} 秒有 {total_slow} 次慢请求；"
                + "；".join(details)
            ),
            "activity_token": str(max_usage_id),
            "scope_items": affected_scopes,
            "severity_level": severity_bucket,
        }
    }


def confirm_recovery(config, state, detected_issues):
    required = max(1, int(config.get("recovery_success_checks", 2)))
    previous_active = set(state.get("active", []))
    clear_counts = state.setdefault("clear_counts", {})
    issue_details = state.setdefault("issue_details", {})

    for key, issue in detected_issues.items():
        clear_counts[key] = 0
        issue_details[key] = issue

    confirmed = dict(detected_issues)
    for key in previous_active - set(detected_issues):
        clear_counts[key] = int(clear_counts.get(key, 0)) + 1
        if clear_counts[key] < required and key in issue_details:
            confirmed[key] = issue_details[key]
        else:
            clear_counts.pop(key, None)
    return confirmed


def reminder_schedule(config):
    raw_schedule = config.get(
        "reminder_schedule_seconds", [120, 300, 600, 1200, 1800]
    )
    return sorted({max(1, int(value)) for value in raw_schedule})


def select_notifications(config, state, issues, previous_active, now):
    schedule = reminder_schedule(config)
    incidents = state.setdefault("incidents", {})
    sendable = []

    for key, issue in issues.items():
        activity_token = str(issue.get("activity_token", ""))
        scope_items = sorted({str(item) for item in issue.get("scope_items", [])})
        severity_level = int(issue.get("severity_level", 0))
        incident = incidents.get(key)
        if key not in previous_active or incident is None:
            incidents[key] = {
                "started_at": now,
                "next_reminder_index": 0,
                "last_activity_token": activity_token,
                "scope_items": scope_items,
                "severity_level": severity_level,
                "notifications_sent": 1,
            }
            sendable.append(issue)
            continue

        previous_scope_items = {
            str(item) for item in incident.get("scope_items", [])
        }
        scope_expanded = bool(set(scope_items) - previous_scope_items)
        severity_increased = severity_level > int(
            incident.get("severity_level", 0)
        )
        incident["scope_items"] = scope_items
        incident["severity_level"] = severity_level
        if scope_expanded or severity_increased:
            incident["last_activity_token"] = activity_token
            incident["notifications_sent"] = int(
                incident.get("notifications_sent", 1)
            ) + 1
            changed = dict(issue)
            changed["body"] = f"影响范围扩大或故障加重；{issue['body']}"
            sendable.append(changed)
            continue

        index = int(incident.get("next_reminder_index", 0))
        elapsed = max(0, now - int(incident.get("started_at", now)))
        due_indexes = [
            offset_index
            for offset_index in range(index, len(schedule))
            if elapsed >= schedule[offset_index]
        ]
        has_new_activity = activity_token != str(
            incident.get("last_activity_token", "")
        )
        if due_indexes and has_new_activity:
            due_index = due_indexes[-1]
            incident["next_reminder_index"] = due_index + 1
            incident["last_activity_token"] = activity_token
            incident["notifications_sent"] = int(
                incident.get("notifications_sent", 1)
            ) + 1
            reminder = dict(issue)
            reminder["category"] = "reminder"
            reminder["body"] = f"持续故障 {elapsed // 60} 分钟更新；{issue['body']}"
            sendable.append(reminder)

    return sendable


def main():
    config = load_json(CONFIG_PATH, {})
    if not config:
        raise RuntimeError(f"missing config: {CONFIG_PATH}")

    feishu_path = config.get(
        "feishu_config_path", "/etc/sub2api-feishu-monitor/config.json"
    )
    feishu_config = load_json(feishu_path, {})
    if not feishu_config.get("webhook"):
        raise RuntimeError(f"webhook is not configured in {feishu_path}")

    state_path = config.get(
        "state_path", "/var/lib/sub2api-feishu-fast-monitor/state.json"
    )
    state = load_json(state_path, {})
    previous_active = set(state.get("active", []))

    detected_issues = collect_probe_issues(config, state)
    detected_issues.update(collect_error_burst_issues(config, state))
    detected_issues.update(collect_latency_burst_issues(config, state))
    issues = confirm_recovery(config, state, detected_issues)
    current_active = set(issues)

    now = int(time.time())
    notification_state = copy.deepcopy(state.get("incidents", {}))
    sendable = select_notifications(
        config, state, issues, previous_active, now
    )

    resolved_keys = previous_active - current_active
    resolved = [
        {
            "title": state.get("issue_details", {})
            .get(key, {})
            .get("title", key)
        }
        for key in sorted(resolved_keys)
    ]

    if sendable or resolved:
        try:
            sent = send_feishu(feishu_config, config, sendable, resolved)
        except Exception:
            state["incidents"] = notification_state
            state["active"] = sorted(previous_active)
            save_json(state_path, state)
            raise
        if not sent:
            state["incidents"] = notification_state
            state["active"] = sorted(previous_active)
            state["updated_at"] = now
            save_json(state_path, state)
            return

    for key in resolved_keys:
        state.get("incidents", {}).pop(key, None)
        state.get("issue_details", {}).pop(key, None)

    state["active"] = sorted(current_active)
    state["updated_at"] = now
    save_json(state_path, state)


if __name__ == "__main__":
    try:
        main()
    except Exception as exc:
        print(f"sub2api-feishu-fast-monitor failed: {exc}", file=sys.stderr)
        sys.exit(1)
