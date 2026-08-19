ALTER TABLE api_keys
    ADD COLUMN IF NOT EXISTS is_open_webui_default BOOLEAN NOT NULL DEFAULT FALSE;

CREATE UNIQUE INDEX IF NOT EXISTS idx_api_keys_open_webui_default_per_user
    ON api_keys (user_id)
    WHERE is_open_webui_default = TRUE AND deleted_at IS NULL;
