import importlib.util
import pathlib
import unittest


MODULE_PATH = pathlib.Path(__file__).with_name("sub2api-feishu-monitor.py")
SPEC = importlib.util.spec_from_file_location("sub2api_feishu_monitor", MODULE_PATH)
MONITOR = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MONITOR)


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


if __name__ == "__main__":
    unittest.main()
