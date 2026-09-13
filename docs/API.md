# meme-bot HTTP API 文档

Base URL：`http://localhost:8080`

鉴权方式：
- **用户端**：`Authorization: Bearer <JWT>`
- **外部 API**：`X-API-Key: <API_KEY>`（按套餐扣减每日配额）

---

## 1. 健康检查与指标

| 方法 | 路径 | 鉴权 | 说明 |
|------|------|------|------|
| GET | `/healthz` | 无 | 运行模式、已就绪链、跳过的链、告警通道健康状态 |
| GET | `/metrics` | 无 | Prometheus 文本格式指标 |

```bash
curl -s localhost:8080/healthz | jq
```

---

## 2. 认证

### POST /api/v1/auth/register

```json
{ "email": "user@example.com", "password": "至少8位", "referral_code": "可选" }
```

响应 `201`：用户信息 + `api_key`（**仅此一次返回**）。

### POST /api/v1/auth/login

```json
{ "email": "user@example.com", "password": "..." }
```

响应 `200`：`{ "token": "...", "user": {...} }`

---

## 3. 用户端（需要 JWT）

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/v1/me` | 当前用户信息（不回显 API Key） |
| POST | `/api/v1/me/api-key/rotate` | 轮换 API Key（旧 Key 立即失效） |
| GET | `/api/v1/signals?limit=50` | 实时信号流 |
| GET | `/api/v1/positions?chain=base` | 当前持仓 |
| GET | `/api/v1/subscription` | 订阅状态 |
| POST | `/api/v1/subscription/checkout` | 创建支付订单 `{"plan_id":2,"currency":"USD"}` |
| POST | `/api/v1/affiliate/bind` | 绑定推荐码 `{"code":"ab12cd34"}` |
| GET | `/api/v1/affiliate/stats` | 返佣统计 |
| POST | `/api/v1/system/pause` | 人工熔断 `{"reason":"异常波动","duration_minutes":60}` |
| POST | `/api/v1/system/resume` | 解除熔断 |

---

## 4. 管理端（需要 `admin` / `super_admin` 角色）

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/v1/admin/users?limit=50&offset=0` | 用户列表 |
| POST | `/api/v1/admin/users/:id/status` | 启用/禁用 `{"status":"banned"}` |
| POST | `/api/v1/admin/users/:id/role` | 调整角色（**仅 super_admin**）`{"role":"admin"}` |
| GET | `/api/v1/admin/addresses/top?chain=base&limit=50` | 高分地址榜 |

---

## 5. 外部 API（`X-API-Key` + 套餐配额）

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/external/v1/signals/latest` | 最新信号（按配额计费） |
| GET | `/api/external/v1/address/score/:address?chain=base` | 地址评分 |

响应头 `X-RateLimit-Remaining` 表示当日剩余配额。

配额规则：按 `plans.max_api_calls` 每日计数，见 `api_usage` 表。

---

## 6. 支付回调（由支付渠道/链上监听服务调用）

`POST /api/v1/payments/webhook`

```json
{ "type": "payment.succeeded", "data": { "order_id": "ord_1_2_1700000000000000000" } }
```

处理逻辑（`subscription.HandlePaymentSucceeded`）：
1. 以 `order_id` 做幂等迁移（`pending → succeeded`），重复回调直接返回 `deduped: true`；
2. 激活/续费订阅（`+30 天`）；
3. 触发直推返佣（默认 20%，写入 `commissions`）。

> 说明：该路由在 `internal/api` 中预留，接入具体支付渠道时在 `SetupRouter` 中注册（需校验签名）。

---

## 7. 错误约定

| 状态码 | 含义 |
|--------|------|
| 400 | 参数/业务校验失败（`{"error": "..."}`） |
| 401 | 缺少或无效的令牌 / API Key |
| 403 | 权限不足 / 账号被禁用 |
| 404 | 资源不存在 |
| 429 | 超出套餐 API 配额 |
| 503 | 依赖服务不可用（数据库/风控/订阅服务未就绪） |
