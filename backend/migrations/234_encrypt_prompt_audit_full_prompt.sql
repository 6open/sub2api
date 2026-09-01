-- Store prompt-audit findings encrypted at rest. The legacy plaintext column is
-- retained for a startup backfill; application code clears it as each row is
-- encrypted with the deployment's AES-256-GCM key.
ALTER TABLE prompt_audit_events
    ADD COLUMN IF NOT EXISTS full_prompt_ciphertext TEXT NOT NULL DEFAULT '';

