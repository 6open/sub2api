-- Add an internal quota ledger for OpenAI high/xhigh/max requests.
-- It is separate from normal platform quota so exhaustion downgrades effort
-- instead of rejecting the request.

ALTER TABLE user_platform_quotas
    DROP CONSTRAINT IF EXISTS user_platform_quotas_platform_check;

ALTER TABLE user_platform_quotas
    ADD CONSTRAINT user_platform_quotas_platform_check
    CHECK (platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'grok',
                        'kimi', 'zhipu', 'deepseek', 'openai_advanced'));

INSERT INTO user_platform_quotas (
    user_id, platform, weekly_limit_usd, weekly_usage_usd,
    weekly_window_start, created_at, updated_at
)
SELECT id, 'openai_advanced', 100, 0, date_trunc('week', NOW()), NOW(), NOW()
FROM users
WHERE deleted_at IS NULL AND role <> 'admin'
ON CONFLICT (user_id, platform) WHERE deleted_at IS NULL DO NOTHING;
