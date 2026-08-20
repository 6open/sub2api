-- Raise the default advanced-reasoning allowance while preserving any
-- independently customized per-user limits.

UPDATE user_platform_quotas
SET weekly_limit_usd = 50,
    updated_at = NOW()
WHERE platform = 'openai_advanced'
  AND deleted_at IS NULL
  AND weekly_limit_usd = 30
  AND NOW() >= TIMESTAMPTZ '2026-08-24 00:00:00+08';
