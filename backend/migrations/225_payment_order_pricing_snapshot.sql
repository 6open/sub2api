ALTER TABLE payment_orders
    ADD COLUMN IF NOT EXISTS pricing_snapshot JSONB;
