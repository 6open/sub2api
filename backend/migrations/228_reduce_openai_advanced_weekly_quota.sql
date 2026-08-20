-- Reduce the default free advanced-reasoning allowance without overriding
-- future per-user custom limits.

UPDATE user_platform_quotas
SET weekly_limit_usd = 30,
    updated_at = NOW()
WHERE platform = 'openai_advanced'
  AND deleted_at IS NULL
  AND weekly_limit_usd = 100;
