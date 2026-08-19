#!/usr/bin/env python3
import base64
import fcntl
import hashlib
import hmac
import json
import os
import re
import subprocess
import sys
import time
import urllib.request


CONFIG_PATH = "/etc/sub2api-feishu-monitor/config.json"
STATE_PATH = "/var/lib/sub2api-feishu-monitor/state.json"
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
        with open(path, "r", encoding="utf-8") as f:
            return json.load(f)
    except FileNotFoundError:
        return default


def save_json(path, data):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    tmp = f"{path}.tmp"
    with open(tmp, "w", encoding="utf-8") as f:
        json.dump(data, f, ensure_ascii=False, indent=2)
        f.write("\n")
    os.replace(tmp, path)


def notification_category(items, fallback="warning"):
    categories = [
        item.get("category", fallback)
        for item in items
        if item.get("category", fallback) in CATEGORY_STYLES
    ]
    return max(categories or [fallback], key=lambda value: CATEGORY_PRIORITY[value])


def run(cmd):
    completed = subprocess.run(
        cmd,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=False,
    )
    if completed.returncode != 0:
        raise RuntimeError(completed.stderr.strip() or completed.stdout.strip())
    return completed.stdout


def psql_json(sql):
    cmd = [
        "docker",
        "compose",
        "exec",
        "-T",
        "postgres",
        "psql",
        "-U",
        "sub2api",
        "-d",
        "sub2api",
        "-t",
        "-A",
        "-c",
        sql,
    ]
    output = run(cmd).strip()
    if not output:
        return []
    return json.loads(output)


def get_accounts(group_name):
    group_sql = "'" + group_name.replace("'", "''") + "'"
    sql = f"""
    select coalesce(json_agg(row_to_json(t)), '[]'::json)
    from (
      select a.id, a.name, a.platform, a.type, a.status, a.schedulable,
             a.error_message, a.last_used_at, a.updated_at
      from accounts a
      join account_groups ag on ag.account_id = a.id
      join groups g on g.id = ag.group_id
      where g.name = {group_sql}
      order by a.id
    ) t;
    """
    return psql_json(sql)


def get_open_alerts(severities):
    severities_sql = ",".join("'" + s.replace("'", "''") + "'" for s in severities)
    sql = f"""
    select coalesce(json_agg(row_to_json(t)), '[]'::json)
    from (
      select id, severity, title, description, fired_at
      from ops_alert_events
      where status = 'firing'
        and severity in ({severities_sql})
      order by fired_at desc
      limit 10
    ) t;
    """
    return psql_json(sql)


def get_recent_http_stats(minutes):
    sql = f"""
    with recent_requests as (
      select l.request_id,
             nullif(l.extra->>'status_code', '')::int as status_code,
             exists (
               select 1
               from ops_error_logs e
               where e.request_id = l.request_id
             ) as has_error_log,
             exists (
               select 1
               from ops_error_logs e
               where e.request_id = l.request_id
                 and e.status_code >= 500
                 and coalesce(e.is_count_tokens, false) = false
                 and coalesce(e.is_business_limited, false) = false
                 and (
                   coalesce(e.upstream_status_code, e.status_code) >= 500
                   or e.network_error_type is not null
                   or e.error_type in ('network_error', 'timeout', 'connection_error')
                 )
             ) as has_severe_error
      from ops_system_logs l
      where l.created_at > now() - interval '{int(minutes)} minutes'
        and l.message = 'http request completed'
    ), stats as (
      select count(*) as total,
             count(*) filter (where status_code >= 400) as errors,
             count(*) filter (where status_code >= 500) as server_errors,
             count(*) filter (
               where status_code >= 500
                 and (not has_error_log or has_severe_error)
             ) as real_server_errors
      from recent_requests
    )
    select json_build_object(
      'total', total,
      'errors', errors,
      'server_errors', server_errors,
      'real_server_errors', real_server_errors,
      'ignored_wrapped_client_errors', server_errors - real_server_errors
    )
    from stats;
    """
    rows = psql_json(sql)
    if isinstance(rows, dict):
        return rows
    return {}


def alert_window_minutes(alert):
    description = alert.get("description") or ""
    match = re.search(r"over last\s+(\d+)m", description)
    if match:
        return max(1, int(match.group(1)))
    return 5


def evaluate_ratio_stats(ratio_filter, minutes, stats):
    total = int(stats.get("total") or 0)
    errors = int(stats.get("errors") or 0)
    server_errors = int(stats.get("server_errors") or 0)
    real_server_errors = int(stats.get("real_server_errors", server_errors) or 0)
    ignored_client_errors = int(stats.get("ignored_wrapped_client_errors") or 0)

    min_requests_by_window = ratio_filter.get("min_requests_by_window", {})
    min_errors_by_window = ratio_filter.get("min_errors_by_window", {})
    min_requests = int(min_requests_by_window.get(str(minutes), ratio_filter.get("min_requests", 30)))
    min_errors = int(min_errors_by_window.get(str(minutes), ratio_filter.get("min_errors", 5)))
    count_5xx_only = bool(ratio_filter.get("count_5xx_only", False))
    effective_errors = real_server_errors if count_5xx_only else errors
    error_rate = effective_errors / total if total else 0.0

    force_min_errors = int(ratio_filter.get("force_min_errors", 8))
    high_rate_min_errors = int(ratio_filter.get("high_rate_min_errors", 5))
    high_error_rate = float(ratio_filter.get("high_error_rate", 0.5))

    normal_volume_trigger = total >= min_requests and effective_errors >= min_errors
    error_count_trigger = effective_errors >= force_min_errors
    high_rate_trigger = (
        effective_errors >= high_rate_min_errors and error_rate >= high_error_rate
    )
    should_alert = normal_volume_trigger or error_count_trigger or high_rate_trigger

    summary = (
        f"最近 {minutes}m 请求 {total}，HTTP 错误 {errors}，原始 5xx {server_errors}，"
        f"真实 5xx {real_server_errors}，排除伪 5xx {ignored_client_errors}，"
        f"真实错误率 {error_rate:.2%}"
    )
    if should_alert:
        return False, "", summary
    return True, f"真实故障样本不足：{summary}", summary


def should_suppress_ratio_alert(config, alert):
    ratio_filter = config.get("ratio_alert_filter", {})
    if not ratio_filter.get("enabled", True):
        return False, ""

    title = alert.get("title") or ""
    description = alert.get("description") or ""
    text = f"{title} {description}"
    if "error_rate" not in text and "success_rate" not in text and "错误率" not in text and "成功率" not in text:
        return False, ""

    minutes = alert_window_minutes(alert)
    stats = get_recent_http_stats(minutes)
    if not stats:
        return False, ""

    suppressed, reason, summary = evaluate_ratio_stats(ratio_filter, minutes, stats)
    if suppressed:
        return True, reason
    alert["body"] = (
        f"{description}\n"
        f"  监控样本：{summary}"
    )
    return False, ""


def get_recent_bad_events(minutes):
    sql = f"""
    select coalesce(json_agg(row_to_json(t)), '[]'::json)
    from (
      select id, severity, title, description, fired_at, resolved_at
      from ops_alert_events
      where created_at > now() - interval '{int(minutes)} minutes'
        and severity in ('P0', 'P1')
      order by created_at desc
      limit 10
    ) t;
    """
    return psql_json(sql)


def probe_network(config, state):
    network = config.get("network_probe", {})
    if not network.get("enabled", True):
        return []

    proxy = network.get("proxy", "http://172.17.0.1:7890")
    url = network.get("url", "https://api.openai.com/v1/models")
    max_time = int(network.get("max_time_seconds", 12))
    slow_ms = int(network.get("slow_threshold_ms", 8000))
    fail_threshold = int(network.get("fail_threshold", 3))
    slow_threshold = int(network.get("slow_threshold", 3))

    start = time.monotonic()
    ok = False
    status = "unknown"
    error = ""
    try:
        output = run([
            "curl",
            "-x",
            proxy,
            "-sS",
            "-o",
            "/dev/null",
            "-w",
            "%{http_code}",
            "--connect-timeout",
            str(min(max_time, 8)),
            "--max-time",
            str(max_time),
            url,
        ]).strip()
        status = output or "000"
        ok = status in {"200", "401", "403"}
    except Exception as exc:
        error = str(exc)

    latency_ms = int((time.monotonic() - start) * 1000)
    network_state = state.setdefault("network_probe", {})
    if ok and latency_ms < slow_ms:
        network_state["fail_count"] = 0
        network_state["slow_count"] = 0
        network_state["last_ok_at"] = int(time.time())
        network_state["last_status"] = status
        network_state["last_latency_ms"] = latency_ms
        return []

    if not ok:
        network_state["fail_count"] = int(network_state.get("fail_count", 0)) + 1
    else:
        network_state["fail_count"] = 0

    if ok and latency_ms >= slow_ms:
        network_state["slow_count"] = int(network_state.get("slow_count", 0)) + 1
    elif not ok:
        network_state["slow_count"] = 0

    network_state["last_status"] = status
    network_state["last_latency_ms"] = latency_ms
    if error:
        network_state["last_error"] = error[-300:]

    issues = []
    if network_state.get("fail_count", 0) >= fail_threshold:
        issues.append({
            "key": "network_fail",
            "category": "error",
            "title": "代理网络连续失败",
            "body": f"7890 到 OpenAI 连续失败 {network_state.get('fail_count')} 次，status={status}，error={error[-180:] or '无'}",
        })
    if network_state.get("slow_count", 0) >= slow_threshold:
        issues.append({
            "key": "network_slow",
            "category": "warning",
            "title": "代理网络持续变慢",
            "body": f"7890 到 OpenAI 连续 {network_state.get('slow_count')} 次超过 {slow_ms}ms，本次 {latency_ms}ms，status={status}",
        })
    return issues


def build_issues(config):
    issues = []
    accounts = get_accounts(config.get("group_name", "gpt20x"))
    active = [
        a for a in accounts
        if a.get("status") == "active" and a.get("schedulable") is True
    ]
    if not active:
        issues.append({
            "key": "no_schedulable_account",
            "category": "error",
            "title": "sub2api 无可调度账号",
            "body": "gpt20x 分组当前没有 active 且 schedulable=true 的账号。",
        })

    for account in accounts:
        status = account.get("status")
        schedulable = account.get("schedulable")
        message = account.get("error_message") or ""
        lowered = message.lower()
        auth_bad = any(
            token in lowered
            for token in ["token revoked", "invalidated oauth", "401", "auth_unavailable", "no auth available"]
        )
        if status != "active" or schedulable is not True or auth_bad:
            issues.append({
                "key": f"account_{account.get('id')}_{status}_{schedulable}_{hashlib.sha1(message.encode()).hexdigest()[:8]}",
                "category": "warning",
                "title": f"账号异常：{account.get('name')}",
                "body": f"status={status}, schedulable={schedulable}, error={message or '无'}",
            })

    for alert in get_open_alerts(config.get("alert_severities", ["P0", "P1"])):
        suppressed, reason = should_suppress_ratio_alert(config, alert)
        if suppressed:
            print(f"suppress alert {alert.get('id')}: {reason}")
            continue
        title = alert.get("title") or "sub2api 告警未恢复"
        # Fingerprint by title so resolve/refire of the same rule does not spam Feishu.
        fingerprint = hashlib.sha1(title.encode()).hexdigest()[:10]
        issues.append({
            "key": f"rule_alert_{fingerprint}",
            "category": "error",
            "title": title,
            "body": alert.get("body") or alert.get("description") or "",
        })

    if config.get("include_recent_resolved_alerts", False):
        for alert in get_recent_bad_events(config.get("recent_alert_minutes", 10)):
            issues.append({
                "key": f"recent_alert_{alert.get('id')}",
                "category": "reminder",
                "title": alert.get("title") or "sub2api 最近告警",
                "body": alert.get("description") or "",
            })

    return issues


def sign(secret, timestamp):
    string_to_sign = f"{timestamp}\n{secret}".encode("utf-8")
    digest = hmac.new(string_to_sign, b"", digestmod=hashlib.sha256).digest()
    return base64.b64encode(digest).decode("utf-8")


def send_feishu(config, issues, category=None):
    category = category or notification_category(issues)
    icon, category_label = CATEGORY_STYLES[category]
    now = int(time.time())
    timestamp = str(now)
    service_label = config.get("service_label", "sub2api")
    lines = [
        f"{icon} {category_label}｜{service_label}",
        "",
        f"时间：{time.strftime('%Y-%m-%d %H:%M:%S %z')}",
    ]
    for issue in issues:
        lines.append("")
        lines.append(f"- {issue['title']}")
        if issue.get("body"):
            lines.append(f"  {issue['body']}")

    payload = {
        "timestamp": timestamp,
        "msg_type": "text",
        "content": {"text": "\n".join(lines)},
    }
    secret = config.get("secret", "")
    if secret:
        payload["sign"] = sign(secret, timestamp)

    rate_state_path = config.get(
        "notification_rate_limit_state_path", NOTIFICATION_RATE_STATE_PATH
    )
    interval = max(60, int(config.get("non_normal_min_interval_seconds", 60)))
    os.makedirs(os.path.dirname(rate_state_path), exist_ok=True)
    with open(f"{rate_state_path}.lock", "a+", encoding="utf-8") as lock_handle:
        fcntl.flock(lock_handle.fileno(), fcntl.LOCK_EX)
        rate_state = load_json(rate_state_path, {})
        last_sent_at = int(rate_state.get("last_non_normal_sent_at", 0))
        if category != "normal" and now - last_sent_at < interval:
            wait_seconds = interval - (now - last_sent_at)
            print(f"defer {category} notification for {wait_seconds}s due to rate limit")
            return False

        req = urllib.request.Request(
            config["webhook"],
            data=json.dumps(payload).encode("utf-8"),
            headers={"Content-Type": "application/json"},
            method="POST",
        )
        with urllib.request.urlopen(req, timeout=10) as response:
            body = response.read().decode("utf-8", "replace")
            if response.status >= 300:
                raise RuntimeError(body)
            data = json.loads(body)
            if data.get("code") != 0:
                raise RuntimeError(body)

        if category != "normal":
            save_json(rate_state_path, {
                "last_non_normal_sent_at": now,
                "last_category": category,
                "last_service": service_label,
            })
        return True


def main():
    config = load_json(CONFIG_PATH, {})
    if not config.get("webhook"):
        raise SystemExit("webhook is not configured")

    state = load_json(STATE_PATH, {})
    issues = build_issues(config)
    issues.extend(probe_network(config, state))

    current_keys = {issue["key"] for issue in issues}
    current_titles = {issue["key"]: issue["title"] for issue in issues}
    if "active_issue_keys" not in state:
        # First run after upgrading: establish a baseline without replaying old alerts.
        state["active_issue_keys"] = sorted(current_keys)
        state["active_issue_titles"] = current_titles
        state["last_status"] = "alert" if current_keys else "ok"
        save_json(STATE_PATH, state)
        return

    previous_keys = set(state.get("active_issue_keys", []))
    previous_titles = state.get("active_issue_titles", {})
    sendable = [issue for issue in issues if issue["key"] not in previous_keys]
    resolved = [
        {
            "category": "normal",
            "title": f"已恢复：{previous_titles.get(key, key)}",
            "body": "该异常已不再出现。",
        }
        for key in sorted(previous_keys - current_keys)
    ]

    tracked_keys = set(previous_keys)
    tracked_titles = dict(previous_titles)
    if resolved:
        send_feishu(config, resolved, category="normal")
        for key in previous_keys - current_keys:
            tracked_keys.discard(key)
            tracked_titles.pop(key, None)

    if sendable and send_feishu(config, sendable):
        for issue in sendable:
            tracked_keys.add(issue["key"])
            tracked_titles[issue["key"]] = issue["title"]

    state["active_issue_keys"] = sorted(tracked_keys)
    state["active_issue_titles"] = tracked_titles
    state["last_status"] = "alert" if current_keys else "ok"
    save_json(STATE_PATH, state)


if __name__ == "__main__":
    try:
        main()
    except Exception as exc:
        print(f"sub2api-feishu-monitor failed: {exc}", file=sys.stderr)
        sys.exit(1)
