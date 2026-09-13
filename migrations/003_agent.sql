-- 003_agent.sql —— LLM Agent 层：运行记录 / 步骤轨迹 / 工具调用 / 长期记忆 / 空投任务

-- ---------------------------------------------------------------------------
-- Agent 运行（一次任务 = 一条 run，可回放）
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS agent_runs (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    agent_name   VARCHAR(64)  NOT NULL,
    user_id      BIGINT,
    input        TEXT         NOT NULL,
    output       TEXT,
    status       VARCHAR(16)  NOT NULL DEFAULT 'running', -- running | succeeded | failed | cancelled
    steps        INT          NOT NULL DEFAULT 0,
    tokens_used  INT          NOT NULL DEFAULT 0,
    cost_usd     NUMERIC(12,6) NOT NULL DEFAULT 0,
    error        TEXT,
    started_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    finished_at  TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_agent_runs_status ON agent_runs (status, started_at DESC);

-- ---------------------------------------------------------------------------
-- Agent 步骤轨迹（Observe → Reason → Plan → Act → Reflect 全量留痕）
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS agent_steps (
    id          BIGSERIAL PRIMARY KEY,
    run_id      UUID        NOT NULL REFERENCES agent_runs(id) ON DELETE CASCADE,
    step_index  INT         NOT NULL,
    phase       VARCHAR(16) NOT NULL, -- observe | reason | plan | act | reflect
    thought     TEXT,
    tool_name   VARCHAR(64),
    tool_args   JSONB,
    tool_result TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_agent_steps_run ON agent_steps (run_id, step_index);

-- ---------------------------------------------------------------------------
-- 人工确认队列（Agent 高风险动作需人工放行）
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS agent_approvals (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    run_id      UUID        NOT NULL REFERENCES agent_runs(id) ON DELETE CASCADE,
    action      VARCHAR(64) NOT NULL,
    payload     JSONB       NOT NULL DEFAULT '{}'::jsonb,
    status      VARCHAR(16) NOT NULL DEFAULT 'pending', -- pending | approved | rejected | expired
    decided_by  BIGINT,
    decided_at  TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_agent_approvals_status ON agent_approvals (status, created_at DESC);

-- ---------------------------------------------------------------------------
-- 长期记忆（结构化 + 可选向量列，便于后续接 pgvector）
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS agent_memories (
    id         BIGSERIAL PRIMARY KEY,
    agent_name VARCHAR(64)  NOT NULL,
    scope      VARCHAR(64)  NOT NULL DEFAULT 'default',
    kind       VARCHAR(32)  NOT NULL DEFAULT 'fact', -- fact | decision | outcome
    content    TEXT         NOT NULL,
    metadata   JSONB        NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_agent_memories_scope ON agent_memories (agent_name, scope, created_at DESC);

-- ---------------------------------------------------------------------------
-- 空投 / 任务监控（Agent 的场景化状态机）
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS airdrop_tasks (
    id            BIGSERIAL PRIMARY KEY,
    project       VARCHAR(128) NOT NULL,
    chain         VARCHAR(32)  NOT NULL,
    task_key      VARCHAR(128) NOT NULL,
    description   TEXT,
    snapshot_at   TIMESTAMPTZ,
    deadline_at   TIMESTAMPTZ,
    status        VARCHAR(16)  NOT NULL DEFAULT 'pending', -- pending | eligible | done | skipped
    wallet        VARCHAR(128),
    last_checked  TIMESTAMPTZ,
    metadata      JSONB        NOT NULL DEFAULT '{}'::jsonb,
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    UNIQUE (project, task_key, wallet)
);

CREATE INDEX IF NOT EXISTS idx_airdrop_status ON airdrop_tasks (status, snapshot_at);

-- ---------------------------------------------------------------------------
-- x402 微支付流水（Agent 自主付费调用服务）
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS x402_payments (
    id          BIGSERIAL PRIMARY KEY,
    resource    TEXT          NOT NULL,
    pay_to      VARCHAR(128)  NOT NULL,
    chain       VARCHAR(32)   NOT NULL,
    token       VARCHAR(128)  NOT NULL,
    amount      NUMERIC(30,0) NOT NULL DEFAULT 0,
    tx_hash     VARCHAR(128),
    status      VARCHAR(16)   NOT NULL DEFAULT 'pending',
    created_at  TIMESTAMPTZ   NOT NULL DEFAULT NOW()
);
