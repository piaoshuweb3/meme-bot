# 功能说明（FEATURES）

> 本文件说明系统**已实现的功能、当前状态与待补项**，并记录对照 `nhovongoc0-max/meme-radar` 后的完善方向。
> 最后更新：本轮（响应体限长 + 交易台 UI + positions 一键卖出 + Flutter Web 交付）。

## 1. 系统定位

面向 Meme 币与 DeFi 参与者的**低频高精度**监控 / 跟单 / 风控 / 执行一体化系统，同时对外提供 SaaS 订阅服务。

与纯筛选工具（如 `meme-radar`：只读扫描、明确不下单）不同，本项目**具备执行能力**，因此对风控与审计的要求更高——所有执行默认 `dry_run`，切换 `live` 需要同时关闭配置层安全门禁并配置签名器。

## 2. 功能清单与状态

图例：✅ 完整可用 ｜ ⚠️ 可用但有边界 ｜ ❌ 待建

### 2.1 数据层

| 功能 | 说明 | 状态 |
|---|---|---|
| Base V2 事件解析 | `Swap` 事件 → 金额/方向/USD（topic0 与签名串双重校验） | ✅ |
| Base V3 事件解析 | `int256` 有符号金额 + 5 字数据段，方向由两金额符号共同判定 | ✅ |
| Solana 金额级解析 | **余额差值法**（`pre/postTokenBalances`），天然聚合 Jupiter 多跳路由 | ✅ |
| 行情（价格/流动性/24h 量） | DexScreener，多池时按链过滤并取流动性最高者，**价格与流动性必须取自同一池** | ✅ |
| 合约安全 | GoPlus（EVM 数字链 ID / Solana 字符串），失败降级为 `Risky=true` 兜底 | ✅ |
| 持仓集中度 | GoPlus `top_10/20_holder_rate` | ✅ |
| 新币发现 | pump.fun + DexScreener 搜索（兜底） | ⚠️ 依赖非官方端点 |
| 聪明钱/标签 | `SmartMoney` 接口就绪，**未接数据源时明确报错，绝不返回假数据** | ❌ 待接 Nansen/Arkham/Cielo |
| 成交额突增 | 滚动窗口（相对该代币自身近期均值，跨代币可比） | ✅ |
| 响应体限长 | 上游响应 > 1 MiB 显式报错（防大响应拖垮内存） | ✅ 本轮新增 |

### 2.2 逻辑层

| 功能 | 说明 | 状态 |
|---|---|---|
| 信号漏斗 | 安全过滤 → 流动性门槛 → 大额买入判定 → 冷却 → 跟风确认（≥2 独立买家） | ✅ |
| 地址画像 | 加权平均成本法重放真实已实现盈亏（胜率/盈亏比/回撤/一致性） | ✅ |
| 评分与准入 | 加权评分 + 阈值准入 | ✅ |
| 信号 TTL 衰减 | 过半衰减速 + 过期移出活跃集合 | ✅ |
| 过滤器有效性验证 | **对被拒绝候选做稳定抽样并跟踪其后续表现** | ❌ P0 待建（见 §4） |

### 2.3 执行层

| 功能 | 说明 | 状态 |
|---|---|---|
| 路由 | 1inch（EVM）/ Jupiter（Solana） | ✅ |
| 私有通道 | Flashbots（EVM）/ Jito（Solana）优先，避免公开 mempool 被夹 | ✅ |
| 签名 | EVM EIP-1559 + Solana Ed25519；**签名前强制校验 fee payer 为本私钥地址**（防盲签） | ✅ |
| 订单状态机 | 构建 → 估算 → 签名 → 广播 → 确认 → 落库 → 告警 | ✅ |
| dry_run | 默认开启；模拟成交**绝不构建或广播**交易（有测试断言 `buildCalls==0 && sendCalls==0`） | ✅ |

### 2.4 风控层

| 功能 | 说明 | 状态 |
|---|---|---|
| 仓位与并发上限 | 单笔占比 + 最大持仓数 | ✅ |
| 止损 / 移动止盈 | 硬止损 + 移动止盈 | ✅ |
| 流动性骤降 | 触发退出 | ✅ |
| 日亏损熔断 | 当日累计亏损上限 | ✅ |
| 人工熔断 | Redis 全局标志，所有执行路径（含 Agent）被拒 | ✅ |
| 执行前复核 | 每次下单前再查熔断状态，防绕过 | ✅ |

### 2.5 告警 / 实时 / 界面

| 功能 | 说明 | 状态 |
|---|---|---|
| 告警分级 | Info / Warning / Critical + Redis 去重 + 止损后抑制 | ✅ |
| Telegram | 三级告警 + 按钮回调 + Webhook 模式 | ✅ |
| WebSocket 实时推送 | 全局单连接 Hub + 统计（clients/sent/dropped） | ✅ |
| Web 交易台 | 顶部状态栏（连接/模式/链/持仓/盈亏/UTC 时钟）、发现流（**卡片↔表格**切换、风险灯、筛选、排序）、快捷交易面板、**一键卖出 25/50/100%**、移动端底部导航 | ✅ 本轮完成 |
| 风险可视化 | 红黄绿灯 + 悬浮明细（蜜罐/可增发/黑名单=红，未开源/未弃权/高税=黄） | ✅ |
| 移动端 | Flutter：Dashboard / 信号 / 持仓；安全存储 + 生物识别（不支持则降级为显式确认） | ✅ |
| 移动端交付 | **Flutter Web**（`build/web` 31 M，手机浏览器可访问）；Android APK 见 §5 | ⚠️ |

### 2.6 SaaS / 商业化

| 功能 | 说明 | 状态 |
|---|---|---|
| 账号 | 注册 / 登录 / JWT（HS256 自实现，强制校验 `exp` 与算法字段，拒绝 `alg=none`） | ✅ |
| API Key | 鉴权 + 套餐配额扣减 + `X-RateLimit-Remaining` | ✅ |
| 套餐 | Free / Pro / Elite（信号数、API 调用、功能开关） | ✅ |
| 支付（回调） | HMAC-SHA256 验签 + 时间戳防重放 + 幂等激活 + 20% 直推返佣 | ✅ |
| 支付（链上） | **USDC 收款监听**：`eth_getLogs` 按 `to` 过滤 + 确认数等待 + 金额匹配 + 双保险幂等 | ✅ |
| x402 微支付 | EIP-3009 客户端 + 单次支付上限硬阀门 | ✅ |
| 产品红线 | **严重风险提示不设付费墙**（免费版同样可见蜜罐/可增发/黑名单） | ✅ 已写入规范 |

### 2.7 工程化

| 功能 | 说明 | 状态 |
|---|---|---|
| 回测引擎 | 事件回放 + 时钟注入（策略与实盘共用同一引擎，不复制逻辑） | ✅ |
| Grafana | 8 面板看板（信号速率/突增分布/风控触发/订单状态机/评分分布/WS 连接/RPC 延迟/日盈亏） | ✅ |
| Prometheus | 指标导出 | ✅ |
| RPC 排障 | `bin/rpccheck`：逐端点真实调用 + 自动识别 EVM/Solana + URL 打码 | ✅ |
| CI | GitHub Actions 工作流（以 `deploy/github-actions-ci.yml` 入库） | ⚠️ |
| 测试 | 15 个测试文件；覆盖率：market 89.6% / backtest 78.4% / risk 77.2% / config 77.0% / payment 72.4% / strategy 69.1% / storage 68.5% / provider 67.7% / signer 52.0% / address 49.8% / alert 33.3% / solana 22.6% / base 22.0% / api 20.8% | ⚠️ 持续补 |
| 配置注入 | 链级配置支持环境变量覆盖（`MEMEBOT_CHAINS_<SEG>_*`），密钥不落盘、日志经 `MaskURL` 打码 | ✅ |

## 3. 本轮完成的改动

1. **响应体限长（安全）**：`httpGetJSON` 从 `io.ReadAll` 改为 `LimitReader(1 MiB + 1)`，超限显式报错；新增 2 个测试（超大响应被拒 / 正常响应不被误伤）。
2. **Web 交易台**：新增 `top-status-bar`、`risk-light`、`quick-trade`、`bottom-nav` 四个组件；`signals` 页升级为发现流（卡片/表格、筛选、排序、风险灯、点击展开交易面板）；`positions` 页加**一键卖出 25/50/100%** + 汇总卡片；补 `Signal.security` 类型（后端已返回但前端缺失，导致风险信息无法渲染）。
3. **文案**：中英双语补齐 `trading.*`、`mobileNav.*`、`positions.realized`。
4. **移动端交付**：补齐 Flutter `web` 平台工程并成功构建（`build/web` 31 M），静态服务跑在 8088。

## 4. 对照 meme-radar 的待补项（按优先级）

| 优先级 | 项 | 为什么重要 | 落地方案 |
|---|---|---|---|
| **P0-1** | **影子表现跟踪** | 回测只能回答"策略在历史上如何"，回答不了"我今天的拒绝是否是错拒"。没有对照样本，过滤器无法证伪、只会越调越自信 | `signal_outcomes` 表 + worker 定时采样（7 个时间视野）+ `/api/v1/outcomes/coverage` + 前端对照视图 |
| **P0-2** | 研究数据源 | 补齐"研究辅助"四大件：历史序列（Birdeye OHLCV）、宏观背景（DefiLlama 免费）、地址动作（Cielo）、筹码关联（Bubblemaps） | 按 `docs/INTEGRATIONS.md` §2 接入 |
| **P0-3** | **实证可卖性** | 静态检测无法覆盖"能买不能卖"，用真实卖出记录验证是最强手段 | 对候选代币做小额卖出模拟 / 读取链上卖出记录统计失败率 |
| **P1-1** | **区分 `unknown` 与 `0`** | 解析失败返回 `0` 会被读成"0% 税率 = 安全"，把数据缺失误判为低风险 | `SecurityReport` 增补字段可用性标记，风险判定区分二者，前端缺失显示 `—` |
| **P1-2** | **预算感知的审计队列** | 外部 API 有配额，全量深审会超限 | 参考 `selectAuditQueue`：按得分与新鲜度排序 + 退避重试 + 限流即停 |
| **P1-3** | 多源交叉校验 | 单一来源可能有误 | 比较 DexScreener 与 GoPlus 的市值/流动性差异，超阈值标记"数据冲突" |
| **P1-4** | 校准纪律 | 样本不足时调参会过拟合噪声 | 采用其 `REQUIRED_CALIBRATION_WINDOWS=['m30','h2','h24']`：样本达标前禁止调参 |
| **P2** | 规则表驱动 | 硬编码 if 链难维护、难审计 | `EVM_SECURITY_RULES` / `SOL_SECURITY_RULES` 表驱动 |
| **P2** | API 字段白名单 | 避免把上游原始响应透传给前端 | 响应只输出白名单字段 |

## 5. 已知边界与阻塞

| 项 | 状态 |
|---|---|
| Android APK | ❌ 阻塞于本机环境：`frontend_server_aot.dart.snapshot is not an AOT snapshot`（Flutter SDK 缓存损坏）。已定位并尝试删除 `dart-sdk` 强制重下（进行中）。**替代交付**：Flutter Web 版可手机浏览器使用 |
| Defender 排除项 | ❌ 需要管理员权限（`Add-MpPreference` 返回 `0x800106ba`） |
| C 盘空间 | ⚠️ 曾 100% 满（已清理约 10 G）；Android SDK 可精简约 6.7 G（`ndk` 5.7 G 为最大项，建议 APK 成功后再动） |

## 6. 快速验证

```bash
# 后端（默认 dry_run）
./bin/bot -config configs/config.yaml          # :8090  /healthz

# Web 交易台
cd web && NEXT_PUBLIC_API_BASE=http://127.0.0.1:8090 npm run dev -- -H 0.0.0.0
#   本机 http://127.0.0.1:3000   局域网 http://<本机IP>:3000

# Flutter（移动端）
cd app && PUB_HOSTED_URL=https://pub.flutter-io.cn FLUTTER_STORAGE_BASE_URL=https://storage.flutter-io.cn \
  flutter run                                   # 真机/模拟器
flutter build web --release --dart-define=API_BASE=http://<本机IP>:8090   # Web 版

# 全量验证
bash scripts/verify.sh                          # tidy/gofmt/vet/test/build

# RPC 排障
./bin/rpccheck -config configs/config.yaml       # 或 -url <端点>
```

## 7. 安全约定（不可妥协）

- 密钥只通过环境变量注入；仓库内**绝无**真实密钥，`.env` 被忽略；
- 日志中的 URL 一律经 `config.MaskURL` 打码（保留 scheme+host）；
- 默认 `dry_run`；切 `live` 需同时关闭 `risk.dry_run_default`（配置层 fail-fast）；
- 所有执行必经风控；人工熔断可随时全局停止；
- 私有通道优先，减少被夹风险；
- 第三方数据源按官方条款接入，无官方 API 的一律做**跳转入口**而非抓取。
