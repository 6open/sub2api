CREATE UNIQUE INDEX IF NOT EXISTS idx_api_keys_open_webui_image_per_user_group
    ON api_keys (user_id, group_id)
    WHERE name = 'Open WebUI Image' AND deleted_at IS NULL;
