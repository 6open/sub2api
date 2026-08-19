import importlib.util
import json
import pathlib
import tempfile
import unittest
from unittest import mock


MODULE_PATH = pathlib.Path(__file__).with_name("sub2api-feishu-monitor.py")
SPEC = importlib.util.spec_from_file_location("sub2api_feishu_monitor", MODULE_PATH)
MONITOR = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MONITOR)

FAST_MODULE_PATH = pathlib.Path(__file__).with_name("sub2api-feishu-fast-monitor.py")
FAST_SPEC = importlib.util.spec_from_file_location(
    "sub2api_feishu_fast_monitor", FAST_MODULE_PATH
)
FAST_MONITOR = importlib.util.module_from_spec(FAST_SPEC)
FAST_SPEC.loader.exec_module(FAST_MONITOR)


class EvaluateRatioStatsTest(unittest.TestCase):
    def setUp(self):
        self.config = {
            "min_requests": 50,
            "min_errors": 8,
            "min_requests_by_window": {"5": 50, "10": 80},
            "min_errors_by_window": {"5": 8, "10": 10},
            "count_5xx_only": True,
            "force_min_errors": 8,
            "high_rate_min_errors": 5,
            "high_error_rate": 0.5,
        }

    def evaluate(self, stats, minutes=5):
        return MONITOR.evaluate_ratio_stats(self.config, minutes, stats)

    def test_wrapped_upstream_400_errors_are_suppressed(self):
        suppressed, reason, summary = self.evaluate({
            "total": 40,
            "errors": 30,
            "server_errors": 30,
            "real_server_errors": 0,
            "ignored_wrapped_client_errors": 30,
        })

        self.assertTrue(suppressed)
        self.assertIn("真实 5xx 0", reason)
        self.assertIn("排除伪 5xx 30", summary)

    def test_eight_real_errors_alert_even_below_request_floor(self):
        suppressed, _, _ = self.evaluate({
            "total": 20,
            "errors": 8,
            "server_errors": 8,
            "real_server_errors": 8,
        })

        self.assertFalse(suppressed)

    def test_five_real_errors_alert_at_fifty_percent(self):
        suppressed, _, _ = self.evaluate({
            "total": 10,
            "errors": 5,
            "server_errors": 5,
            "real_server_errors": 5,
        })

        self.assertFalse(suppressed)

    def test_five_real_errors_are_suppressed_below_fifty_percent(self):
        suppressed, _, _ = self.evaluate({
            "total": 20,
            "errors": 5,
            "server_errors": 5,
            "real_server_errors": 5,
        })

        self.assertTrue(suppressed)

    def test_missing_new_fields_falls_back_to_raw_server_errors(self):
        suppressed, _, _ = self.evaluate({
            "total": 10,
            "errors": 8,
            "server_errors": 8,
        })

        self.assertFalse(suppressed)


class NotificationTest(unittest.TestCase):
    class Response:
        status = 200

        def __enter__(self):
            return self

        def __exit__(self, *_args):
            return False

        def read(self):
            return b'{"code":0}'

    def test_category_priority_and_light_yellow_reminder(self):
        self.assertEqual(
            MONITOR.notification_category([
                {"category": "reminder"},
                {"category": "warning"},
            ]),
            "warning",
        )
        self.assertEqual(MONITOR.CATEGORY_STYLES["reminder"], ("🟡", "提醒"))

    def test_non_normal_is_rate_limited_but_normal_is_not(self):
        with tempfile.TemporaryDirectory() as directory:
            config = {
                "webhook": "https://example.invalid/hook",
                "service_label": "test",
                "notification_rate_limit_state_path": str(
                    pathlib.Path(directory) / "rate.json"
                ),
            }
            warning = [{"category": "warning", "title": "warning", "body": "x"}]
            error = [{"category": "error", "title": "error", "body": "y"}]
            normal = [{"category": "normal", "title": "recovered", "body": "z"}]

            with mock.patch.object(
                MONITOR.urllib.request,
                "urlopen",
                side_effect=lambda *_args, **_kwargs: self.Response(),
            ) as urlopen:
                self.assertTrue(MONITOR.send_feishu(config, warning))
                self.assertFalse(MONITOR.send_feishu(config, error))
                self.assertTrue(MONITOR.send_feishu(config, normal))

            self.assertEqual(urlopen.call_count, 2)
            first_payload = json.loads(urlopen.call_args_list[0].args[0].data)
            normal_payload = json.loads(urlopen.call_args_list[1].args[0].data)
            self.assertTrue(first_payload["content"]["text"].startswith("🟠 警告"))
            self.assertTrue(normal_payload["content"]["text"].startswith("🟢 正常"))


class FastNotificationTest(unittest.TestCase):
    def test_slow_request_query_uses_context_aware_thresholds(self):
        completed = mock.Mock(returncode=0, stdout="[]", stderr="")
        config = {
            "window_seconds": 180,
            "ttft_threshold_ms": 30000,
            "medium_context_tokens": 50000,
            "medium_context_ttft_threshold_ms": 60000,
            "long_context_tokens": 100000,
            "long_context_ttft_threshold_ms": 90000,
            "deep_reasoning_ttft_threshold_ms": 60000,
        }

        with mock.patch.object(
            FAST_MONITOR.subprocess, "run", return_value=completed
        ) as run:
            self.assertEqual(FAST_MONITOR.query_slow_ai_requests(config), [])

        sql = run.call_args.kwargs["input"]
        self.assertIn("context_tokens >= 100000 then 90000", sql)
        self.assertIn("context_tokens >= 50000 then 60000", sql)
        self.assertIn("reasoning_effort in ('xhigh', 'max') then 60000", sql)
        self.assertIn("first_token_ms >= effective_threshold_ms", sql)

    def test_fast_categories_and_rate_limit(self):
        self.assertEqual(
            FAST_MONITOR.notification_category(
                [{"category": "reminder"}], []
            ),
            "reminder",
        )
        self.assertEqual(
            FAST_MONITOR.CATEGORY_STYLES["reminder"], ("🟡", "提醒")
        )
        with tempfile.TemporaryDirectory() as directory:
            feishu_config = {
                "webhook": "https://example.invalid/hook",
                "notification_rate_limit_state_path": str(
                    pathlib.Path(directory) / "rate.json"
                ),
            }
            monitor_config = {"service_label": "fast-test"}
            issue = [{"category": "error", "title": "down", "body": "x"}]
            reminder = [{"category": "reminder", "title": "still down", "body": "y"}]
            resolved = [{"title": "down"}]

            with mock.patch.object(
                FAST_MONITOR.urllib.request,
                "urlopen",
                side_effect=lambda *_args, **_kwargs: NotificationTest.Response(),
            ) as urlopen:
                self.assertTrue(
                    FAST_MONITOR.send_feishu(
                        feishu_config, monitor_config, issue, []
                    )
                )
                self.assertFalse(
                    FAST_MONITOR.send_feishu(
                        feishu_config, monitor_config, reminder, []
                    )
                )
                self.assertTrue(
                    FAST_MONITOR.send_feishu(
                        feishu_config, monitor_config, [], resolved
                    )
                )

            self.assertEqual(urlopen.call_count, 2)
            error_payload = json.loads(urlopen.call_args_list[0].args[0].data)
            normal_payload = json.loads(urlopen.call_args_list[1].args[0].data)
            self.assertTrue(error_payload["content"]["text"].startswith("🔴 错误"))
            self.assertTrue(normal_payload["content"]["text"].startswith("🟢 正常"))

if __name__ == "__main__":
    unittest.main()
