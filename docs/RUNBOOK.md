# meme-bot 运行手册（Runbook）

面向运维与开发者的**可执行**操作手册：启动、验证、故障排查、上线切换。

---

## 1. 环境准备

| 依赖 | 版本 | 说明 |
|------|------|------|
| Go | ≥ 1.23 | 本机 1.25.4 已验证兼容 |
| Docker | 任意近期版本 | 本地 Postgres/Redis |
| Node.js | ≥ 20 | Web Dashboard |
| Flutter | ≥ 3.22 | 移动端（可选） |

**关键环境差异**：本机 `proxy.golang.org` 不可达（SSL 失败），必须使用国内代理：

```bash
go env -w GOPROXY=https://goproxy.cn,direct GOSUMDB=off
```

npm 已配置 `https://registry.npmmirror.com`。

---

## 2. 首次启动

```bash
# 1) 依赖解析
go mod tidy

# 2) 本地基础设施
make infra                 # = cd deploy && docker compose up -d postgres redis

# 3) 数据库迁移（幂等，可重复执行）
make migrate

# 4) 配置
cp .env.example .env       # 填 TELEGRAM_TOKEN / RPC Key；保持 MEMEBOT_MODE=dry_run

# 5) 启动后端
make run                   # http://localhost:8080/healthz

# 6) 前端
make web                   # http://localhost:3000
```

验收：`curl -s localhost:8080/healthz | jq` 显示 `status=ok`、`dry_run=true`、`chains` 含 `base`。

---

## 3. 一键验证

```bash
bash scripts/verify.sh          # tidy + gofmt + vet + test + build
make verify                     # 同上（Makefile 版本）
```

包含：
- `gofmt -l` 格式门禁
- `go vet ./...`
- `go test ./... -count=1`
- 编译 `bin/bot`、`bin/migrator`、`bin/worker`

---

## 4. 常用运维操作

### 4.1 人工熔断 / 恢复

```bash
# 熔断 60 分钟（所有执行路径被拒绝，含 Agent）
curl -X POST localhost:8080/api/v1/system/pause \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"reason":"巨鲸砸盘","duration_minutes":60}'

# 恢复
curl -X POST localhost:8080/api/v1/system/resume -H "Authorization: Bearer $TOKEN"
```

熔断状态存放在 Redis（`memebot:risk:paused`），多实例共享；Redis 不可用时回退内存（**仅单实例**生效）。

### 4.2 配置监控目标

```bash
# chain:token 逗号分隔；Base 传池子地址，Solana 传 mint/账户
export MEMEBOT_WATCHLIST="base:0xPoolAddress,solana:MintAddress"
```

未配置时进程正常运行，仅不产生信号（日志会提示）。

### 4.3 调整阈值

改 `configs/config.yaml` 的 `score` / `signal` / `risk` 段后重启即可；环境变量可覆盖关键项（见 `.env.example`）。

---

## 5. 故障排查

| 症状 | 排查顺序 |
|------|----------|
| `/healthz` 显示 `database=false` | `docker compose ps` → 检查 `MEMEBOT_DATABASE_DSN` → `make migrate` |
| 无信号产生 | ①`MEMEBOT_WATCHLIST` 是否配置；②流动性是否 ≥ 25k；③安全过滤是否命中（日志 `signal filtered`）；④冷却窗口 |
| Telegram 无消息 | ①`MEMEBOT_TELEGRAM_ENABLED=true`；②token/chat_id 是否正确；③`/healthz` 的 `alerts` 字段；④网络可达性 `api.telegram.org` |
| 交易未发送（live） | ①`signer` 是否为 true（需配置私钥环境变量）；②是否处于熔断；③私有通道是否配置；④日志中 `order failed` 的原因 |
| 编译失败 `no required module provides package` | 执行 `go mod tidy`（GOPROXY 必须为 goproxy.cn） |
| 编译失败 `missing go.sum entry` | 同上；或在离线环境手动准备 `go.sum` |

---

## 6. dry_run → live 切换检查清单

> ⚠️ 不可逆操作，务必逐项确认。

- [ ] `MEMEBOT_MODE=live`
- [ ] `configs/config.yaml` 中 `risk.dry_run_default: false`（否则配置校验直接拒绝启动）
- [ ] 签名器已配置（`MEMEBOT_CHAINS_BASE_PRIVATE_KEY` 或替换为 KMS 实现），且**已从 shell 历史/配置中清理明文**
- [ ] 私有交易通道已配置（否则日志会告警 MEV 风险）
- [ ] `risk` 段阈值复核：`max_position_pct` ≤ 0.1、`max_open_positions` ≤ 5、`stop_loss_pct` 明确
- [ ] Telegram 通道可用（Critical 告警必须可达）
- [ ] 先用最小资金灰度：`risk.max_position_pct: 0.01`，观察 24 小时
- [ ] 确认无 Agent 自动执行权限（`agent.require_human_confirm: true`）

---

## 7. 数据与备份

| 数据 | 位置 | 备份建议 |
|------|------|----------|
| 画像/交易/持仓/订单/告警 | PostgreSQL | `pg_dump` 每日全量 + WAL 归档 |
| 熔断/冷却/去重状态 | Redis | 可丢失（TTL 秒级恢复） |
| 日志 | stdout（容器） | 采集到 Loki/ELK |
| 指标 | `/metrics` | Prometheus 抓取（`deploy/prometheus.yml`） |

---

## 8. 可观测性

- 指标：`memebot_signals_total`、`memebot_signals_filtered_total`、`memebot_orders_total`、`memebot_orders_failed_total`、`memebot_positions_open`、`memebot_equity_usd`、`memebot_paused`
- 日志：结构化 zap（`live` 为 JSON，`dry_run` 为开发可读格式）
- 告警：分级（Info / Warning / Critical）+ Redis 去重 + 止损后抑制
