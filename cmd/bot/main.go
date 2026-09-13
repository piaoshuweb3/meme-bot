// Command bot 是 meme-bot 主程序：装配全部模块并启动 HTTP 服务与监控循环。
//
// 设计原则：
//   - 依赖不可用时降级启动（/healthz 反映真实状态），便于分阶段开发与本地调试；
//   - 默认 dry_run：不发送任何真实交易；切到 live 需显式配置并关闭 risk.dry_run_default；
//   - 所有执行（含 Agent 发起）必须经过风控层，无旁路。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"meme-bot/internal/address"
	"meme-bot/internal/affiliate"
	"meme-bot/internal/alert"
	"meme-bot/internal/api"
	"meme-bot/internal/chain"
	"meme-bot/internal/chain/aggregator"
	basechain "meme-bot/internal/chain/base"
	"meme-bot/internal/chain/privatetx"
	"meme-bot/internal/chain/signer"
	solanachain "meme-bot/internal/chain/solana"
	"meme-bot/internal/config"
	"meme-bot/internal/logger"
	"meme-bot/internal/metrics"
	"meme-bot/internal/model"
	"meme-bot/internal/payment"
	"meme-bot/internal/provider"
	"meme-bot/internal/risk"
	"meme-bot/internal/storage"
	"meme-bot/internal/strategy"
	"meme-bot/internal/subscription"
	"meme-bot/internal/user"
	"meme-bot/internal/ws"
)

var flagConfig = flag.String("config", "configs/config.yaml", "配置文件路径")

func main() {
	flag.Parse()

	cfg, err := config.Load(*flagConfig)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}
	log, err := logger.New(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "init logger: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = log.Sync() }()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	dryRun := cfg.IsDryRun()
	log.Info("meme-bot starting",
		zap.String("mode", string(cfg.Mode)),
		zap.Bool("dry_run", dryRun),
		zap.Strings("enabled_chains", cfg.EnabledList),
	)
	if dryRun {
		log.Warn("当前为 dry_run 模式：不会发送任何真实链上交易")
	}

	// ---- 指标 ----
	reg := metrics.New()

	// ---- 存储（失败降级）----
	pool := newPool(ctx, cfg, log)
	if pool != nil {
		defer pool.Close()
	}
	rdb := newRedis(ctx, cfg, log)
	if rdb != nil {
		defer func() { _ = rdb.Close() }()
	}

	stateStore := newStateStore(ctx, cfg, rdb, log)

	// ---- 告警 ----
	alertMgr, tg := newAlerting(cfg, stateStore, reg, log)

	// ---- 数据源 ----
	market := provider.NewMarket()
	security := provider.NewSecurity(cfg.Providers.GoPlusAPIKey, market)
	newTokens := provider.NewNewTokenFeed(market)
	smartMoney := provider.NewSmartMoney("", "")

	// ---- 多链 ----
	privateTx := newPrivateTx(cfg, log)
	basechain.Configure(basechain.Deps{
		Market:     market,
		Security:   security,
		Aggregator: aggregator.NewOneInch(cfg.AggregatorBaseURL("base"), config.ResolveSecret(cfg.AggregatorKeyEnv("base"))),
		PrivateTx:  privateTx,
	})
	solanachain.Configure(solanachain.Deps{
		Market:              market,
		Security:            security,
		Jupiter:             aggregator.NewJupiter(cfg.AggregatorBaseURL("solana"), config.ResolveSecret(cfg.AggregatorKeyEnv("solana"))),
		PrivateTx:           privateTx,
		PriorityFeeLamports: 2000,
	})

	factory, err := chain.NewFactory(cfg, log)
	if err != nil {
		log.Fatal("chain factory", zap.Error(err))
	}
	marketPort := chain.NewMarket(factory)
	log.Info("chain adapters ready",
		zap.Strings("ready", factory.List()),
		zap.Strings("skipped", factory.Skipped()),
		zap.Strings("registered", chain.Registered()),
	)

	// ---- WebSocket 实时推送 ----
	hub := ws.NewHub(log)
	wsHandler := hub.Handler(ws.HandlerConfig{
		AllowedOrigins: cfg.Server.CORSAllowedOrigins,
		RequireAuth:    false, // 内网面板：靠 Origin 白名单；如需令牌鉴权见 HandlerConfig.RequireAuth
	})
	// 告警扇出：既走 Telegram/日志，也实时推给前端
	var alertSink model.AlertSink = &wsAlertSink{inner: alertMgr, hub: hub}

	// ---- 地址画像与评分 ----
	var profiles model.ProfileStore
	if pool != nil {
		profiles = address.NewPGStore(pool)
	} else {
		profiles = address.NewMemoryStore()
	}
	scorer := address.NewScorer(cfg.Score, cfg.Signal.LargeBuyMultiple, profiles, log)

	// ---- 持仓与执行 ----
	var positions model.PositionStore
	if pool != nil {
		positions = strategy.NewPGPositions(pool)
	} else {
		positions = strategy.NewMemoryPositions()
	}
	swapsigner := signer.SignerOrNil("MEMEBOT_CHAINS_BASE_PRIVATE_KEY", rpcByChain(cfg), log)
	solSigner := signer.SolanaSignerOrNil("MEMEBOT_CHAINS_SOLANA_PRIVATE_KEY", log)
	if swapsigner == nil && !dryRun {
		log.Fatal("live 模式必须配置签名器（MEMEBOT_CHAINS_BASE_PRIVATE_KEY 或改用 KMS 签名实现）")
	}

	riskEngine := risk.New(cfg.Risk, stateStore, positions, reg, log)
	executor := strategy.NewExecutor(
		factory, swapsigner, solSigner, riskEngine, positions, alertSink, reg, log,
		cfg.Risk, dryRun, nativeTokens(cfg),
	)

	// ---- 信号漏斗 ----
	filter := model.SignalFilter{
		MaxBuyTax:           0.10,
		MaxSellTax:          0.10,
		MinLiquidityUSD:     25000,
		MinVolumeSpike:      cfg.Signal.VolumeSpikeMultiple,
		RequireOpenSource:   true,
		RejectHoneypot:      true,
		RejectMintable:      true,
		RejectWithBlacklist: true,
	}
	engine := strategy.NewSignalEngine(cfg.Signal, filter, marketPort, scorer, alertSink, reg, log)

	// ---- SaaS 模块（需要数据库）----
	var (
		userSvc *user.Service
		subSvc  *subscription.Service
		affSvc  *affiliate.Service
	)
	if pool != nil {
		userSvc = user.NewService(pool, cfg.Auth.JWTSecret, cfg.Auth.JWTTTLHours)
		affSvc = affiliate.NewService(pool, 0.20)
		subSvc = subscription.NewService(pool, affSvc, reg)
	}

	// 支付回调处理器（HMAC 验签 + 防重放）；未配置密钥时回调路由会拒绝所有请求
	paymentWebhook := subscription.NewWebhookHandler(
		subSvc,
		cfg.Payment.WebhookSecret,
		time.Duration(cfg.Payment.ToleranceSeconds)*time.Second,
		reg,
	)
	if !paymentWebhook.Configured() {
		log.Warn("支付回调未启用：未配置 MEMEBOT_PAYMENT_WEBHOOK_SECRET（/api/v1/payments/webhook 返回 503）")
	}

	// ---- HTTP ----
	router := api.SetupRouter(api.Deps{
		Users:       userSvc,
		Subs:        subSvc,
		Affiliates:  affSvc,
		Payments:    paymentWebhook,
		Signals:     engine.Active,
		Positions:   positions,
		Addresses:   profiles,
		Risk:        riskEngine,
		AlertHealth: func() map[string]string { return alertMgr.HealthCheck(context.Background()) },
		HealthExtra: func() map[string]any {
			return map[string]any{
				"dry_run":        dryRun,
				"database":       pool != nil,
				"redis":          rdb != nil,
				"signer":         swapsigner != nil,
				"smart_money":    smartMoney.Available(),
				"alert_channels": alertMgr.Channels(),
				"ws":             hub.Stats(),
			}
		},
		MetricsHandler: reg.Handler(),
		MetricsPath:    cfg.Metrics.Path,
		JWTSecret:      cfg.Auth.JWTSecret,
		Mode:           string(cfg.Mode),
		GinMode:        cfg.Server.GinMode,
		CORSOrigins:    cfg.Server.CORSAllowedOrigins,
		WS:             wsHandler,
		Chains:         factory.List(),
	})

	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Server.Port),
		Handler:      router,
		ReadTimeout:  time.Duration(cfg.Server.ReadTimeoutSeconds) * time.Second,
		WriteTimeout: time.Duration(cfg.Server.WriteTimeoutSeconds) * time.Second,
	}
	go func() {
		log.Info("http server listening", zap.Int("port", cfg.Server.Port))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http server error", zap.Error(err))
		}
	}()

	// ---- Telegram 交互（按钮回调：暂停 / 平仓 / 忽略 / 状态）----
	if tg != nil {
		go tg.StartPolling(ctx, func(cbCtx context.Context, userID int64, action, payload string) string {
			return handleTelegramAction(cbCtx, riskEngine, engine, action, payload, log)
		})
	}

	// ---- 监控循环 ----
	watch := parseWatchlist(config.ResolveSecret("MEMEBOT_WATCHLIST"))
	if len(watch) == 0 {
		log.Warn("未配置 MEMEBOT_WATCHLIST（格式 chain:token,chain:token），监控循环未启动")
	}
	// 链上 USDC 收款：付款 → 订阅激活 → 直推返佣（幂等，走同一 HandlePaymentSucceeded）
	startUSDCPaymentWatcher(ctx, cfg, pool, subSvc, log)

	unsubs := startWatchers(ctx, factory, engine, executor, riskEngine, dryRun, watch, reg, log)
	defer func() {
		for _, unsub := range unsubs {
			unsub()
		}
	}()

	// ---- 事件泵：信号/持仓变化实时推送 ----
	go wsEventPump(ctx, hub, engine, positions, reg, log)

	// ---- 信号衰减扫描 ----
	go decayLoop(ctx, engine, alertSink, log)

	_ = newTokens // 新币发现：由 worker 或后续调度使用（Stage 2+）

	<-ctx.Done()
	log.Info("shutdown signal received")

	shutdownTimeout := time.Duration(cfg.Server.ShutdownTimeoutSeconds) * time.Second
	if shutdownTimeout <= 0 {
		shutdownTimeout = 10 * time.Second
	}
	shCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shCtx); err != nil {
		log.Error("graceful shutdown failed", zap.Error(err))
	}
	log.Info("meme-bot stopped")
}

// ---------------------------------------------------------------------------
// 装配辅助
// ---------------------------------------------------------------------------

func newPool(ctx context.Context, cfg *config.Config, log *zap.Logger) *pgxpool.Pool {
	pool, err := storage.NewPostgres(ctx, cfg)
	if err != nil {
		log.Warn("postgres unavailable, degraded mode（SaaS 模块将被禁用）", zap.Error(err))
		return nil
	}
	log.Info("postgres connected")
	return pool
}

func newRedis(ctx context.Context, cfg *config.Config, log *zap.Logger) *redis.Client {
	client, err := storage.NewRedis(ctx, cfg)
	if err != nil {
		log.Warn("redis unavailable, degraded mode（使用内存状态存储）", zap.Error(err))
		return nil
	}
	log.Info("redis connected")
	return client
}

func newStateStore(ctx context.Context, cfg *config.Config, rdb *redis.Client, log *zap.Logger) risk.StateStore {
	if rdb != nil {
		return storage.NewRedisStateStore(rdb)
	}
	_ = ctx
	_ = cfg
	log.Warn("using in-memory state store（熔断状态不会跨实例共享）")
	return risk.NewMemoryStore()
}

func newAlerting(cfg *config.Config, store risk.StateStore, reg *metrics.Registry, log *zap.Logger) (*alert.Manager, *alert.TelegramChannel) {
	channels := []model.Channel{alert.NewLogChannel(log)}
	var tg *alert.TelegramChannel

	if cfg.Telegram.Enabled && strings.TrimSpace(cfg.Telegram.Token) != "" && cfg.Telegram.ChatID != 0 {
		ch, err := alert.NewTelegramChannel(cfg.Telegram.Token, cfg.Telegram.ChatID, log)
		if err != nil {
			log.Error("telegram channel disabled", zap.Error(err))
		} else {
			pingCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			if err := ch.Ping(pingCtx); err != nil {
				log.Error("telegram 通道不可用（token/网络问题）", zap.Error(err))
			} else {
				tg = ch
				channels = append(channels, ch)
				log.Info("telegram channel ready")
			}
			cancel()
		}
	} else {
		log.Info("telegram 未启用（仅日志通道）")
	}

	return alert.New(cfg.Alert, channels, store, reg, log), tg
}

func newPrivateTx(cfg *config.Config, log *zap.Logger) *privatetx.Manager {
	mgr := privatetx.NewManager()

	baseRPC := cfg.PrivateTx.BaseRPC
	if baseRPC == "" {
		if ch, ok := cfg.Chain("base"); ok {
			baseRPC = ch.PrivateTxRPC
		}
	}
	if baseRPC != "" {
		mgr.Register("base", privatetx.NewRPCProtectSender("flashbots-protect", baseRPC, "base"))
		log.Info("private tx channel registered", zap.String("chain", "base"))
	}

	solRPC := cfg.PrivateTx.SolanaRPC
	if solRPC != "" {
		mgr.Register("solana", privatetx.NewJitoSender(solRPC))
		log.Info("private tx channel registered", zap.String("chain", "solana"))
	}
	if !mgr.Available("base") && !mgr.Available("solana") {
		log.Warn("未配置私有交易通道：live 模式下交易将降级到公开 mempool（存在 MEV 风险）")
	}
	return mgr
}

func rpcByChain(cfg *config.Config) map[string][]string {
	out := make(map[string][]string, len(cfg.Chains))
	for id, ch := range cfg.Chains {
		out[id] = ch.RPCURLs
	}
	return out
}

func nativeTokens(cfg *config.Config) map[string]string {
	out := make(map[string]string, len(cfg.Chains))
	for id, ch := range cfg.Chains {
		out[id] = ch.NativeToken.Address
	}
	return out
}

type watchTarget struct {
	chain string
	token string
}

func parseWatchlist(raw string) []watchTarget {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var out []watchTarget
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		parts := strings.SplitN(item, ":", 2)
		if len(parts) != 2 {
			continue
		}
		chainID := strings.ToLower(strings.TrimSpace(parts[0]))
		tok := strings.TrimSpace(parts[1])
		if chainID == "" || tok == "" {
			continue
		}
		out = append(out, watchTarget{chain: chainID, token: tok})
	}
	return out
}

// startWatchers 订阅 watchlist 的 Swap 事件，并驱动「信号 → 确认 → 风控 → 执行」闭环。
//
// 安全约束：
//   - dry_run 时执行引擎只产出模拟订单与告警，不发送任何真实交易；
//   - live 时要求签名器与（建议）私有通道就绪，且每单都要通过风控引擎；
//   - 确认与执行在独立 goroutine 中完成，避免阻塞事件订阅循环。
func startWatchers(
	ctx context.Context,
	factory *chain.Factory,
	engine *strategy.SignalEngine,
	executor *strategy.Executor,
	riskEngine *risk.Engine,
	dryRun bool,
	targets []watchTarget,
	reg *metrics.Registry,
	log *zap.Logger,
) []model.Unsubscribe {
	var unsubs []model.Unsubscribe
	for _, t := range targets {
		adapter, err := factory.Get(t.chain)
		if err != nil {
			log.Warn("watch target skipped", zap.String("chain", t.chain), zap.String("token", t.token), zap.Error(err))
			continue
		}
		target := t
		unsub, err := adapter.SubscribeSwaps(ctx, target.token, func(ev model.SwapEvent) {
			sig, err := engine.Evaluate(ev)
			if err != nil {
				log.Warn("evaluate swap failed", zap.Error(err))
				return
			}
			if sig == nil {
				return
			}
			log.Info("signal candidate",
				zap.String("chain", sig.Chain), zap.String("token", sig.Token),
				zap.Float64("usd", sig.AmountUSD), zap.String("status", string(sig.Status)))
			if reg != nil {
				reg.Inc("memebot_signals_observed_total", 1)
			}

			// 确认 → 风控 → 执行（异步，避免阻塞订阅循环）
			go func(s *model.Signal) {
				confirmed, cErr := engine.Confirm(s)
				if cErr != nil {
					log.Warn("confirm signal failed", zap.Error(cErr))
					return
				}
				if confirmed.Status != model.SignalConfirmed {
					log.Debug("signal not confirmed yet",
						zap.String("token", confirmed.Token), zap.String("status", string(confirmed.Status)))
					return
				}
				tradeSignal(ctx, adapter, confirmed, executor, riskEngine, dryRun, log)
			}(sig)
		})
		if err != nil {
			log.Warn("subscribe swaps failed", zap.String("chain", target.chain), zap.String("token", target.token), zap.Error(err))
			continue
		}
		log.Info("watching", zap.String("chain", target.chain), zap.String("token", target.token))
		unsubs = append(unsubs, unsub)
	}
	return unsubs
}

// tradeSignal 把已确认信号送入风控与执行引擎（唯一入场路径）。
func tradeSignal(
	parent context.Context,
	adapter model.ChainAdapter,
	sig *model.Signal,
	executor *strategy.Executor,
	riskEngine *risk.Engine,
	dryRun bool,
	log *zap.Logger,
) {
	ctx, cancel := context.WithTimeout(parent, 90*time.Second)
	defer cancel()

	if executor == nil || riskEngine == nil {
		return
	}

	// 信号生成到确认之间流动性与价格可能已变化，入场前重新校验
	liquidityUSD := sig.LiquidityUSD
	priceUSD := sig.PriceUSD
	if liq, err := adapter.Liquidity(ctx, sig.Token); err == nil && liq != nil && liq.LiquidityUSD > 0 {
		liquidityUSD = liq.LiquidityUSD
	}
	if p, err := adapter.TokenPrice(ctx, sig.Token); err == nil && p != nil && p.USD > 0 {
		priceUSD = p.USD
	}

	decision, err := riskEngine.CheckEntry(model.EntryRequest{
		Chain:        sig.Chain,
		Token:        sig.Token,
		SignalID:     sig.ID,
		Source:       sig.Source,
		Address:      sig.TriggerAddress,
		AmountUSD:    sig.AmountUSD,
		PriceUSD:     priceUSD,
		LiquidityUSD: liquidityUSD,
		SlippageBps:  100,
		RequestedAt:  time.Now().UTC(),
	})
	if err != nil {
		log.Warn("risk check failed", zap.String("signal", sig.ID), zap.Error(err))
		return
	}
	if decision.Verdict == model.VerdictReject {
		log.Info("entry rejected by risk engine",
			zap.String("token", sig.Token), zap.String("reason", decision.Reason))
		return
	}

	amountIn, err := toBaseUnits(decision.AllowedUSD, priceUSD, adapter, ctx, sig.Token)
	if err != nil || amountIn.Sign() <= 0 {
		log.Warn("cannot compute entry amount",
			zap.String("token", sig.Token), zap.Float64("allowed_usd", decision.AllowedUSD), zap.Error(err))
		return
	}

	native := adapter.NativeToken().Address
	intent := &model.TradeIntent{
		Chain:       sig.Chain,
		TokenIn:     native,
		TokenOut:    sig.Token,
		AmountIn:    amountIn,
		Side:        model.SideBuy,
		SlippageBps: decision.SlippageBps,
		SignalID:    sig.ID,
		OrderID:     uuid.NewString(),
		DryRun:      dryRun,
		Reason:      "跟单信号确认后自动入场",
		CreatedAt:   time.Now().UTC(),
	}

	result, err := executor.Execute(intent)
	if err != nil {
		log.Error("auto entry failed", zap.String("signal", sig.ID), zap.Error(err))
		return
	}
	log.Info("auto entry submitted",
		zap.String("signal", sig.ID),
		zap.String("token", sig.Token),
		zap.String("status", string(result.Status)),
		zap.Bool("dry_run", result.DryRun))
}

// toBaseUnits 把 USD 金额按实时价格与**链上真实精度**换算为最小单位数量。
//
// 关键点：代币精度差异极大（6 / 8 / 9 / 18），用固定值会导致数量级错误，
// 因此优先向适配器查询 decimals（可选能力接口），查询失败时保守回退 18 并记录。
func toBaseUnits(usd, priceUSD float64, adapter model.ChainAdapter, ctx context.Context, token string) (*big.Int, error) {
	if priceUSD <= 0 {
		return nil, errors.New("price unavailable")
	}
	if usd <= 0 {
		return nil, errors.New("amount must be positive")
	}
	tokens := usd / priceUSD

	decimals := uint8(18)
	if reader, ok := adapter.(interface {
		TokenDecimals(context.Context, string) (uint8, error)
	}); ok {
		if d, err := reader.TokenDecimals(ctx, token); err == nil {
			decimals = d
		}
	}

	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	value := new(big.Float).SetFloat64(tokens)
	value.Mul(value, new(big.Float).SetInt(scale))

	out, _ := value.Int(nil)
	return out, nil
}

// startUSDCPaymentWatcher 在启用配置时启动 USDC 收款监听。
//
// 配置：payment.usdc.{enabled,network,pay_to,...}；收款地址只从环境变量
// MEMEBOT_PAYMENT_USDC_PAY_TO 读取（避免误提交到仓库）。
func startUSDCPaymentWatcher(ctx context.Context, cfg *config.Config, pool *pgxpool.Pool, subSvc *subscription.Service, log *zap.Logger) {
	usdc := cfg.Payment.USDC
	if !usdc.Enabled {
		return
	}
	if pool == nil || subSvc == nil {
		log.Warn("USDC 收款已启用但数据库不可用，监听未启动")
		return
	}
	if usdc.PayTo == "" {
		log.Warn("USDC 收款已启用但未配置 MEMEBOT_PAYMENT_USDC_PAY_TO，监听未启动")
		return
	}

	asset := usdc.Asset
	if asset == "" {
		// 内置表按 "network|asset" 建键，取该网络下的第一个（USDC）
		prefix := strings.ToLower(usdc.Network) + "|"
		for k, info := range payment.DefaultAssets() {
			if strings.HasPrefix(k, prefix) {
				asset = info.Address
				break
			}
		}
	}
	if asset == "" {
		log.Error("USDC 收款：无法确定资产地址，监听未启动", zap.String("network", usdc.Network))
		return
	}

	rpcURLs := []string{}
	if ch, ok := cfg.Chain(usdc.Network); ok {
		rpcURLs = ch.RPCURLs
	}
	if len(rpcURLs) == 0 {
		log.Error("USDC 收款：网络未配置 RPC，监听未启动", zap.String("network", usdc.Network))
		return
	}

	logs := payment.NewEVMTransferSource("usdc-"+usdc.Network, rpcURLs)
	orderSource := subscription.NewUSDCOrderSource(subSvc, pool)
	watcher := payment.NewWatcher(payment.WatcherConfig{
		Asset:           asset,
		PayTo:           usdc.PayTo,
		Decimals:        6,
		Confirmations:   usdc.Confirmations,
		PollInterval:    time.Duration(usdc.PollSeconds) * time.Second,
		OrderWindow:     time.Duration(usdc.OrderWindowHours) * time.Hour,
		AmountTolerance: usdc.AmountTolerance,
	}, logs, orderSource, log)

	log.Info("USDC 收款监听已启动",
		zap.String("network", usdc.Network),
		zap.String("asset", asset),
		zap.Uint64("confirmations", usdc.Confirmations))
	go func() {
		if err := watcher.Run(ctx); err != nil && ctx.Err() == nil {
			log.Error("USDC 收款监听退出", zap.Error(err))
		}
	}()
}

// decayLoop 周期衰减信号：过期信号自动作废并通知。
func decayLoop(ctx context.Context, engine *strategy.SignalEngine, alerts model.AlertSink, log *zap.Logger) {
	ticker := time.NewTicker(2 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			for _, sig := range engine.Decay(now.UTC()) {
				log.Info("signal expired", zap.String("token", sig.Token))
				if alerts != nil {
					_ = alerts.Raise(ctx, &model.Alert{
						Level:   model.LevelInfo,
						Title:   "信号已过期（未满足确认条件）",
						Message: fmt.Sprintf("代币 %s 的信号在 %d 分钟内未获得跟风确认，已自动作废", sig.Token, 30),
						Chain:   sig.Chain,
						Token:   sig.Token,
					})
				}
			}
		}
	}
}

// handleTelegramAction 处理 Telegram 按钮回调与命令。
func handleTelegramAction(ctx context.Context, riskEngine *risk.Engine, engine *strategy.SignalEngine, action, payload string, log *zap.Logger) string {
	switch action {
	case "pause":
		if err := riskEngine.Pause("Telegram 一键熔断", time.Now().Add(time.Hour)); err != nil {
			return "熔断失败：" + err.Error()
		}
		return "交易已暂停 1 小时"
	case "resume":
		if err := riskEngine.Resume(); err != nil {
			return "恢复失败：" + err.Error()
		}
		return "交易已恢复"
	case "ignore":
		return "已忽略（30 分钟内不再提醒该代币）"
	case "status":
		paused, reason := riskEngine.Paused()
		state := "运行中"
		if paused {
			state = "已熔断：" + reason
		}
		return fmt.Sprintf("状态：%s；活跃信号 %d 个", state, len(engine.Active()))
	case "close":
		return "平仓指令已记录（需人工确认后由执行层处理）"
	default:
		_ = ctx
		_ = payload
		return "未知操作"
	}
}
