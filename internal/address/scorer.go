// Package address 实现跟单地址筛选：四层过滤漏斗 + 实时动态评分。
//
// 对齐文书要求的四层筛选：
//  1. 基础标签与黑名单（项目方 / CEX / 混币器 / 套利机器人 / Rug 记录）
//  2. 历史表现量化（滚动窗口）：WinRate / ProfitFactor / MaxDrawdown / Consistency / HoldingTime
//  3. 行为特征：金额稳定性、跟风确认、分批出货、活跃度
//  4. 实时动态评分：加权公式，只有 Score > 阈值且近期有有效交易才进入“可跟单池”
package address

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"go.uber.org/zap"

	"meme-bot/internal/config"
	"meme-bot/internal/model"
)

// Scorer 地址评分与筛选服务。
type Scorer struct {
	cfg              config.ScoreConfig
	largeBuyMultiple float64
	store            model.ProfileStore
	log              *zap.Logger
}

// NewScorer 构建评分服务。
func NewScorer(cfg config.ScoreConfig, largeBuyMultiple float64, store model.ProfileStore, log *zap.Logger) *Scorer {
	if log == nil {
		log = zap.NewNop()
	}
	if largeBuyMultiple <= 0 {
		largeBuyMultiple = 2.0 // 文书建议：超过历史中位数 2 倍视为大额
	}
	if cfg.WindowDays <= 0 {
		cfg.WindowDays = 90
	}
	return &Scorer{cfg: cfg, largeBuyMultiple: largeBuyMultiple, store: store, log: log}
}

var _ model.ScorerPort = (*Scorer)(nil)

// Score 计算综合评分（0-1）。
//
//	Score = 0.30·WinRate + 0.25·ProfitFactor_norm + 0.20·(1-MaxDD)
//	      + 0.15·Consistency + 0.10·Recency
//
// 权重可在 configs/config.yaml 的 score 段调整。
func (s *Scorer) Score(p *model.AddressProfile) float64 {
	if p == nil {
		return 0
	}
	win := clamp01(p.WinRate)

	// ProfitFactor 以 3.0 为“优秀”上界做归一化
	pf := clamp01(p.ProfitFactor / 3.0)

	dd := 1 - clamp01(p.MaxDrawdown)
	consistency := clamp01(p.Consistency)
	recency := recencyScore(p.LastActive, time.Now(), 30*24*time.Hour)

	w := s.weights()
	return w[0]*win + w[1]*pf + w[2]*dd + w[3]*consistency + w[4]*recency
}

// weights 返回归一化后的权重（保证和恒为 1，配置写错也不会导致评分溢出）。
func (s *Scorer) weights() [5]float64 {
	w := [5]float64{
		orDefault(s.cfg.WeightWinRate, 0.30),
		orDefault(s.cfg.WeightProfitFact, 0.25),
		orDefault(s.cfg.WeightDrawdown, 0.20),
		orDefault(s.cfg.WeightConsistency, 0.15),
		orDefault(s.cfg.WeightRecency, 0.10),
	}
	var sum float64
	for _, v := range w {
		sum += v
	}
	if sum <= 0 {
		return [5]float64{0.30, 0.25, 0.20, 0.15, 0.10}
	}
	for i := range w {
		w[i] = w[i] / sum
	}
	return w
}

// IsEligible 判断地址是否进入可跟单池（实现 model.ScorerPort）。
func (s *Scorer) IsEligible(ctx context.Context, chain, address string) (bool, float64, error) {
	profile, err := s.store.Get(ctx, chain, address)
	if err != nil {
		return false, 0, err
	}
	if profile == nil {
		return false, 0, nil
	}

	res := Screen(profile, s.cfg)
	score := s.Score(profile)
	if !res.Passed {
		return false, score, nil
	}
	return score >= s.threshold(), score, nil
}

// IsLargeBuy 判断该笔买入是否为大额（相对该地址历史买入中位数）。
func (s *Scorer) IsLargeBuy(ctx context.Context, chain, address string, amountUSD float64) (bool, error) {
	profile, err := s.store.Get(ctx, chain, address)
	if err != nil {
		return false, err
	}
	if profile == nil || profile.MedianBuyUSD <= 0 {
		// 没有历史样本时不认为是“大额”（保守，避免跟单冷启动地址）
		return false, nil
	}
	return amountUSD >= profile.MedianBuyUSD*s.largeBuyMultiple, nil
}

func (s *Scorer) threshold() float64 {
	if s.cfg.ScoreThreshold <= 0 {
		return 0.70
	}
	return s.cfg.ScoreThreshold
}

// Screen 执行四层筛选漏斗的前三层（标签/黑名单 + 历史指标 + 行为稳定性）。
func Screen(p *model.AddressProfile, cfg config.ScoreConfig) model.FilterResult {
	if p == nil {
		return model.FilterResult{Passed: false, Reason: "无地址画像"}
	}
	res := model.FilterResult{Passed: true}

	// 第 1 层：黑名单与标签
	if p.IsBlacklisted {
		return model.FilterResult{Passed: false, Reason: "地址在黑名单：" + p.BlacklistReason}
	}
	for _, tag := range p.Tags {
		switch tag {
		case model.TagTeam, model.TagCEX, model.TagMixer, model.TagBot, model.TagSuspect:
			return model.FilterResult{Passed: false, Reason: "标签命中排除项：" + string(tag)}
		}
	}
	res.Checks = append(res.Checks, "tags-ok")

	// 第 2 层：历史表现
	minTrades := cfg.MinTrades
	if minTrades <= 0 {
		minTrades = 20
	}
	if p.TotalTrades < minTrades {
		return model.FilterResult{Passed: false, Reason: fmt.Sprintf("历史交易数不足（%d < %d）", p.TotalTrades, minTrades)}
	}
	if cfg.MinWinRate > 0 && p.WinRate < cfg.MinWinRate {
		return model.FilterResult{Passed: false, Reason: fmt.Sprintf("胜率不足（%.2f < %.2f）", p.WinRate, cfg.MinWinRate)}
	}
	if cfg.MinProfitFactor > 0 && p.ProfitFactor < cfg.MinProfitFactor {
		return model.FilterResult{Passed: false, Reason: fmt.Sprintf("盈亏比不足（%.2f < %.2f）", p.ProfitFactor, cfg.MinProfitFactor)}
	}
	if cfg.MaxDrawdown > 0 && p.MaxDrawdown > cfg.MaxDrawdown {
		return model.FilterResult{Passed: false, Reason: fmt.Sprintf("回撤过大（%.2f > %.2f）", p.MaxDrawdown, cfg.MaxDrawdown)}
	}
	res.Checks = append(res.Checks, "performance-ok")

	// 第 3 层：行为特征（持仓时长过短 = 秒级砸盘嫌疑）
	if p.AvgHoldSeconds > 0 && p.AvgHoldSeconds < 60 {
		return model.FilterResult{Passed: false, Reason: "平均持仓时间过短（疑似机器人）"}
	}
	res.Checks = append(res.Checks, "behavior-ok")
	return res
}

// RebuildProfile 依据窗口内的交易记录重算地址画像（由 worker 定时全量刷新）。
func (s *Scorer) RebuildProfile(ctx context.Context, chain, address string) (*model.AddressProfile, error) {
	since := time.Now().AddDate(0, 0, -s.cfg.WindowDays)
	trades, err := s.store.RecentTrades(ctx, chain, address, since, 1000)
	if err != nil {
		return nil, err
	}
	if len(trades) == 0 {
		if existing, err := s.store.Get(ctx, chain, address); err == nil && existing != nil {
			existing.RecentScore = 0
			existing.UpdatedAt = time.Now().UTC()
			_ = s.store.Upsert(ctx, existing)
			return existing, nil
		}
		return nil, nil
	}

	stats := computeStats(trades)
	profile, err := s.store.Get(ctx, chain, address)
	if err != nil {
		return nil, err
	}
	if profile == nil {
		profile = &model.AddressProfile{Address: address, Chain: chain}
	}
	profile.WinRate = stats.winRate
	profile.ProfitFactor = stats.profitFactor
	profile.MaxDrawdown = stats.maxDrawdown
	profile.Consistency = stats.consistency
	profile.SizeConsistency = stats.sizeConsistency
	profile.MedianBuyUSD = stats.medianBuyUSD
	profile.TotalTrades = len(trades)
	profile.WinningTrades = stats.wins
	profile.AvgHoldSeconds = stats.avgHoldSeconds
	profile.LastActive = trades[len(trades)-1].At
	profile.RecentScore = s.Score(profile)
	profile.UpdatedAt = time.Now().UTC()

	if err := s.store.Upsert(ctx, profile); err != nil {
		return nil, err
	}
	s.log.Info("profile rebuilt",
		zap.String("address", address), zap.Float64("score", profile.RecentScore))
	return profile, nil
}

// ---- 统计 ----

type stats struct {
	winRate         float64
	profitFactor    float64
	maxDrawdown     float64
	consistency     float64
	sizeConsistency float64
	medianBuyUSD    float64
	avgHoldSeconds  int64
	wins            int
}

// computeStats 从交易记录计算画像指标（简化版，Stage 2 可引入成本基准与真实持仓配对）。
func computeStats(trades []*model.TradeRecord) stats {
	sorted := make([]*model.TradeRecord, len(trades))
	copy(sorted, trades)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].At.Before(sorted[j].At) })

	var (
		wins, losses        int
		grossWin, grossLoss float64
		buys                []float64
		pnls                []float64
		equity, peak, maxDD float64
		lastBuyAt           time.Time
		holdSamples         []int64
	)

	for _, t := range sorted {
		switch strings.ToLower(string(t.Side)) {
		case "buy":
			buys = append(buys, t.AmountUSD)
			lastBuyAt = t.At
		case "sell":
			if !lastBuyAt.IsZero() {
				holdSamples = append(holdSamples, int64(t.At.Sub(lastBuyAt).Seconds()))
			}
			switch {
			case t.PnLUSD > 0:
				wins++
				grossWin += t.PnLUSD
			case t.PnLUSD < 0:
				losses++
				grossLoss += -t.PnLUSD
			}
			pnls = append(pnls, t.PnLUSD)
			equity += t.PnLUSD
			if equity > peak {
				peak = equity
			}
			if peak > 0 {
				dd := (peak - equity) / peak
				if dd > maxDD {
					maxDD = dd
				}
			}
		}
	}

	var st stats
	total := wins + losses
	if total > 0 {
		st.winRate = float64(wins) / float64(total)
		st.wins = wins
	}
	if grossLoss > 0 {
		st.profitFactor = grossWin / grossLoss
	} else if grossWin > 0 {
		st.profitFactor = 10 // 无亏损样本时给一个高上限，避免除零
	}
	st.maxDrawdown = clamp01(maxDD)

	// Consistency：盈利交易分布均匀度（用盈利样本标准差的反向指标近似）
	st.consistency = consistencyOf(pnls)

	// SizeConsistency：买入金额稳定性
	st.sizeConsistency = sizeConsistencyOf(buys)

	// 中位数买入金额（大额买入判定基准）
	st.medianBuyUSD = median(buys)

	// 平均持仓时长
	if len(holdSamples) > 0 {
		var sum int64
		for _, v := range holdSamples {
			sum += v
		}
		st.avgHoldSeconds = sum / int64(len(holdSamples))
	}
	// 盈亏类指标改用**加权平均成本法**配对（真实建仓成本），而非流水里的估算 pnl：
	// 这样 WinRate / ProfitFactor / MaxDrawdown / Consistency 才是可复核的口径。
	basis := ComputeCostBasis(trades)
	if basis.ClosedTrades > 0 {
		st.winRate = float64(basis.Wins) / float64(basis.ClosedTrades)
		st.wins = basis.Wins
		st.consistency = consistencyOf(basis.TradePnLs)

		var grossWin, grossLoss float64
		equity, peak, maxDD := 0.0, 0.0, 0.0
		for _, pnl := range basis.TradePnLs {
			if pnl > 0 {
				grossWin += pnl
			} else {
				grossLoss += -pnl
			}
			equity += pnl
			if equity > peak {
				peak = equity
			}
			if peak > 0 {
				if dd := (peak - equity) / peak; dd > maxDD {
					maxDD = dd
				}
			}
		}
		switch {
		case grossLoss > 0:
			st.profitFactor = grossWin / grossLoss
		case grossWin > 0:
			st.profitFactor = 10 // 无亏损样本时给上限，避免除零
		}
		st.maxDrawdown = clamp01(maxDD)
	}
	return st
}

// consistencyOf 用“盈利交易占比与均匀度”近似一致性（0-1，越高越稳定）。
func consistencyOf(pnls []float64) float64 {
	if len(pnls) == 0 {
		return 0
	}
	var pos []float64
	for _, v := range pnls {
		if v > 0 {
			pos = append(pos, v)
		}
	}
	if len(pos) == 0 {
		return 0
	}
	mean := avg(pos)
	if mean <= 0 {
		return 0
	}
	var variance float64
	for _, v := range pos {
		d := v - mean
		variance += d * d
	}
	variance /= float64(len(pos))
	cv := math.Sqrt(variance) / mean // 变异系数：越小越均匀
	return clamp01(1 - cv)
}

// sizeConsistencyOf 用买入金额的变异系数反转衡量稳定性。
func sizeConsistencyOf(buys []float64) float64 {
	if len(buys) < 2 {
		return 0
	}
	mean := avg(buys)
	if mean <= 0 {
		return 0
	}
	var variance float64
	for _, v := range buys {
		d := v - mean
		variance += d * d
	}
	variance /= float64(len(buys))
	cv := math.Sqrt(variance) / mean
	return clamp01(1 - cv)
}

func avg(vs []float64) float64 {
	if len(vs) == 0 {
		return 0
	}
	var sum float64
	for _, v := range vs {
		sum += v
	}
	return sum / float64(len(vs))
}

func median(vs []float64) float64 {
	if len(vs) == 0 {
		return 0
	}
	sorted := make([]float64, len(vs))
	copy(sorted, vs)
	sort.Float64s(sorted)
	n := len(sorted)
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}

// recencyScore 时间衰减：距上次活跃越久分数越低（半衰期默认 30 天）。
func recencyScore(lastActive, now time.Time, halfLife time.Duration) float64 {
	if lastActive.IsZero() {
		return 0.1
	}
	age := now.Sub(lastActive)
	if age <= 0 {
		return 1.0
	}
	if halfLife <= 0 {
		halfLife = 30 * 24 * time.Hour
	}
	ratio := math.Pow(0.5, age.Hours()/halfLife.Hours())
	return clamp01(ratio)
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func orDefault(v, def float64) float64 {
	if v <= 0 {
		return def
	}
	return v
}
