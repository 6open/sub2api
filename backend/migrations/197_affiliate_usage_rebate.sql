-- Track consumption-based referral rewards independently from legacy recharge rewards.
ALTER TABLE user_affiliate_ledger
    ADD COLUMN IF NOT EXISTS source_request_id VARCHAR(255) NULL;

ALTER TABLE user_affiliate_ledger
    ADD COLUMN IF NOT EXISTS source_api_key_id BIGINT NULL REFERENCES api_keys(id) ON DELETE SET NULL;

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS non_rebatable_balance DECIMAL(20,8) NOT NULL DEFAULT 0;

CREATE UNIQUE INDEX IF NOT EXISTS idx_ual_usage_source_uniq
    ON user_affiliate_ledger(source_request_id, source_api_key_id)
    WHERE action = 'accrue_usage';

CREATE INDEX IF NOT EXISTS idx_ual_usage_invitee
    ON user_affiliate_ledger(user_id, source_user_id, created_at)
    WHERE action = 'accrue_usage';

COMMENT ON COLUMN user_affiliate_ledger.source_request_id IS '产生消费返利的网关请求 ID';
COMMENT ON COLUMN user_affiliate_ledger.source_api_key_id IS '产生消费返利的 API Key；与请求 ID 共同保证幂等';
COMMENT ON COLUMN users.non_rebatable_balance IS '已转入用户余额、但消费时不得再次产生邀请返利的额度';
