// Command worker 运行后台周期任务：
//   - 持仓标记价格刷新与风控退出检查（每 30 秒）
//   - 地址画像重算（每小时，取评分榜前 N 个地址）
//   - 每日汇总日志（UTC 03:00 触发一次全量重算）
//
// 依赖不可用时降级运行（例如无数据库时跳过画像任务）。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"meme-bot/internal/address"
	"meme-bot/internal/alert"
	"meme-bot/internal/chain"
	"meme-bot/internal/chain/aggregator"
	basechain "meme-bot/internal/chain/base"
	"meme-bot/internal/chain/privatetx"
	solanachain "meme-bot/internal/chain/solana"
	"meme-bot/internal/config"
	"meme-bot/internal/logger"
	"meme-bot/internal/metrics"
	"meme-bot/internal/model"
	"meme-bot/internal/outcome"
	"meme-bot/internal/provider"
	"meme-bot/internal/risk"
	"meme-bot/internal/storage"
	"meme-bot/internal/strategy"
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

	reg := metrics.New()

	var pool *pgxpool.Pool
	if p, err := storage.NewPostgres(ctx, cfg); err != nil {
		log.Warn("postgres unavailable, profile jobs disabled", zap.Error(err))
	} else {
		pool = p
		defer pool.Close()
	}

	rdb, err := storage.NewRedis(ctx, cfg)
	if err != nil {
		log.Warn("redis unavailable, using memory state store", zap.Error(err))
	} else {
		defer func() { _ = rdb.Close() }()
	}

	var stateStore risk.StateStore = risk.NewMemoryStore()
	if rdb != nil {
		stateStore = storage.NewRedisStateStore(rdb)
	}
	riskEngine := risk.New(cfg.Risk, stateStore, nil, reg, log)

	// 数据源与链
	market := provider.NewMarket()
	security := provider.NewSecurity(cfg.Providers.GoPlusAPIKey, market)
	privateTx := privatetx.NewManager()
	basechain.Configure(basechain.Deps{
		Market:     market,
		Security:   security,
		Aggregator: aggregator.NewOneInch(cfg.AggregatorBaseURL("base"), config.ResolveSecret(cfg.AggregatorKeyEnv("base"))),
		PrivateTx:  privateTx,
	})
	solanachain.Configure(solanachain.Deps{
		Market:    market,
		Security:  security,
		Jupiter:   aggregator.NewJupiter(cfg.AggregatorBaseURL("solana"), config.ResolveSecret(cfg.AggregatorKeyEnv("solana"))),
		PrivateTx: privateTx,
	})

	factory, err := chain.NewFactory(cfg, log)
	if err != nil {
		log.Fatal("chain factory", zap.Error(err))
	}

	alertMgr := alert.New(cfg.Alert, []model.Channel{alert.NewLogChannel(log)}, stateStore, reg, log)

	var positions model.PositionStore = strategy.NewMemoryPositions()
	var profiles *address.PGStore
	if pool != nil {
		positions = strategy.NewPGPositions(pool)
		profiles = address.NewPGStore(pool)
	}
	// 影子跟踪存储：pool 为 nil 时 Available()==false，采样任务自动跳过（降级而非失败）
	outcomeStore := outcome.NewPGStore(pool)

	scorer := address.NewScorer(cfg.Score, cfg.Signal.LargeBuyMultiple, profiles, log)

	log.Info("worker started",
		zap.Strings("jobs", []string{"position_mark(30s)", "profile_rebuild(1h)", "outcome_sample(2m)"}),
		zap.Strings("chains", factory.List()))

	var (
		markTicker    = time.NewTicker(30 * time.Second)
		profileTicker = time.NewTicker(time.Hour)
		outcomeTicker = time.NewTicker(2 * time.Minute)
	)
	defer markTicker.Stop()
	defer profileTicker.Stop()
	defer outcomeTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Info("worker stopped")
			return
		case <-markTicker.C:
			markPositions(ctx, factory, market, positions, riskEngine, alertMgr, log)
		case <-profileTicker.C:
			rebuildProfiles(ctx, profiles, scorer, factory.List(), log)
		case <-outcomeTicker.C:
			collectOutcomes(ctx, outcomeStore, market, outcome.DefaultHorizons, log)
		}
	}
}

// markPositions 刷新持仓标记价格并检查风控退出条件。
func markPositions(
	ctx context.Context,
	factory *chain.Factory,
	market *provider.Market,
	positions model.PositionStore,
	riskEngine *risk.Engine,
	alerts model.AlertSink,
	log *zap.Logger,
) {
	if positions == nil {
		return
	}
	for _, chainID := range factory.List() {
		open, err := positions.ListOpen(chainID)
		if err != nil {
			log.Warn("list open positions failed", zap.String("chain", chainID), zap.Error(err))
			continue
		}
		for _, p := range open {
			price := p.CurrentPriceUSD
			liquidity := p.CurrentLiquidityUSD

			if v, _, err := market.PriceUSD(ctx, p.Chain, p.Token); err == nil && v > 0 {
				price = v
			}
			if liq, _, _, err := market.LiquidityUSD(ctx, p.Chain, p.Token); err == nil {
				liquidity = liq
			}
			if price == 0 {
				continue
			}

			decision, err := riskEngine.CheckExit(p, price, liquidity)
			if err != nil {
				log.Warn("risk check failed", zap.Error(err))
				continue
			}
			if decision != nil && decision.Verdict == model.VerdictAllow {
				if alerts != nil {
					_ = alerts.Raise(ctx, &model.Alert{
						Level:   model.LevelCritical,
						Title:   "退出条件触发（需处理）",
						Message: fmt.Sprintf("代币 %s：%s；现价 %.8f", p.Token, decision.Reason, price),
						Chain:   p.Chain,
						Token:   p.Token,
						Buttons: []model.Button{
							{Text: "一键平仓", Data: "close:" + p.ID},
							{Text: "忽略 30min", Data: "ignore:" + p.Token},
							{Text: "暂停交易", Data: "pause:global"},
						},
					})
				}
			}

			p.CurrentPriceUSD = price
			p.CurrentLiquidityUSD = liquidity
			if price > p.PeakPriceUSD {
				p.PeakPriceUSD = price
			}
			p.UpdatedAt = time.Now().UTC()
			if err := positions.Update(p); err != nil {
				log.Warn("update position failed", zap.Error(err))
			}
		}
	}
}

// rebuildProfiles 重算评分榜地址的画像（受限于窗口与数量，避免打爆数据源）。
func rebuildProfiles(ctx context.Context, store *address.PGStore, scorer *address.Scorer, chains []string, log *zap.Logger) {
	if store == nil || scorer == nil {
		return
	}
	for _, chainID := range chains {
		list, err := store.ListTop(ctx, chainID, 200)
		if err != nil {
			log.Warn("list top profiles failed", zap.String("chain", chainID), zap.Error(err))
			continue
		}
		updated := 0
		for _, p := range list {
			select {
			case <-ctx.Done():
				return
			default:
			}
			if _, err := scorer.RebuildProfile(ctx, chainID, p.Address); err != nil {
				if !errors.Is(err, context.Canceled) {
					log.Debug("rebuild profile failed", zap.String("address", p.Address), zap.Error(err))
				}
				continue
			}
			updated++
			time.Sleep(50 * time.Millisecond) // 限速，避免触发数据源限流
		}
		log.Info("profiles rebuilt", zap.String("chain", chainID), zap.Int("updated", updated))
	}
}
