-- 005_outcomes.sql —— 影子表现跟踪（验证"过滤器本身"是否有效）
--
-- 动机：回测只能回答"策略在历史数据上如何"，回答不了"我今天的拒绝是否是错拒"。
-- 因此对被过滤的候选做**稳定抽样**并跟踪其后续表现，与放行样本形成对照；
-- 没有对照样本，过滤器只会越调越自信而无法证伪（参考 nhovongoc0-max/meme-radar 的 outcomes 设计）。
--
-- 幂等：可重复执行。

CREATE TABLE IF NOT EXISTS signal_outcomes (
    id               BIGSERIAL PRIMARY KEY,
    chain            VARCHAR(32)   NOT NULL,
    token            VARCHAR(128)  NOT NULL,
    -- 决策分层：FILTERED 被拦 / SIGNALED 生成信号未成交 / EXECUTED 实际成交
    decision         VARCHAR(16)   NOT NULL,
    reason           VARCHAR(128),                              -- 被拦的漏斗环节
    baseline_at      TIMESTAMPTZ   NOT NULL,
    baseline_price   NUMERIC(30,12) NOT NULL DEFAULT 0,
    -- 抽样方式：FULL 全量（信号/成交）｜HASH_MOD5 稳定 1/5（被过滤样本，防记录爆炸）
    sampling         VARCHAR(16)   NOT NULL DEFAULT 'FULL',
    strategy_version VARCHAR(32)   NOT NULL DEFAULT 'v1',
    metric           JSONB,                                     -- 决策时上下文（流动性/突增倍数/评分等）
    created_at       TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
    UNIQUE (chain, token, decision, baseline_at)
);

CREATE INDEX IF NOT EXISTS idx_outcomes_decision_time ON signal_outcomes (decision, baseline_at DESC);
CREATE INDEX IF NOT EXISTS idx_outcomes_token ON signal_outcomes (chain, token);

CREATE TABLE IF NOT EXISTS signal_outcome_samples (
    id         BIGSERIAL PRIMARY KEY,
    outcome_id BIGINT      NOT NULL REFERENCES signal_outcomes(id) ON DELETE CASCADE,
    horizon    VARCHAR(8)  NOT NULL,                            -- m5/m15/m30/h1/h2/h6/h24
    target_at  TIMESTAMPTZ NOT NULL,
    sampled_at TIMESTAMPTZ,
    price      NUMERIC(30,12),
    ret        NUMERIC(20,8),                                   -- price/baseline - 1
    attempts   INT         NOT NULL DEFAULT 0,
    next_at    TIMESTAMPTZ,
    error_code VARCHAR(32),
    UNIQUE (outcome_id, horizon)
);

-- 到期未采样的索引（worker 扫描用）：部分索引避免全表扫描
CREATE INDEX IF NOT EXISTS idx_outcome_samples_due
    ON signal_outcome_samples (next_at, target_at) WHERE sampled_at IS NULL;
