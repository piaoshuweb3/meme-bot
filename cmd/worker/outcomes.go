package main

import (
	"context"
	"time"

	"go.uber.org/zap"

	"meme-bot/internal/outcome"
	"meme-bot/internal/provider"
)

// collectOutcomes 采样到期的影子表现样本。
//
// 行为约定：
//   - 单轮有上限（batchLimit），避免一次打满行情 API 配额；
//   - 取价失败按指数退避重试，不丢弃样本（丢弃等于让对照组失真）；
//   - 上下文取消时立即返回，不做"尽力而为"的收尾写入。
func collectOutcomes(
	ctx context.Context,
	store *outcome.PGStore,
	market *provider.Market,
	horizons []outcome.Horizon,
	log *zap.Logger,
) {
	if store == nil || !store.Available() || market == nil {
		return
	}
	const batchLimit = 20

	now := time.Now().UTC()
	pending, err := store.DueSamples(ctx, now, outcome.DefaultSampleGrace, batchLimit)
	if err != nil {
		log.Warn("outcomes: 读取待采样失败", zap.Error(err))
		return
	}
	if len(pending) == 0 {
		return
	}

	var okCount, failCount int
	for _, p := range pending {
		if ctx.Err() != nil {
			return
		}

		price, _, perr := market.PriceUSD(ctx, p.Chain, p.Token)
		if perr != nil || price <= 0 {
			attempts := p.Attempts + 1
			if merr := store.MarkFailed(ctx, p.OutcomeID, p.Horizon, "NO_PRICE", attempts,
				outcome.NextRetry(attempts, now)); merr != nil {
				log.Warn("outcomes: 记录重试失败", zap.Error(merr))
			}
			failCount++
			continue
		}

		ret := price/p.BaselinePrice - 1
		if merr := store.MarkSampled(ctx, p.OutcomeID, p.Horizon, price, ret, now); merr != nil {
			log.Warn("outcomes: 记录采样失败", zap.Error(merr))
			failCount++
			continue
		}
		okCount++
	}

	log.Info("outcomes sampled",
		zap.Int("ok", okCount),
		zap.Int("failed", failCount),
		zap.Int("pending", len(pending)))
}

// recordOutcome 记录一条决策样本（供信号/执行路径调用）。
//
// 被过滤样本会走稳定抽样（ShouldSample），未命中则不记录——这是刻意的：
// 对照组只需要无偏样本，不需要全量。
func recordOutcome(
	ctx context.Context,
	store *outcome.PGStore,
	chain, token string,
	decision outcome.Decision,
	reason string,
	price float64,
	version string,
	metric map[string]float64,
	horizons []outcome.Horizon,
	log *zap.Logger,
) {
	if store == nil || !store.Available() {
		return
	}
	rec, ok := outcome.NewRecord(chain, token, decision, reason, time.Now().UTC(), price, version, metric)
	if !ok {
		return // 无基准价或未命中抽样：不记录（不臆造基准、不做有偏抽样）
	}
	if err := store.Save(ctx, rec, horizons); err != nil {
		log.Debug("outcomes: 记录决策失败", zap.String("token", token), zap.Error(err))
	}
}
