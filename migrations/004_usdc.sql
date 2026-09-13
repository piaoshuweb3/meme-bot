-- 004_usdc.sql —— 链上 USDC 收款闭环（付款 → 订阅激活 → 返佣）
-- 幂等：可重复执行。

CREATE TABLE IF NOT EXISTS usdc_payments (
    id          BIGSERIAL PRIMARY KEY,
    order_id    VARCHAR(64)   NOT NULL,
    tx_hash     VARCHAR(128)  NOT NULL UNIQUE,  -- 同一链上交易只落库一次（防重复激活）
    payer       VARCHAR(128),
    amount      NUMERIC(20,6) NOT NULL DEFAULT 0,
    asset       VARCHAR(64)   NOT NULL DEFAULT 'usdc',
    network     VARCHAR(32),
    status      VARCHAR(16)   NOT NULL DEFAULT 'confirmed',
    created_at  TIMESTAMPTZ   NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_usdc_payments_order ON usdc_payments (order_id);
CREATE INDEX IF NOT EXISTS idx_usdc_payments_time ON usdc_payments (created_at DESC);

-- payment_orders 补充链上支付信息（便于对账与排障）
ALTER TABLE payment_orders ADD COLUMN IF NOT EXISTS tx_hash   VARCHAR(128);
ALTER TABLE payment_orders ADD COLUMN IF NOT EXISTS payer     VARCHAR(128);
ALTER TABLE payment_orders ADD COLUMN IF NOT EXISTS paid_at   TIMESTAMPTZ;
