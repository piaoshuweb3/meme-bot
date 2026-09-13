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
├── web/              # Next.js Dashboard + Admin（zh-CN / en-US 国际化）
└── app/              # Flutter App（iOS / Android，ARB 双语）
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
# 后端：依赖解析 → 格式门禁 → 静态检查 → 单测 → 编译（一键）
bash scripts/verify.sh          # 等价于 make verify

# 前端
cd web && npm install && npm run typecheck && npm run build

# 移动端
cd app && flutter pub get && flutter analyze
```

**最近一次验证结果**（本机 go1.25.4 / Node 24 / Flutter stable）：

| 检查 | 结果 |
|------|------|
| `go mod tidy` + `gofmt` 门禁 + `go vet` | ✅ 通过 |
| `go test ./...` | ✅ address / alert / risk 三个包全部通过 |
| `go build ./...` + 三个二进制 | ✅ 通过 |
| `npm run build`（Next.js 15.5.25） | ✅ 8 个页面预渲染 |
| `flutter analyze` | ✅ No issues found |

CI（`.github/workflows/ci.yml`）在 GitHub Actions 上重复上述全部检查（含 `go test -race`）。

---

## 国际化（i18n）

前后端均支持 **zh-CN / en-US** 双语，遵循常见国际化标准：

| 端 | 方案 |
|----|------|
| Web (`web/`) | 自研轻量 i18n：BCP 47 locale（`zh-CN`/`en-US`）+ 类型安全字典（`DeepStringRecord` 编译期校验 key 结构）+ `Intl` 格式化（货币/数字/紧凑/百分比/日期时间/相对时间）+ 语言切换器（localStorage 持久化、`navigator.language` 自动判定、同步 `<html lang>`） |
| App (`app/`) | 标准 ARB 翻译源（`lib/l10n/app_*.arb`）+ `flutter_localizations` + 手写 `LocalizationsDelegate`（无需代码生成即可编译）+ `NumberFormat` 按 locale 格式化 + 语言切换菜单 |

约定：
- **禁止在 UI 中硬编码面向用户的字符串**，一律经字典获取（`t("ns.key")` / `AppLocalizations.of(context).xxx`）；
- 新增语言只需补充对应字典/ARB，不改动组件逻辑；
- 无障碍同步保障：skip-link、`aria-current`、表格 `caption`/`scope`、`role="alert"`/`aria-live`、`focus-visible` 焦点环、盈亏用「颜色 + 符号」双编码。

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

### 验证状态

**已在本机完成真实验证**：

| 检查 | 命令 | 结果 |
|------|------|------|
| 依赖解析 / 格式 / 静态检查 / 单测 / 编译 | `bash scripts/verify.sh` | ✅ 全绿 |
| 单元测试 | `go test ./...` | ✅ address / alert / risk 通过 |
| 前端生产构建 | `npm run build` | ✅ Next.js 15.5.25，8 页预渲染 |
| 移动端静态分析 | `flutter analyze` | ✅ No issues found |

CI（`.github/workflows/ci.yml`）会在每次推送/PR 自动重复这些检查（后端额外跑 `-race`）。

仍有待完善的实现细节（逐条列在 `docs/DELIVERY.md` 第 4 节）：Base V3 与 Solana 的金额级事件解析、Solana 交易签名、WebSocket 推送、回测框架、Grafana 面板、支付渠道回调路由与 x402 客户端。

---

## 免责声明

本项目仅供**技术架构参考与教育目的**，不构成任何投资、财务或交易建议。迷你币市场风险极高，可能导致本金全部损失；自动化交易存在技术故障、滑点、MEV、合约漏洞等风险。请自行充分回测、小额验证，并遵守当地法律法规与税务要求。所有决策与风险由使用者自行承担。
