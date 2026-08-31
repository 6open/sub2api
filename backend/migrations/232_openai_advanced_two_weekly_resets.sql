-- Increase the weekly advanced-quota reset allowance from one to two.
-- Existing records receive exactly one additional credit, capped at the new
-- weekly maximum. This preserves already-consumed resets and never changes
-- quota usage or window timestamps.

UPDATE user_platform_quotas q
SET self_service_reset_credits = LEAST(q.self_service_reset_credits + 1, 2),
    self_service_reset_granted = TRUE,
    updated_at = NOW()
FROM users u
WHERE q.user_id = u.id
  AND q.platform = 'openai_advanced'
  AND q.deleted_at IS NULL
  AND u.deleted_at IS NULL
  AND u.role <> 'admin';

COMMENT ON COLUMN user_platform_quotas.self_service_reset_credits IS
    'Remaining self-service resets in the current weekly quota window; openai_advanced replenishes to two on weekly rollover';
