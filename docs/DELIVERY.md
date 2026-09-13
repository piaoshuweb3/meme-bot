# 交付说明（Delivery Notes）

本文件记录本轮交付的**具体产物、对照关系、验证方式与已知限制**。

---

## 1. 交付物总览

目标目录：`C:\Users\Administrator\Desktop\meme-bot`

| 分类 | 内容 |
|------|------|
| 方案文档 | `TECH-PLAN.md`（技术框架 / 实现路径 / Stage 0–6 阶段目标 / 并行开发分工）、`docs/API.md`、`docs/RUNBOOK.md` |
| Go 后端 | `cmd/`（bot / migrator / worker）、`internal/`（16 个业务包）、`migrations/`（3 个 SQL） |
| 配置 | `configs/config.yaml`、`configs/chains.yaml`、`.env.example`（全部为占位符，无任何真实密钥） |
| 部署 | `deploy/docker-compose.yml`、`deploy/Dockerfile`、`deploy/prometheus.yml` |
| 前端 | `web/`（Next.js 15 App Router + TS + Tailwind，6 个页面，zh-CN / en-US 国际化） |
| 移动端 | `app/`（Flutter，总览/信号/持仓三页 + API 客户端 + ARB 双语） |
| 工程化 | `Makefile`、`scripts/verify.sh`、`.gitignore`、`.github/workflows/ci.yml`、`README.md` |

---

## 2. 文书模块 → 代码对照

| 文书要求 | 落地位置 |
|----------|----------|
| 数据层：多源融合 + 优先级 + 实时流 | `internal/provider/`（market / security / newtoken / smartmoney）、`internal/chain/rpcpool.go`（多节点故障转移） |
| 安全前置过滤（GoPlus/RugCheck 风格） | `internal/provider/security.go` + `internal/model/market.go:SecurityReport` |
| 逻辑层：自主信号漏斗 | `internal/strategy/signal.go`（安全 → 流动性 → 大额买入 → 冷却/TTL） |
| 跟单地址四层筛选 + 评分公式 | `internal/address/scorer.go`（`Screen` 四层漏斗 + 加权评分 + `RebuildProfile`） |
| 多链抽象（Adapter + Factory） | `internal/model/market.go:ChainAdapter`、`internal/chain/factory.go`、`internal/chain/base/`、`internal/chain/solana/` |
| 执行层：智能路由 | `internal/chain/aggregator/`（1inch / Jupiter） |
| 执行层：订单状态机 | `internal/model/trade.go:OrderStatus`、`internal/strategy/execution.go` |
| 执行层：私有通道（MEV 防护） | `internal/chain/privatetx/manager.go`（Flashbots Protect / MEV Blocker / Jito） |
| 风控层：止损/移动止盈/仓位/日亏损/熔断 | `internal/risk/engine.go`（`CheckEntry` / `CheckExit` / `Pause`） |
| 告警系统：分级 + 去重 + 抑制 + 按钮 | `internal/alert/manager.go` + `internal/alert/telegram.go` |
| SaaS：用户/角色/JWT/APIKey | `internal/user/`（`service.go` + 自实现 HS256 `jwt.go`） |
| SaaS：套餐/订阅/支付回调/返佣 | `internal/subscription/service.go`（幂等回调）、`internal/affiliate/service.go`（20% 直推） |
| 外部 API + 配额 | `internal/api/router.go` + `middleware.go`（API Key + 按天配额） |
| Agent 层：循环 + 工具 + 记忆 + 风控钩子 | `internal/agent/`（`core.go` / `tool.go` / `llm.go` / `airdrop.go`） |
| 可观测性 | `internal/metrics/metrics.go`（Prometheus 文本格式，自实现）、结构化 zap 日志 |
| 部署与运维 | `deploy/`、`Makefile`、`docs/RUNBOOK.md` |
| 展示端 | `web/`（Dashboard/Admin，双语）、`app/`（Flutter，双语） |

---

## 3. 如何验证

```bash
cd /c/Users/Administrator/Desktop/meme-bot

# 一键验证（依赖解析 → 格式门禁 → 静态检查 → 单测 → 编译）
bash scripts/verify.sh

# 或分步
go env -w GOPROXY=https://goproxy.cn,direct GOSUMDB=off   # 本机必须
go mod tidy
go vet ./...
go test ./... -count=1
go build ./...

# 前端
cd web && npm install && npm run typecheck && npm run build

# 移动端
cd app && flutter pub get && flutter analyze
```

单元测试覆盖（无需数据库/网络）：
- `internal/address/scorer_test.go`：筛选漏斗、权重归一化、评分单调性、统计量计算
- `internal/risk/engine_test.go`：熔断拒绝、仓位削减、硬止损、流动性骤降、移动止盈、冷却、TTL
- `internal/alert/manager_test.go`：去重、Critical 不被抑制、HTML 转义、内联键盘、回调解析

---

## 4. 已知限制（诚实清单）

### 4.1 编译与构建验证（已完成）

**本机真实验证已通过**（go1.25.4 / Node 24 / Flutter stable）：

| 检查 | 命令 | 结果 |
|------|------|------|
| 依赖解析 / 格式门禁 / 静态检查 / 单测 / 编译 | `bash scripts/verify.sh` | ✅ 全绿（首次执行时修复了 3 个编译错误 + 1 个测试断言） |
| 单元测试 | `go test ./...` | ✅ internal/address、internal/alert、internal/risk 全部通过 |
| 前端生产构建 | `npm run build` | ✅ Next.js 15.5.25，8 个静态页面预渲染 |
| 移动端静态分析 | `flutter analyze` | ✅ No issues found |

首次验证修复的问题（均已提交）：
1. `internal/chain/base` 引用未定义的 `aggregator.Client` 接口、`zap.String` 传入 `common.Address` 类型不匹配；
2. `cmd/bot` 的 executor 未接入导致 "declared and not used"——顺带把「信号 → 确认 → 风控 → 执行」链路真正接通，并新增按链上真实 decimals 换算金额的 `toBaseUnits`；
3. `splitAction` 未处理命令的前导 `/`，导致 `TestSplitAction` 失败；
4. 前端构建暴露的问题：Next.js 15 页面文件禁止额外导出、字典 `as const` 类型过窄、多 lockfile 导致构建根目录误判；同时把 next 从存在漏洞的 15.1.6 升级到 15.5.25。

CI（`.github/workflows/ci.yml`）在每次推送/PR 重复上述检查（后端额外跑 `-race`）。

### 4.2 功能层面的未完成项

| 项 | 状态 | 说明 |
|----|------|------|
| Base V3 池子 Swap 事件金额解析 | 部分 | V2 已完整解析（方向/金额/sender）；V3 目前只产出 txHash，需补 sqrtPriceX96 数学 |
| Solana Swap 金额级解析 | 部分 | 当前基于 `getSignaturesForAddress` 轮询，需 Geyser/Jupiter 解析补全 |
| Solana 交易签名 | 未实现 | Jupiter 返回的未签名交易需外部钱包签名（Ed25519 + 交易反序列化替换签名） |
| 地址画像真实成本基准 | 简化 | `computeStats` 用已实现盈亏近似，未接入真实建仓成本配对 |
| 成交活跃度倍数 | 近似 | 用 `volume24h / liquidity` 近似，真实实现需要时序库滚动均值 |
| WebSocket 实时推送 | 未实现 | 当前前端为轮询（10–20s）；Stage 6 接 WS |
| Flutter 推送/生物识别/安全存储 | 未实现 | 已在 `app/README.md` 标注 |
| 回测引擎 / Grafana 面板 | 未实现 | Stage 6 |
| x402 微支付客户端 | 未实现 | 数据库表（`x402_payments`）与迁移已就绪 |
| 支付渠道对接 | ✅ 已打通（待接真实渠道） | `POST /api/v1/payments/webhook` 已注册：HMAC-SHA256 验签 + 时间戳防重放 + 幂等激活 + 返佣；实测首次回调 `commissioned=true`、重放 `deduped=true` |

### 4.3 环境残留

无项目内残留（`bin/`、`web/node_modules`、`web/.next`、`app/.dart_tool` 均由 `.gitignore` 排除，不会进入仓库）。
原始需求文书 `MEME.docx` 已从索引移除并加入 `.gitignore`（如需提交：删除 `.gitignore` 中的 `MEME.docx`/`*.docx` 两行后 `git add -f MEME.docx`）。

---

## 5. 安全基线落实情况

| 要求 | 落实 |
|------|------|
| 密钥零硬编码 | ✅ 全仓库无任何真实密钥；`.env.example` 仅占位；`.gitignore` 排除 `.env*`、`*.key`、`wallet/` |
| 默认 dry_run | ✅ `mode: dry_run`；`live` 需同时把 `risk.dry_run_default` 置 false，否则配置校验直接拒绝启动 |
| 执行必经风控 | ✅ `Executor.Execute` 与 Agent 的 `submit_private_tx` 工具都先调用 `RiskPort` |
| 人工熔断 | ✅ Redis 全局标志（多实例共享），Telegram / Web / App 三处入口 |
| 私钥不落盘、用完即弃 | ✅ 私钥只从 `MEMEBOT_*_PRIVATE_KEY` 读取；`.env.example` 中该项留空并注明生产用 KMS/Vault |
| 私有通道优先 | ✅ `SendTransaction` 强制先走 `privatetx`，失败才降级并记警告 |

---

## 6. 建议的下一步

1. **本地联调**：`make infra && make migrate && make run`，配置 `MEMEBOT_WATCHLIST=base:<池子地址>` 观察 dry_run 下的完整链路（安全过滤 → 流动性 → 大额买入 → 跟风确认 → 风控 → 模拟下单 → 告警）。
2. **补 Stage 6 剩余项**：Base V3 金额解析、Solana 交易签名、WebSocket 实时推送、回测框架、Grafana 面板。
3. **接入真实收款**：注册支付回调路由（`/api/v1/payments/webhook`，需验签），对接链上 USDC 或 Stripe。
4. **上线前**：按 `docs/RUNBOOK.md` 第 6 节清单逐项确认，再切 `live` 并用最小仓位灰度。
