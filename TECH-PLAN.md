# meme-bot 技术框架与开发计划（基于《MEME.docx》文书分析）

> 文书来源：`C:\Users\Administrator\Desktop\meme-bot\MEME.docx`（与 `.reasonix/attachments/clipboard-20260913-203654.561380-000001.docx` 同一份，7,979 行）
> 本文档 = 文书内容精炼 + 架构定稿 + 技术实现路径 + 阶段目标 + 并行开发分工
> 版本：v1.0 ｜ 状态：已评审，进入实施

---

## 0. 一页速览

| 项目 | 结论 |
|------|------|
| 系统定位 | 低频 · 高精度 · 强风控的迷你币（Micro-cap）监控 + 聪明钱跟单 + 自动化执行 + SaaS 订阅 + LLM Agent 平台 |
| 主链 | **Base（优先）**、**Solana**，第三条链（EVM 兼容）可零改动接入 |
| 后端 | **Go 1.23+**（本机 1.25.4），goroutine/channel 天然事件驱动 |
| 数据 | PostgreSQL 16 + Redis 7（+ 可选 TimescaleDB/ClickHouse 时序扩展） |
| 展示 | Telegram Bot（实时）· Next.js Web Dashboard（专业）· Flutter App（iOS/Android 一套代码） |
| 差异化 | 不与 MEV 机器人拼毫秒速度，走「逻辑密集型 + 多步推理 + 长期状态跟踪」的 Agent 路线 |
| 上线节奏 | 6–8 周 MVP；本仓库按 阶段 0 → 阶段 6 递进，阶段内多线程并行 |
| 安全基线 | 默认 **DRY-RUN 模拟模式**；私钥永不落盘明文（环境变量/KMS）；所有执行必经风控层 + 人工熔断 |

---

## 1. 文书分析结论

### 1.1 文书原框架的优点
- 强调「少即是多」与**漏斗式过滤**，契合迷你币高噪音特性。
- 覆盖链上数据、安全审计、滑点控制、硬性止损等关键点。
- 风险意识强：仓位限制、黑天鹅应对、合规提醒。

### 1.2 文书自陈的不足（本方案逐条解决）
| # | 不足 | 本方案对策 |
|---|------|-----------|
| 1 | 监控层偏静态，缺实时异常检测与多源数据融合优先级 | 引入**事件驱动流处理**（WebSocket → channel/Stream）+ 数据源优先级矩阵 + 信号衰减机制 |
| 2 | 跟单（Copy Trading）几乎空白 | 独立 `internal/address` 模块：地址画像 + 四层筛选漏斗 + 实时动态评分 + 黑名单 |
| 3 | 执行层偏理想化，缺低延迟设施/重试/订单状态机 | `internal/strategy/execution` 订单状态机（Pending→Submitted→Confirming→Filled/Failed/Partial）+ 重试补偿 + 私有通道 |
| 4 | 缺可观测性（Metrics + Tracing）与故障自愈 | Prometheus + Grafana 指标、结构化 zap 日志、Docker 健康检查、RPC 多节点自动切换 |
| 5 | 多链落地时 RPC/Gas/MEV 差异巨大 | `internal/chain` 统一 `Adapter` 接口 + Factory 注册 + 配置驱动 |

### 1.3 文书追加的扩展点（已纳入本方案）
- **SaaS 化**：用户 / 角色 / JWT / API Key、套餐订阅、支付回调、直推返佣（20%）。
- **数据源集成**：pump.fun、GMGN、Birdeye、DexScreener、Bitquery、Moralis，统一 `DataProvider` 接口。
- **LLM Agent 层**：Agent 循环（Observe→Reason→Plan→Act→Reflect）+ ToolRegistry + Memory + PreAct 风控钩子。
- **x402 微支付**：Agent 自主付费调用付费数据源。
- **SEO / GEO**：Next.js SSR + JSON-LD + `llms.txt` 风格 AI 友好内容。
- **合规边界**：文书明确「仅供技术架构参考，不构成投资建议」——本项目同样以**技术研究/工具**定位交付。

---

## 2. 总体架构

### 2.1 分层视图

```
┌──────────────────────────────────────────────────────────────┐
│  接入层  Telegram Bot  |  Web Dashboard(Next.js)  |  Flutter  │
│          |  第三方 API（API Key + 配额）                       │
└───────────────────────────┬──────────────────────────────────┘
                            │  REST / WebSocket
┌───────────────────────────▼──────────────────────────────────┐
│  API Gateway（Gin）：鉴权(JWT/APIKey) · 限流 · 路由 · 审计      │
└───────────────────────────┬──────────────────────────────────┘
                            │
┌───────────────────────────▼──────────────────────────────────┐
│  Agent 层（internal/agent）                                    │
│   Orchestrator(LLM) → ToolRegistry → Memory → PreAct 风控钩子  │
└───────────────────────────┬──────────────────────────────────┘
                            │
   ┌──────────────┬─────────┴────────┬──────────────┐
┌──▼───────────┐ ┌▼──────────────┐ ┌▼────────────┐ ┌▼─────────────┐
│ Address      │ │ Strategy      │ │ Risk        │ │ Alert        │
│ Scorer 跟单  │ │ Engine 信号   │ │ Engine 风控 │ │ Manager 告警 │
└──┬───────────┘ └┬──────────────┘ └┬────────────┘ └┬─────────────┘
   └──────────────┴─────────┬───────┴───────────────┘
                            │
┌───────────────────────────▼──────────────────────────────────┐
│  Chain Factory（internal/chain）— 统一 Adapter 抽象            │
│  base/  solana/  <future-chain>/  private_tx/  aggregator/    │
└───────────────────────────┬──────────────────────────────────┘
                            │
┌───────────────────────────▼──────────────────────────────────┐
│  Data Layer（internal/provider + internal/storage）            │
│  链上事件 · 行情 · 安全审计 · 社交情绪 · PG/Redis/时序库       │
└──────────────────────────────────────────────────────────────┘
```

### 2.2 全自动数据流

1. **采集** — 链上事件实时订阅（Swap / Transfer / LP 变化）+ 新池创建监听（重点前 5–30 分钟窗口）。
2. **过滤** — GoPlus / RugCheck 安全前置过滤（mint / blacklist / 高税 / 未放弃权限 → 直接丢弃）。
3. **画像** — 地址画像实时 + 定时更新，综合评分。
4. **信号** — 高分地址大额买入（> 历史中位数 2×）且满足流动性/成交量突增 → 生成 `FollowSignal`。
5. **确认** — 5–30 分钟内出现后续买家跟进（跟风确认）才放行；否则信号衰减/取消。
6. **风控** — 仓位上限、日亏损限额、冷却期、熔断标志校验。
7. **执行** — 智能路由（1inch / Jupiter）→ 私有通道（Flashbots Protect / MEV Blocker / Jito）→ 订单状态机。
8. **反馈** — 持仓/盈亏更新 → 分级告警 → Bot / Web / App 实时推送 → 决策日志落库（可回放）。

### 2.3 关键设计原则
- **接口先行**：所有跨模块依赖通过 `internal/model` + 各模块 `types.go` 定义契约，实现可并行开发。
- **配置驱动**：链参数、阈值、评分权重全部 YAML 化，改参数不改代码。
- **单一风控关卡**：任何执行（含 Agent 发起）都必须经过 `RiskEngine`，无旁路。
- **默认保守**：`mode: dry_run` 为默认；`live` 需显式开启 + 二次确认。

---

## 3. 技术栈定稿

| 层 | 选型 | 理由 |
|----|------|------|
| 后端语言 | Go 1.23+（本机 go1.25.4） | 高并发、长连接、低延迟；文书指定 |
| EVM 交互 | `github.com/ethereum/go-ethereum`（ethclient + types） | 事实标准 |
| Solana 交互 | `github.com/gagliardetto/solana-go` | 生态最全 |
| HTTP 框架 | `github.com/gin-gonic/gin` | 文书指定；中间件生态好 |
| 数据库 | `github.com/jackc/pgx/v5` + pgxpool | 性能优于 database/sql |
| 缓存/去重 | `github.com/redis/go-redis/v9` | SETNX+TTL 去重、限流、熔断标志 |
| 配置 | `github.com/spf13/viper` + `godotenv` | YAML + 环境变量覆盖 |
| 日志 | `go.uber.org/zap` | 结构化、低开销 |
| 定时任务 | `github.com/robfig/cron/v3` | 画像全量重算 |
| Telegram | `gopkg.in/telebot.v3` | Inline Button 回调 |
| 鉴权 | `github.com/golang-jwt/jwt/v5` + `golang.org/x/crypto/bcrypt` | JWT + 密码哈希 |
| 指标 | `github.com/prometheus/client_golang` | 延迟/成功率/滑点/资金曲线 |
| 前端 | Next.js 15 (App Router) + TypeScript + Tailwind + Recharts | 文书指定；SSR 利于 SEO/GEO |
| App | Flutter 3.x + Riverpod | 一套代码双端；本机已装 Flutter |
| 基础设施 | Docker Compose（Postgres 16 + Redis 7 + bot + prometheus） | 一键本地环境 |

**本机环境注意**：`proxy.golang.org` 不可达，Go 模块代理必须设为 `https://goproxy.cn,direct`；npm registry 已指向 `https://registry.npmmirror.com`。

---

## 4. 目录结构（仓库定稿）

```
meme-bot/
├── cmd/
│   ├── bot/          # 主程序：装配所有模块并启动
│   ├── migrator/     # 数据库迁移执行器
│   └── worker/       # 后台任务：画像重算、信号衰减扫描
├── internal/
│   ├── config/       # 配置加载（YAML + ENV）
│   ├── model/        # 公共领域模型（跨模块契约）
│   ├── storage/      # Postgres / Redis 连接与仓储
│   ├── chain/        # 多链抽象
│   │   ├── adapter.go       # Adapter 接口 + 类型
│   │   ├── factory.go       # 注册与获取
│   │   ├── base/            # Base(EVM) 实现
│   │   ├── solana/          # Solana 实现
│   │   ├── aggregator/      # 1inch / Jupiter / OpenOcean 路由
│   │   └── privatetx/       # 私有交易通道
│   ├── provider/     # 外部数据源（pump.fun / GMGN / Birdeye / GoPlus ...）
│   ├── address/      # 地址画像与评分（跟单筛选）
│   ├── strategy/     # 信号漏斗 + 执行引擎 + 订单状态机
│   ├── risk/         # 风控：仓位/止损/冷却/熔断
│   ├── alert/        # 分级告警 Manager + Channels
│   ├── agent/        # LLM Agent 循环 + ToolRegistry + Memory
│   ├── user/         # 用户/角色/APIKey
│   ├── subscription/ # 套餐/订阅/支付回调
│   ├── affiliate/    # 直推返佣
│   ├── api/          # Gin 路由 + handlers + middleware
│   └── metrics/      # Prometheus 指标
├── migrations/       # 001_init.sql / 002_saas.sql / 003_agent.sql
├── configs/          # config.yaml / chains.yaml
├── web/              # Next.js Dashboard + Admin
├── app/              # Flutter App
├── deploy/           # docker-compose.yml, Dockerfile, prometheus.yml
├── docs/             # API 文档、运维手册、架构补充
├── TECH-PLAN.md      # 本文档
├── .env.example      # 环境变量模板（不含任何真实密钥）
└── README.md
```

---

## 5. 核心模块技术实现路径

### 5.1 数据层 `internal/provider` + `internal/storage`
- **多源融合优先级**：链上（最高）> 市场数据 > 社交情绪（需机器人过滤）。
- **实时流处理**：WebSocket 订阅 → `chan SwapEvent`（解耦后接 Redis Stream 可水平扩展），避免轮询。
- **安全前置**：`SecurityReport{IsHoneypot, HasMint, HasBlacklist, BuyTax, SellTax, Owner, IsOpenSource}`，命中即丢弃。
- **信号衰减**：`Signal.TTL` + `DecayScore`，超时未确认自动降权/取消。
- **存储**：`address_profiles` / `trade_records`（按月分区）/ `address_blacklist` / `positions` / `alert_history`。
- **数据源适配矩阵**：

  | 数据源 | 用途 | 接入方式 |
  |--------|------|----------|
  | GoPlus / RugCheck | 合约安全 | REST |
  | DexScreener | 多链新池、趋势 | 免费 REST |
  | Birdeye | 价格/持仓/OHLCV | 官方 API |
  | pump.fun | 新币发射/Bonding Curve | frontend-api-v3 + Moralis |
  | GMGN | 聪明钱/趋势 | 官方有限 API + 适配层 |
  | Bitquery | 全生命周期 | GraphQL |

  所有数据源统一到 `DataProvider` 接口，策略层只消费 `NewTokenSignal` / `SmartMoneyBuy` / `SecurityScore`。

### 5.2 逻辑/策略层（`internal/address` + `internal/strategy`）
- **跟单地址四层筛选漏斗**：
  1. 基础标签与黑名单（排除项目方、CEX 热钱包、混币器、套利机器人、Rug 记录）。
  2. 历史表现量化（30/90 天滚动）：WinRate > 55–60%、ProfitFactor > 1.8、MaxDD < 40%、Consistency、HoldingTime、SizeConsistency。
  3. 行为特征：买入后 5–30 分钟有跟进买家、分批出货、活跃度。
  4. 实时动态评分：`Score = 0.30·WinRate + 0.25·PF_norm + 0.20·(1-MaxDD) + 0.15·Consistency + 0.10·Recency`，阈值默认 0.70。
- **自主信号漏斗**：静态安全过滤 → 流动性/成交量突增（相对均值 5–10×）→ 入场确认（回调企稳/突破 + RSI）。
- **跟单触发**：高分地址大额买入（> 历史中位数 2×）+ 安全过滤 + 后续买家确认。
- **冷却/衰减**：信号 TTL、同币同址冷却窗口、连续亏损自动降分。

### 5.3 执行层（`internal/strategy/execution` + `internal/chain`）
- **统一 Adapter 接口**（跨链唯一契约）：
  `ChainID/NativeToken/Balance/BuildSwap/EstimateGas/SendTransaction/WaitConfirmation/TokenPrice/TokenSecurity/HolderConcentration/SubscribeSwaps`。
- **金额表示**：内部统一 `*big.Int` 最小单位 + `decimals`，仅展示层格式化。
- **Gas 抽象**：EVM 用 `maxFeePerGas + maxPriorityFeePerGas`；Solana 用 `computeUnitPrice`；L2 特化。
- **路由**：聚合器优先（1inch / Jupiter / OpenOcean），失败降级原生 Router。
- **订单状态机**：`Pending → Submitted → Confirming → Filled | Failed | Partial → Compensated`，支持重试（指数退避）+ 补偿。
- **分批建仓**：TWAP 风格拆 2–3 笔，降低冲击。
- **私有通道**：`PrivateTxManager` 统一封装 Flashbots Protect / MEV Blocker / Jito Bundle，执行层**强制优先**私有通道，降级需告警。
- **RPC 管理**：每链多节点 + 健康检查 + 错误计数自动切换。

### 5.4 风控层（`internal/risk`）
- 硬止损：百分比 + 流动性骤降**双重触发**。
- 移动止盈（Trailing Stop）。
- 仓位：单币 ≤ 5–10%，同时持仓 ≤ 3–5 个。
- 每日最大亏损 → 强制冷却。
- 人工熔断：Critical 告警支持一键暂停（Redis 全局标志）。
- **所有执行路径的必经关卡**，含 Agent 发起的操作。

### 5.5 告警层（`internal/alert`）
- 三级：`Critical`（巨鲸砸盘/LP 解锁/止损/宕机，Telegram + 电话）· `Warning` · `Info`。
- `AlertManager.Raise`：Redis SETNX+TTL 去重 → 抑制规则 → 按级别路由 → 异步发送。
- `Channel` 接口：Telegram（首选，含 Inline Button 回调 `pause/close/ignore`）、Discord、Webhook。
- 消息必备上下文：代币地址、当前价格/流动性、触发原因、区块浏览器链接、建议动作。

### 5.6 Agent 层（`internal/agent`）
- 核心循环：`Observe → Reason → Plan → Act → Reflect`，`MaxSteps` 上限 + LLM 超时/降级。
- `ToolRegistry`：`ToolSchema`（JSON Schema）+ `ToolFunc`，运行时注册；内置工具：`get_airdrop_tasks`、`check_wallet_eligibility`、`estimate_gas_and_path`、`submit_private_tx`、`request_human_confirm`。
- `LLMClient` 抽象：可切换 OpenAI / Claude / 本地模型。
- `MemoryStore`：短期对话 + 长期结构化记忆（决策日志可回放）。
- `PreActHook`：执行前强制风控（金额上限、频率、黑名单）。
- x402 微支付客户端：HTTP 402 → 稳定币支付 → 带凭证重试。

### 5.7 SaaS 层（`internal/user` / `subscription` / `affiliate` / `api`）
- 角色：`super_admin` / `admin` / `user`；API 用户按 Key + 套餐配额（Redis 计数）。
- 表：`users` / `plans` / `subscriptions` / `commissions`。
- 支付回调：`payment.succeeded` → 激活/续费订阅 → 触发返佣（默认 20%）。
- 路由三组：用户端（JWT）、管理端（Role）、外部 API（API Key + 配额）。
- 外部 API：`/api/external/v1/signals/latest`、`/address/score/:addr`、`/webhook/register`。

### 5.8 可观测性与运维（横切）
- Metrics：延迟、成功率、滑点分布、资金曲线、最大回撤、告警发送成功率。
- 部署：Docker + 健康检查 + 自动重启；生产可 K8s 或 Supervisor + Nginx。
- 密钥：环境变量 / KMS / Vault，**永不落盘明文**（本仓库 `.env.example` 仅占位）。
- 灰度：`dry_run` 模拟盘 → 小额实盘 → 全量。

---

## 6. 阶段目标（Stage 0 → Stage 6）

### Stage 0 — 工程地基（本次已完成）
- **交付**：仓库骨架、`go.mod`、配置加载、zap 日志、PG/Redis 连接、`cmd/bot` 可运行、migrations、docker-compose、README、`.env.example`。
- **验收**：`go build ./...` + `go vet ./...` + `go test ./...` 全绿；`cmd/bot` 在无外部依赖时可启动（降级模式）。

### Stage 1 — 安全监控 MVP（快速可用）
- **交付**：多链 Adapter 接口 + Base/EVM 只读实现、安全前置过滤、流动性/成交量突增监控、硬止损、Telegram 三级告警（含 Inline Button）。
- **验收**：模拟 `SwapEvent` 流 → 生成信号 → Telegram 推送（或日志 mock）→ 按钮回调可暂停；单元测试覆盖安全过滤与评分。

### Stage 2 — 跟单能力
- **交付**：地址画像表 + 评分服务 + 黑名单 + 增量/定时更新 + 跟单触发 + 滑点/延迟上限控制。
- **验收**：回放历史交易样本 → 高分地址筛选结果符合阈值；跟单信号带确认条件与 TTL。

### Stage 3 — 执行闭环（专业化）
- **交付**：智能路由（1inch/Jupiter）、订单状态机、重试补偿、私有交易通道、分批建仓、多链 Solana 支持、风控熔断全链路。
- **验收**：`dry_run` 下完整跑通「信号 → 风控 → 路由报价 → 订单状态机 → 持仓落库 → 告警」；私有通道强制优先。

### Stage 4 — SaaS 化
- **交付**：用户/角色/JWT/APIKey、套餐订阅、支付回调、直推返佣、外部 API + 配额、Web Dashboard（信号流/地址榜/持仓盈亏/告警历史/人工干预面板）。
- **验收**：注册 → 生成推荐码 → 模拟支付回调 → 订阅生效 + 返佣入账；外部 API Key 可鉴权调用。

### Stage 5 — Agent 层
- **交付**：Agent 循环 + ToolRegistry + Memory、空投监控 Agent（先人工确认执行）、x402 客户端、决策日志与人工审核界面。
- **验收**：给定项目名 → Agent 输出结构化执行计划 → 人工确认 → 走私有通道执行（dry_run 模拟）。

### Stage 6 — App 与生产化
- **交付**：Flutter App（Dashboard/信号/持仓/告警/设置 + WebSocket 推送 + 生物识别二次确认）、Prometheus/Grafana、回测引擎、灰度发布、SEO/GEO。
- **验收**：App 双端构建通过；关键指标可在 Grafana 观察；历史事件回放可产出策略报告。

### 里程碑时间（对齐文书 6–8 周）
| 周次 | 阶段 | 交付 |
|------|------|------|
| W1–W2 | Stage 0–1 | 骨架 + Base/Solana Adapter + DB + Telegram 基础告警 |
| W3 | Stage 2 | 地址画像 + 评分 + 基础信号 |
| W4 | Stage 3 | 执行引擎 + 风控 + 自动交易闭环 |
| W5 | Stage 4 | Web Dashboard 核心页面 |
| W6 | Stage 4–5 | SaaS 闭环 + Agent 最小可用 |
| W7–W8 | Stage 6 | 优化/回测/监控/安全加固/文档/App |

---

## 7. 并行开发分工（多线程执行方案）

并行前提：**接口契约先冻结**（`internal/model` 与各模块 `types.go`），各线程只写自己的目录，互不重叠；最后由主线程统一 `go build ./...` 验证。

| 线程 | 负责目录 | 交付物 | 依赖 |
|------|----------|--------|------|
| T0（主） | `cmd/` `internal/config` `internal/model` `internal/storage` `deploy/` | 骨架 + 契约 + 装配 + 容器 | — |
| T1 | `internal/chain/**` | Adapter 接口实现、Base/Solana、聚合器、私有通道 | 契约 |
| T2 | `internal/address/**` | 画像仓储 + 评分 + 筛选漏斗 | 契约 |
| T3 | `internal/alert/**` | Manager + Telegram/Discord/Webhook Channel | 契约 |
| T4 | `internal/strategy/**` `internal/risk/**` | 信号漏斗、执行状态机、风控引擎 | 契约 |
| T5 | `migrations/` `internal/user` `subscription` `affiliate` `api` | SQL + SaaS 闭环 + Gin 路由 | 契约 |
| T6 | `internal/agent/**` | Agent 循环 + ToolRegistry + 空投 Agent | 契约 |
| T7 | `web/` | Next.js Dashboard/Admin 骨架 | REST/WS 契约 |
| T8 | `app/` | Flutter App 骨架 | REST/WS 契约 |

**合并纪律**：每个线程产出后必须 `gofmt` + `go build ./...` 通过；跨模块调用只允许使用冻结契约中的类型；新增契约需在主线程登记。

---

## 8. 安全与合规基线（强制）

1. **密钥零硬编码**：仓库中不得出现任何 API Key、私钥、Token 明文；全部走环境变量/密钥管理，`.env.example` 只放占位符。
2. **默认 dry_run**：未经显式配置 + 二次确认，不发送任何真实链上交易。
3. **私钥隔离**：执行子账户独立；生产建议 KMS/Vault；私钥使用后立即从配置中移除。
4. **权限沙箱**：Agent 单次操作金额上限、频率上限、白名单代币。
5. **人工熔断**：Critical 告警一键暂停全系统（Redis 标志位，执行层每单校验）。
6. **合规声明**：本系统为技术工具与研究用途，不构成投资建议；使用者自行承担全部风险并遵守当地法律与税务要求。

---

## 9. 主要风险与对策

| 风险 | 影响 | 对策 |
|------|------|------|
| MEV 夹击 / 抢跑 | 成交劣化、亏损 | 私有通道强制优先 + Intent-based 路由 + 拆单随机延迟 |
| 数据源 API 变更（pump.fun/GMGN） | 监控中断 | `DataProvider` 适配层 + 多源冗余（Birdeye/Bitquery 兜底） |
| RPC 单点故障 | 漏事件/漏交易 | 多节点 + 健康检查 + 自动切换 |
| LLM 成本与延迟 | Agent 循环变慢/超支 | 超时降级、结果缓存、MaxSteps 上限、按套餐限流 |
| 迷你币极端波动 | 本金损失 | 硬止损 + 流动性骤降双触发 + 单币仓位上限 + 日亏损熔断 |
| 合规与监管 | 法律风险 | 工具定位、免责声明、不代客理财、KYC 由使用者自行合规 |
| 依赖下载受限（本机） | 无法构建 | 固定 `GOPROXY=https://goproxy.cn,direct`；npm 用 npmmirror |

---

## 10. 本地运行手册

```bash
# 0) 环境准备（Windows PowerShell / Git Bash）
export GOPROXY=https://goproxy.cn,direct

# 1) 基础设施
cd deploy && docker compose up -d postgres redis

# 2) 数据库迁移
cd .. && go run ./cmd/migrator -dir ./migrations

# 3) 配置
cp .env.example .env   # 填入 TELEGRAM_TOKEN 等；保持 MODE=dry_run

# 4) 启动后端
go run ./cmd/bot

# 5) 前端 Dashboard
cd web && npm install && npm run dev     # http://localhost:3000

# 6) 验证
go vet ./... && go test ./...
curl -s localhost:8080/healthz
```

---

## 11. 文书核心原则（贯穿实现）

- **少即是多**：宁可错过，不做模糊信号。
- **数据验证价格**：链上行为优先于 K 线形态。
- **自动化执行 + 人工决策**：系统负责扫描与执行，人负责策略审核与极端干预。
- **本金保全优先于收益最大化**。

> **免责声明**：本文档及本仓库代码仅供技术架构参考与教育目的，不构成任何投资、财务或交易建议。迷你币市场风险极高，可能导致本金全部损失。自动化交易存在技术故障、滑点、MEV、合约漏洞等风险。请自行充分回测、小额验证，并遵守当地法律法规与税务要求。所有决策与风险由使用者自行承担。
