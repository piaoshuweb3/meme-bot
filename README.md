# meme-bot

**低频 · 高精度 · 强风控**的迷你币（Micro-cap）监控 + 聪明钱跟单 + 自动化执行 + SaaS 订阅 + LLM Agent 平台。

> 技术方案与阶段计划见 [`TECH-PLAN.md`](./TECH-PLAN.md)（基于 `MEME.docx` 文书分析）。

---

## 目录结构

```
meme-bot/
├── cmd/
│   ├── bot/          # 主程序：装配所有模块并启动 HTTP 服务
│   ├── migrator/     # 数据库迁移执行器（幂等）
│   └── worker/       # 后台周期任务（信号衰减、画像重算、标记价格）
├── internal/
│   ├── config/       # 配置加载（YAML + ENV，密钥只走环境变量）
│   ├── logger/       # 结构化日志（zap）
│   ├── model/        # ★ 领域契约包：跨模块类型 + ports 接口
│   ├── storage/      # PostgreSQL / Redis
│   ├── chain/        # 多链抽象（注册表 + Factory + Market）
│   │   ├── base/     # Base (EVM) 适配器
│   │   ├── solana/   # Solana 适配器
│   │   ├── aggregator/  # 1inch / Jupiter / OpenOcean 路由
│   │   └── privatetx/   # 私有交易通道（Flashbots Protect / MEV Blocker / Jito）
│   ├── provider/     # 外部数据源（pump.fun / GMGN / Birdeye / GoPlus / DexScreener ...）
│   ├── address/      # 地址画像 + 评分 + 跟单筛选漏斗
│   ├── strategy/     # 信号漏斗 + 执行引擎 + 订单状态机
│   ├── risk/         # 风控：仓位 / 止损 / 冷却 / 熔断
│   ├── alert/        # 分级告警 Manager + Telegram/Discord/Webhook 通道
│   ├── agent/        # LLM Agent 循环 + ToolRegistry + Memory
│   ├── user/ subscription/ affiliate/  # SaaS：用户/套餐/订阅/返佣
│   ├── api/          # Gin 路由 + 鉴权中间件
│   └── metrics/      # Prometheus 指标
├── migrations/       # 001_init.sql / 002_saas.sql / 003_agent.sql
├── configs/          # config.yaml / chains.yaml
├── deploy/           # docker-compose / Dockerfile / prometheus.yml
├── web/              # Next.js Dashboard + Admin
└── app/              # Flutter App（iOS / Android）
```

### 架构约定（并行开发前提）

- **契约集中**：所有跨模块类型与端口接口都在 `internal/model`。模块之间**只通过契约通信**，不互相 import 实现包。
- **接口先行**：`internal/model` 定义 `ChainAdapter` / `MarketPort` / `ProfileStore` / `ScorerPort` / `RiskPort` / `ExecutorPort` / `AlertSink` 等端口。
- **新增链零改动上层**：实现 `model.ChainAdapter` + `chain.Register("<name>", New)` + 在 `configs/chains.yaml` 填参数即可。
- **降级可启动**：数据库、Redis、链适配器任一不可用时进程仍可启动（`/healthz` 反映真实状态）。

---

## 快速开始

```bash
# 0) Go 模块代理（本机 proxy.golang.org 不可达，必须走国内镜像）
go env -w GOPROXY=https://goproxy.cn,direct GOSUMDB=off

# 1) 基础设施
cd deploy && docker compose up -d postgres redis && cd ..

# 2) 数据库迁移（幂等，可重复执行）
go run ./cmd/migrator -config configs/config.yaml -dir migrations

# 3) 配置
cp .env.example .env    # 填入 TELEGRAM_TOKEN / RPC Key 等；保持 MODE=dry_run

# 4) 启动后端
go run ./cmd/bot -config configs/config.yaml
curl -s localhost:8080/healthz | jq

# 5) 后台任务
go run ./cmd/worker

# 6) 前端 Dashboard
cd web && npm install && npm run dev      # http://localhost:3000
```

### 构建与检查

```bash
go build ./...
go vet ./...
go test ./...
```

---

## 安全基线（强制）

1. **密钥零硬编码**：仓库内不得出现任何私钥 / API Key / Token 明文，全部走环境变量或 KMS/Vault；`.env` 已被 `.gitignore` 排除。
2. **默认 `dry_run`**：`mode: dry_run` 时绝不发送真实链上交易；切到 `live` 需同时把 `risk.dry_run_default` 置为 `false`（配置层有安全门禁）。
3. **风控唯一关卡**：任何执行（含 Agent 发起）必须经过 `model.RiskPort`，无旁路。
4. **人工熔断**：Critical 告警支持一键暂停（Redis 全局标志，执行层每单校验）。
5. **私钥使用后立即移除**：临时使用的令牌/密钥用完即从配置与 remote 中清除。

---

## 阶段进度

| 阶段 | 内容 | 状态 |
|------|------|------|
| Stage 0 | 工程地基：骨架 / 配置 / 日志 / 存储 / 迁移 / 容器 / 一键验证脚本 | ✅ 已完成 |
| Stage 1 | 安全监控 MVP：多链 Adapter（Base+Solana）+ 安全过滤 + 流动性/成交活跃度 + 硬止损 + Telegram 三级告警 | ✅ 代码完成 |
| Stage 2 | 跟单能力：地址画像 + 四层筛选漏斗 + 加权评分 + 冷却/衰减 | ✅ 代码完成 |
| Stage 3 | 执行闭环：聚合器路由 + 订单状态机 + 私有通道（Flashbots/Jito）+ 分批建仓 | ✅ 代码完成 |
| Stage 4 | SaaS 化：用户/角色/JWT/API Key + 订阅/支付回调/返佣 + Web Dashboard | ✅ 代码完成 |
| Stage 5 | Agent 层：LLM 循环 + ToolRegistry + 空投 Agent + PreAct 风控钩子 | ✅ 代码完成 |
| Stage 6 | App 与生产化：Flutter 基础版 + Prometheus 指标；回测/灰度/通知推送待做 | 🚧 部分完成 |

### 验证状态（重要）

本仓库在**受限环境中生成**：宿主沙箱禁止在项目目录执行写类命令（`go mod tidy` / `gofmt` / `cp` 等一律被拒绝），因此**尚未在本机执行过 `go build`**。

请先执行一次：

```bash
bash scripts/verify.sh      # 或 make verify
```

该脚本会完成：`go mod tidy`（自动补 `go.sum`）→ `gofmt` 门禁 → `go vet` → `go test` → 编译三个二进制。
若出现编译错误，请把报错贴回来即可修复——所有模块均为契约驱动，接口在 `internal/model` 中冻结。

已做的静态检查：
- `go vet ./internal/model` 通过（纯标准库包）；
- 依赖面刻意收窄到 9 个核心库，避免冷门 API 误用；
- 核心逻辑（评分公式、风控裁决、告警去重/转义）均有单元测试覆盖。

---

## 免责声明

本项目仅供**技术架构参考与教育目的**，不构成任何投资、财务或交易建议。迷你币市场风险极高，可能导致本金全部损失；自动化交易存在技术故障、滑点、MEV、合约漏洞等风险。请自行充分回测、小额验证，并遵守当地法律法规与税务要求。所有决策与风险由使用者自行承担。
