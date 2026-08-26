-- Migration 230 introduced the reset counter as an initial one-time grant.
-- The application now replenishes it to one whenever the advanced quota's
-- weekly window rolls over. Keep the historical grant marker for compatibility;
-- it only guards initial seeding and no longer controls weekly replenishment.

COMMENT ON COLUMN user_platform_quotas.self_service_reset_credits IS
    'Remaining self-service resets in the current weekly quota window; openai_advanced replenishes to one on weekly rollover';

COMMENT ON COLUMN user_platform_quotas.self_service_reset_granted IS
    'Initial-seed marker retained for compatibility; weekly replenishment follows weekly_window_start';
