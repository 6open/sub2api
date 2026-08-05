ALTER TABLE users
    ADD COLUMN IF NOT EXISTS signup_ip VARCHAR(64) NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_users_signup_ip
    ON users (signup_ip)
    WHERE deleted_at IS NULL AND signup_ip <> '';
