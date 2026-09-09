CREATE TABLE IF NOT EXISTS lklb_usage_outcomes (
 usage_log_id bigint PRIMARY KEY REFERENCES usage_logs(id) ON DELETE CASCADE,
 outcome text NOT NULL CHECK (outcome IN ('completed','failed','unknown')),
 error_code text NOT NULL DEFAULT '',
 first_output_ms integer CHECK (first_output_ms >= 0)
);
