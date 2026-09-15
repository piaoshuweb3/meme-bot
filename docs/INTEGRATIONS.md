# 数据源集成与系统完善方案（INTEGRATIONS）

> 目标：为 Meme 币与 DeFi 参与者提供**高效研究与交易辅助**能力。
> 本文回答两个问题：① 如何把生态里的数据源接进本系统；② 对照 `nhovongoc0-max/meme-radar` 等成熟实现，我们的短板与落地顺序。

## 1. 现状盘点（已具备的数据能力）

| 层 | 已实现 | 代码位置 |
|---|---|---|
| 行情（价格/流动性/24h 量） | DexScreener（`/tokens` + `/search`），可注入自建网关 | `internal/provider/market.go` |
| 合约安全（蜜罐/增发/黑名单/税率） | GoPlus（EVM 数字链 ID、Solana 字符串），失败降级为 `Risky=true` 兜底 | `internal/provider/security.go` |
| 持仓集中度 | GoPlus `top_10/20_holder_rate` | `internal/provider/security.go` |
| 新币发现 | pump.fun 兜底 + DexScreener 搜索 | `internal/provider/newtoken.go` |
| 聪明钱/标签/趋势 | `SmartMoney` **接口占位**（未配置代理端点时明确返回错误，不做假数据） | `internal/provider/smartmoney.go` |
| 链上事件（金额级） | Base V2/V3 + Solana 余额差值法 | `internal/chain/{base,solana}` |
| 执行 | 1inch / Jupiter 路由 + Flashbots/Jito 私有通道 + EVM/Solana 签名 | `internal/chain/{aggregator,privatetx,signer}` |
| 风控 | 仓位/止损/移动止盈/流动性骤降/日亏损/人工熔断 | `internal/risk` |
| 回测 | 事件回放 + 时钟注入（策略与实盘共用同一引擎） | `internal/backtest` |

**结论**：交易与风控闭环完整；**研究辅助与"验证过滤器有效性"的能力是短板**（见 §3）。

## 2. 生态数据源接入映射

按用途分层，标注接入方式与落地优先级（P0 立即 / P1 近期 / P2 视需要）。
「合规」列提示是否需要官方授权或存在使用条款风险。

### 2.1 发现与挖掘

| 来源 | 用途 | 接入方式 | 优先级 | 合规 |
|---|---|---|---|---|
| **gmgn**（`@gmgnai`） | 新池发现、钱包标签、1 分钟活跃榜 | 官方 API Key + Ed25519 认证（参考 meme-radar 的 `gmgn-cli` 只读用法） | **P0** | 需 Key，仅只读 |
| **Axiom**（`@AxiomExchange`） | 新币/热点发现 | 无公开 API；做**跳转入口**而非抓取 | P1 | 抓取有风险 |
| **Birdeye**（`@birdeye_so`） | 多链行情/新池/OHLCV（**历史序列**，补我们的图表缺口） | 官方 API Key（配置已有 `providers.birdeye_api_key` 位） | **P0** | 官方授权 |
| pump.fun | Solana 新币 | 现用公开前端 API（易变，需兜底） | P1 | 非官方 |

### 2.2 市场数据与指标

| 来源 | 用途 | 接入方式 | 优先级 |
|---|---|---|---|
| **CoinGecko**（`@coingecko`） | 主流币价、市值、分类 | 官方 API（免费额度足够） | P1 |
| CoinMarketCap（`@CoinMarketCap`） | 同上（备源，避免单点） | 官方 API Key | P2 |
| **DefiLlama**（`@DefiLlama`） | 协议 TVL、链上手续费、稳定币流向 | 免费公开 API（无需 Key） | **P0** |
| Coinglass（`@coinglass_com`） | 衍生品持仓/资金费率/爆仓（大盘情绪） | 官方 API Key | P2 |
| **DexScreener**（`@dexscreener`） | K 线/交易对（已接入） | 已用 | ✅ |
| TradingView（`@tradingview`） | 技术分析图表 | 用**跳转链接**（`tradingview.com/chart/?symbol=`），不抓取 | P1 |
| Dune（`@Dune`） | 自定义分析仪表板 | 官方 API（Query ID） | P2 |
| **TokenTerminal**（`@tokenterminal`） | Meme 基本面（收入/活跃度） | 官方 API | P2 |

### 2.3 链上情报与钱包追踪（**风控与研究价值最高**）

| 来源 | 用途 | 接入方式 | 优先级 |
|---|---|---|---|
| **Arkham**（`@arkham`） | 实体标签、资金链路 | 官方 API（审批制） | P1 |
| **Nansen**（`@nansen_ai`） | 聪明钱标签、资金流 | 官方 API Key | P1 |
| **Moni**（`@getmoni_io`） | 社交+链上情报、KOL 追踪 | 官方 API | P1 |
| **Cielo**（`@CieloFinance`） | 钱包监控（多地址实时动作） | 官方 API | **P0** |
| **Bubblemaps**（`@bubblemaps`） | 筹码关联/集群（「地毯式检测」） | 官方 API | **P0** |
| **Moby**（`@mobyagent`） | 聪明钱**警报** | 官方 API/Webhook | P1 |

> 我们的 `smartmoney.go` 已把接口留好，缺的只是**具体实现**（当前无端点即明确报错，绝不返回假数据）。

### 2.4 社交与跟单

| 来源 | 用途 | 接入方式 | 优先级 |
|---|---|---|---|
| fomo | 社交化跟单 | 无公开 API → 跳转入口 | P2 |
| X/Twitter | 叙事与 KOL 信号 | 官方 API（付费）或人工复核入口 | P1 |
| Telegram | 告警与人工确认 | **已有**（Telegram 三级告警 + 按钮回调） | ✅ |

### 2.5 安全与权限

| 来源 | 用途 | 接入方式 | 优先级 |
|---|---|---|---|
| **RevokeCash**（`@RevokeCash`） | 钱包授权管理（清理危险 approve） | 跳转 `revoke.cash/?address=` + 本地授权扫描 | **P0**（运营侧低成本、信任度高） |
| GoPlus | 合约安全（已接入） | 已用 | ✅ |
| 自有黑名单/白名单 | 长期资产（`address_blacklist` 表已存在） | 已用 | ✅ |

> 关于 TG 交易自动化（Maestro）：本项目**已有自身执行链路**（风控 → 私有通道 → 签名），不建议再引入第三方自动交易（多一层托管私钥的风险）；仅保留其作为**人工对照工具**。

## 3. 对照 meme-radar：我们的真实短板与改进

`nhovongoc0-max/meme-radar`（Node/ESM，61 文件，12 个测试）定位是**只读筛选器**：GMGN 发现 + GoPlus/DexScreener 复核 + 人工复核入口 + 影子表现跟踪，**明确不下单**。它有四处设计值得我们直接吸收：

### 3.1 【最关键】"影子表现跟踪"——验证过滤器本身是否有效

它做的事（`src/outcomes.mjs`）：

- 对**被拒绝**的候选（`HARD_REJECT`）做 **SHA256(address) % 5 == 0** 的**稳定抽样**（与后续涨跌无关 → 无选择性偏差）；
- 记录 7 个时间视野 `m5/m15/m30/h1/h2/h6/h24` 的价格，计算 `return = price/baseline - 1`；
- 用 `outcomeCoverage()` 输出每个视野的 `eligible / completed / missing` 与**中位数、正收益率**；
- **样本 < 50 之前不调参、不宣称有效**；采样失败有指数退避 + 限流检测。

**为什么重要**：回测只能回答"策略在历史数据上如何"，回答不了"**我今天的拒绝，是否是错的拒绝**"。没有对照样本，过滤器只会越调越自信而无法证伪。

**我们的落地**（P0）：

```
migrations/005_outcomes.sql
  signal_outcomes(chain, token, address, decision, baseline_at, baseline_price,
                  strategy_version, samples JSONB, sampling)
  -- decision ∈ {HARD_REJECT, X_REVIEW, EXECUTED}
worker: 每 N 分钟采样到期样本（价格来自 Market)，指数退避重试
API: GET /api/v1/outcomes/coverage → 按决策分层的覆盖率 + 中位数 + 正收益率
前端: 研究页展示"拒绝 vs 放行"的对照曲线（样本不足时明确标注"样本不足，不构成结论"）
```

### 3.2 "不确定就猜" vs "不确定返回 null"

我们的 `floatValue/flagValue` 在解析失败时返回 `0/false`，而**0% 税率看起来像"安全"** —— 这会把"上游数据缺失"误读成"低风险"。它在 `scoring.mjs` 里刻意让解析函数**返回 null**，注释原话：*"large bare values are deliberately not guessed to be percentages because doing so would manufacture precision from ambiguous upstream data."*

**我们的落地**（P1）：`SecurityReport` 增补"字段是否可用"的信息（如 `TaxAvailable bool`），风险判定把 `unknown` 与 `0` 区分开；前端在字段缺失时显示 `—` 而不是 `0.00%`。

### 3.3 安全边界（对我们更严格，因为我们**会下单**）

它：HTTP 只监听回环；API Key/私钥只写 `0700/0600` 受限文件，不进命令行/日志/HTTP 响应/浏览器存储；浏览器只拿**字段白名单**过滤后的状态。

我们：默认 `dry_run`、密钥只用环境变量、日志经 `config.MaskURL` 打码（已做）。**待补**：API 响应对内部字段做白名单输出（避免把上游原始响应透传给前端）、私钥文件权限显式收紧、`live` 模式新增"二次确认 + 熔断可回滚"检查清单。

### 3.4 产品分层（对应我们的订阅体系）

它的 `EDITION-BOUNDARY.md` 明确：**"已知的严重风险提示不应被专业版付费墙隐藏"**。这条应写进我们的 `docs/DELIVERY.md` 作为**产品红线**：免费版也要能看到蜜罐/可增发/黑名单等致命风险，付费卖的是**效率与研究深度**（多钱包关联、持仓演化、多源冲突处理、团队协作、托管运行），不是"安全信息"。

## 4. 落地路线

| 阶段 | 内容 | 价值 |
|---|---|---|
| **P0-1** | 影子跟踪闭环（迁移 + worker + API + 前端对照视图） | 唯一能**证伪**过滤器的手段；直接回答"如何更好地阻击" |
| **P0-2** | 接 **Birdeye**（行情/OHLCV）+ **DefiLlama**（免费）+ **Cielo**（钱包监控）+ **Bubblemaps**（筹码集群） | 补齐"研究辅助"四大件：历史序列、宏观背景、地址动作、筹码关联 |
| **P0-3** | RevokeCash 授权检查入口（运营/信任） | 低成本高感知 |
| **P1-1** | `SecurityReport` 区分 `unknown` 与 `0` | 消除"缺数据被当成安全"的系统性误判 |
| **P1-2** | 接 GMGN 只读 API（发现 + 标签）、Nansen/Arkham 标签 | 提升新池发现命中率与标签质量 |
| **P1-3** | API 字段白名单 + 私钥文件权限 + `live` 切换检查清单 | 安全边界对齐 |
| **P2** | Dune/TokenTerminal/Coinglass/TradingView 跳转入口、X 叙事人工复核流 | 深度研究体验 |

## 5. 定位差异（避免重复造轮子）

| 维度 | meme-radar | 本项目 |
|---|---|---|
| 定位 | 只读筛选雷达（只筛不下单） | 监控 + 跟单 + 风控 + 执行 + SaaS |
| 数据源 | GMGN 为主 | 多源（可插拔 provider） |
| 交付 | 便携包（含 Node 运行时） | Docker + Web 面板 + Flutter 移动端 |
| 短板 | 无执行/无风控 | **无影子跟踪、多源情报偏少** |

→ 结论：**不重复它的只读筛选**，而是把它的**影子跟踪与"不猜精度"纪律**吸收进来，再用我们的执行与风控形成闭环。

## 6. 合规提醒

- 所有第三方数据源按官方条款接入，**不抓取需要授权的接口**；无官方 API 的一律做**跳转入口**；
- X / 社交数据仅作**人工复核**，不因社交热度自动下单（本项目所有执行必经风控与人工可熔断）；
- 第三方风险提示一律**不设付费墙**。
