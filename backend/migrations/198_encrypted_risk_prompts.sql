CREATE TABLE IF NOT EXISTS risk_prompt_records (
    id BIGSERIAL PRIMARY KEY,
    source VARCHAR(32) NOT NULL,
    request_id VARCHAR(255) NOT NULL DEFAULT '',
    user_id BIGINT,
    api_key_id BIGINT,
    group_id BIGINT,
    model VARCHAR(255) NOT NULL DEFAULT '',
    prompt_ciphertext TEXT NOT NULL,
    prompt_hash CHAR(64) NOT NULL,
    truncated BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_risk_prompt_records_expires_at
    ON risk_prompt_records (expires_at);
CREATE INDEX IF NOT EXISTS idx_risk_prompt_records_request_id
    ON risk_prompt_records (request_id);

-- Legacy audit details stored prompts in plaintext. New risk records are
-- encrypted and bounded, so remove the old plaintext immediately.
UPDATE prompt_audit_events SET full_prompt = '' WHERE full_prompt <> '';

