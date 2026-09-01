-- Move Migo advanced quota from 0.1 x $50 with two resets to 0.2 x $100
-- with one reset. Doubling both usage and the limit preserves every user's
-- consumed percentage while the reset allowance is capped at one.

UPDATE user_platform_quotas q
SET daily_usage_usd = q.daily_usage_usd * 2,
    weekly_usage_usd = q.weekly_usage_usd * 2,
    monthly_usage_usd = q.monthly_usage_usd * 2,
    weekly_limit_usd = 100,
    self_service_reset_credits = LEAST(q.self_service_reset_credits, 1),
    updated_at = NOW()
FROM users u
WHERE q.user_id = u.id
  AND q.platform = 'openai_advanced'
  AND q.deleted_at IS NULL
  AND u.deleted_at IS NULL
  AND u.role <> 'admin';

COMMENT ON COLUMN user_platform_quotas.self_service_reset_credits IS
    'Remaining self-service resets in the current weekly quota window; openai_advanced replenishes to one on weekly rollover';
