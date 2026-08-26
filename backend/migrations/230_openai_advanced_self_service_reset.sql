-- Give every non-admin user one lifetime self-service reset for the internal
-- OpenAI advanced-reasoning weekly quota. The credit is consumed atomically
-- with the weekly usage reset; it is not replenished by weekly rollover.

ALTER TABLE user_platform_quotas
    ADD COLUMN IF NOT EXISTS self_service_reset_credits INTEGER NOT NULL DEFAULT 0;

ALTER TABLE user_platform_quotas
    ADD COLUMN IF NOT EXISTS self_service_reset_granted BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE user_platform_quotas
    DROP CONSTRAINT IF EXISTS user_platform_quotas_self_service_reset_credits_check;

ALTER TABLE user_platform_quotas
    ADD CONSTRAINT user_platform_quotas_self_service_reset_credits_check
    CHECK (self_service_reset_credits >= 0);

UPDATE user_platform_quotas q
SET self_service_reset_credits = 1,
	self_service_reset_granted = TRUE,
    updated_at = NOW()
FROM users u
WHERE q.user_id = u.id
  AND q.platform = 'openai_advanced'
  AND q.deleted_at IS NULL
  AND u.deleted_at IS NULL
  AND u.role <> 'admin'
	AND q.self_service_reset_granted = FALSE;
