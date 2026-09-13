-- 001_init.sql —— 核心数据层（链上监控 / 跟单 / 订单 / 持仓 / 告警）
-- 说明：金额字段统一 NUMERIC；时间统一 TIMESTAMPTZ（UTC）。

CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- ---------------------------------------------------------------------------
-- 地址画像（跟单筛选）
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS address_profiles (
    id                BIGSERIAL PRIMARY KEY,
    address           VARCHAR(128) NOT NULL,
    chain             VARCHAR(32)  NOT NULL,
    win_rate          NUMERIC(6,5)  NOT NULL DEFAULT 0,
    profit_factor     NUMERIC(10,4) NOT NULL DEFAULT 0,
    max_drawdown      NUMERIC(6,5)  NOT NULL DEFAULT 0,
    avg_hold_seconds  BIGINT        NOT NULL DEFAULT 0,
    consistency       NUMERIC(6,5)  NOT NULL DEFAULT 0,
    size_consistency  NUMERIC(6,5)  NOT NULL DEFAULT 0,
    median_buy_usd    NUMERIC(20,6) NOT NULL DEFAULT 0,
    total_trades      INT           NOT NULL DEFAULT 0,
    winning_trades    INT           NOT NULL DEFAULT 0,
    recent_score      NUMERIC(6,5)  NOT NULL DEFAULT 0,
    tags              TEXT[],
    is_blacklisted    BOOLEAN       NOT NULL DEFAULT FALSE,
    blacklist_reason  TEXT,
    last_active       TIMESTAMPTZ,
    created_at        TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
    UNIQUE (address, chain)
);

CREATE INDEX IF NOT EXISTS idx_profiles_score ON address_profiles (recent_score DESC);
CREATE INDEX IF NOT EXISTS idx_profiles_chain ON address_profiles (chain);
CREATE INDEX IF NOT EXISTS idx_profiles_active ON address_profiles (last_active DESC);

-- ---------------------------------------------------------------------------
-- 交易记录（画像统计原始数据；建议后续按 timestamp 做月分区）
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS trade_records (
    id          BIGSERIAL PRIMARY KEY,
    chain       VARCHAR(32)   NOT NULL,
    address     VARCHAR(128)  NOT NULL,
    token       VARCHAR(128)  NOT NULL,
    side        VARCHAR(8)    NOT NULL CHECK (side IN ('buy', 'sell')),
    amount_usd  NUMERIC(20,6) NOT NULL DEFAULT 0,
    price_usd   NUMERIC(30,12) NOT NULL DEFAULT 0,
    pnl_usd     NUMERIC(20,6) NOT NULL DEFAULT 0,
    tx_hash     VARCHAR(128)  NOT NULL,
    ts          TIMESTAMPTZ   NOT NULL,
    created_at  TIMESTAMPTZ   NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_trades_address_ts ON trade_records (address, ts DESC);
CREATE INDEX IF NOT EXISTS idx_trades_token ON trade_records (token);
CREATE INDEX IF NOT EXISTS idx_trades_chain_ts ON trade_records (chain, ts DESC);
CREATE UNIQUE INDEX IF NOT EXISTS uq_trades_tx_hash_sender ON trade_records (chain, tx_hash, address, token, side);

-- ---------------------------------------------------------------------------
-- 黑名单
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS address_blacklist (
    id         BIGSERIAL PRIMARY KEY,
    chain      VARCHAR(32)  NOT NULL,
    address    VARCHAR(128) NOT NULL,
    reason     TEXT,
    created_at TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    UNIQUE (chain, address)
);

-- ---------------------------------------------------------------------------
-- 信号
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS signals (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    chain           VARCHAR(32)  NOT NULL,
    token           VARCHAR(128) NOT NULL,
    token_symbol    VARCHAR(64),
    source          VARCHAR(16)  NOT NULL,
    trigger_address VARCHAR(128),
    trigger_score   NUMERIC(6,5) NOT NULL DEFAULT 0,
    amount_usd      NUMERIC(20,6) NOT NULL DEFAULT 0,
    price_usd       NUMERIC(30,12) NOT NULL DEFAULT 0,
    liquidity_usd   NUMERIC(20,6) NOT NULL DEFAULT 0,
    volume_spike    NUMERIC(10,4) NOT NULL DEFAULT 0,
    security        JSONB,
    decay           NUMERIC(6,5) NOT NULL DEFAULT 1,
    status          VARCHAR(16)  NOT NULL DEFAULT 'pending',
    reason          TEXT,
    follow_confirmed BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    expires_at      TIMESTAMPTZ,
    confirmed_at    TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_signals_status ON signals (status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_signals_token ON signals (chain, token);

-- ---------------------------------------------------------------------------
-- 订单（状态机）
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS orders (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    chain        VARCHAR(32)  NOT NULL,
    token_in     VARCHAR(128) NOT NULL,
    token_out    VARCHAR(128) NOT NULL,
    token_symbol VARCHAR(64),
    side         VARCHAR(8)   NOT NULL,
    amount_in    NUMERIC(40,0) NOT NULL DEFAULT 0,
    amount_out   NUMERIC(40,0) NOT NULL DEFAULT 0,
    status       VARCHAR(16)  NOT NULL DEFAULT 'pending',
    tx_hash      VARCHAR(128),
    router       VARCHAR(64),
    is_private   BOOLEAN      NOT NULL DEFAULT FALSE,
    slippage_bps INT          NOT NULL DEFAULT 0,
    attempts     INT          NOT NULL DEFAULT 0,
    signal_id    UUID,
    dry_run      BOOLEAN      NOT NULL DEFAULT TRUE,
    error        TEXT,
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_orders_status ON orders (status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_orders_signal ON orders (signal_id);

-- ---------------------------------------------------------------------------
-- 持仓
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS positions (
    id                   UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    chain                VARCHAR(32)  NOT NULL,
    token                VARCHAR(128) NOT NULL,
    token_symbol         VARCHAR(64),
    amount               NUMERIC(40,0) NOT NULL DEFAULT 0,
    decimals             SMALLINT     NOT NULL DEFAULT 18,
    entry_price_usd      NUMERIC(30,12) NOT NULL DEFAULT 0,
    current_price_usd    NUMERIC(30,12) NOT NULL DEFAULT 0,
    peak_price_usd       NUMERIC(30,12) NOT NULL DEFAULT 0,
    invested_usd         NUMERIC(20,6) NOT NULL DEFAULT 0,
    stop_loss_pct        NUMERIC(6,5) NOT NULL DEFAULT 0,
    trailing_stop_pct    NUMERIC(6,5) NOT NULL DEFAULT 0,
    liquidity_at_entry_usd NUMERIC(20,6) NOT NULL DEFAULT 0,
    current_liquidity_usd  NUMERIC(20,6) NOT NULL DEFAULT 0,
    status               VARCHAR(16)  NOT NULL DEFAULT 'open',
    exit_price_usd       NUMERIC(30,12),
    realized_pnl_usd     NUMERIC(20,6),
    signal_id            UUID,
    dry_run              BOOLEAN      NOT NULL DEFAULT TRUE,
    opened_at            TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    closed_at            TIMESTAMPTZ,
    updated_at           TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_positions_status ON positions (status, chain);

-- ---------------------------------------------------------------------------
-- 告警历史
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS alert_history (
    id         UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    level      VARCHAR(16)  NOT NULL,
    title      TEXT,
    message    TEXT,
    chain      VARCHAR(32),
    token      VARCHAR(128),
    address    VARCHAR(128),
    action_url TEXT,
    created_at TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_alert_level_time ON alert_history (level, created_at DESC);
