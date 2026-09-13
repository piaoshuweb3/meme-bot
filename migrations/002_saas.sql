-- 002_saas.sql —— SaaS 层：用户 / 角色 / 套餐 / 订阅 / 直推返佣

-- ---------------------------------------------------------------------------
-- 用户（含角色、推荐关系、外部 API Key）
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS users (
    id             BIGSERIAL PRIMARY KEY,
    email          VARCHAR(128) NOT NULL UNIQUE,
    password_hash  TEXT         NOT NULL,
    role           VARCHAR(32)  NOT NULL DEFAULT 'user',   -- super_admin | admin | user
    status         VARCHAR(16)  NOT NULL DEFAULT 'active', -- active | banned
    referral_code  VARCHAR(16)  NOT NULL UNIQUE,
    referred_by    BIGINT REFERENCES users(id),
    api_key        VARCHAR(64)  UNIQUE,
    created_at     TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_users_referral ON users (referral_code);
CREATE INDEX IF NOT EXISTS idx_users_role ON users (role);

-- ---------------------------------------------------------------------------
-- 套餐
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS plans (
    id              SERIAL PRIMARY KEY,
    name            VARCHAR(64)   NOT NULL,
    price_monthly   NUMERIC(10,2) NOT NULL DEFAULT 0,
    max_signals_day INT           NOT NULL DEFAULT 100,
    max_api_calls   INT           NOT NULL DEFAULT 1000,
    features        JSONB         NOT NULL DEFAULT '{}'::jsonb,
    is_active       BOOLEAN       NOT NULL DEFAULT TRUE,
    created_at      TIMESTAMPTZ   NOT NULL DEFAULT NOW()
);

-- ---------------------------------------------------------------------------
-- 订阅（每用户一条有效记录）
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS subscriptions (
    id                 BIGSERIAL PRIMARY KEY,
    user_id            BIGINT      NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
    plan_id            INT         REFERENCES plans(id),
    status             VARCHAR(16) NOT NULL DEFAULT 'inactive', -- active | expired | cancelled | inactive
    current_period_end TIMESTAMPTZ,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_subscriptions_status ON subscriptions (status);

-- ---------------------------------------------------------------------------
-- 支付订单（幂等：回调按 order_id 去重）
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS payment_orders (
    id          BIGSERIAL PRIMARY KEY,
    order_id    VARCHAR(64)   NOT NULL UNIQUE,
    user_id     BIGINT        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    plan_id     INT           NOT NULL REFERENCES plans(id),
    amount      NUMERIC(12,4) NOT NULL DEFAULT 0,
    currency    VARCHAR(16)   NOT NULL DEFAULT 'USD',
    status      VARCHAR(16)   NOT NULL DEFAULT 'pending', -- pending | succeeded | failed
    created_at  TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ   NOT NULL DEFAULT NOW()
);

-- ---------------------------------------------------------------------------
-- 返佣（直推）
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS commissions (
    id            BIGSERIAL PRIMARY KEY,
    from_user_id  BIGINT        NOT NULL,
    to_user_id    BIGINT        NOT NULL,
    amount        NUMERIC(12,4) NOT NULL,
    rate          NUMERIC(6,5)  NOT NULL,
    order_id      VARCHAR(64),
    status        VARCHAR(16)   NOT NULL DEFAULT 'pending', -- pending | settled | rejected
    created_at    TIMESTAMPTZ   NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_commissions_to_user ON commissions (to_user_id, created_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS uq_commissions_order_user ON commissions (order_id, to_user_id);

-- ---------------------------------------------------------------------------
-- API Key 调用配额（按天计数）
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS api_usage (
    id         BIGSERIAL PRIMARY KEY,
    user_id    BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    day        DATE        NOT NULL DEFAULT CURRENT_DATE,
    calls      INT         NOT NULL DEFAULT 0,
    UNIQUE (user_id, day)
);

-- ---------------------------------------------------------------------------
-- 基础套餐种子数据（幂等）
-- ---------------------------------------------------------------------------
INSERT INTO plans (name, price_monthly, max_signals_day, max_api_calls, features)
VALUES
    ('Free',  0,   20,   200,  '{"signals":true,"api":false,"agent":false}'::jsonb),
    ('Pro',   49,  500,  10000,'{"signals":true,"api":true,"agent":false}'::jsonb),
    ('Elite', 199, 5000, 100000,'{"signals":true,"api":true,"agent":true}'::jsonb)
ON CONFLICT DO NOTHING;
