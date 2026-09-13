package backtest

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/big"
	"sort"
	"time"

	"go.uber.org/zap"

	"meme-bot/internal/model"
	"meme-bot/internal/risk"
	"meme-bot/internal/strategy"
)

// EventSource 事件源：按时间升序提供 Swap 事件。
type EventSource interface {
	// Next 返回下一个事件；ok=false 表示已结束。
	Next() (ev model.SwapEvent, ok bool, err error)
}

// position 回测持仓（一个代币一笔，与实盘"不加仓"一致）。
type position struct {
	signalID   string
	source     model.SignalSource
	chain      string
	token      string
	entryAt    time.Time
	entryPrice float64
	amountUSD  float64
	qty        float64
	peakPrice  float64
	feesUSD    float64
	model      *model.Position // 供风控引擎判断
}

// Engine 回放引擎。
type Engine struct {
	cfg    Config
	engine *strategy.SignalEngine
	risk   *risk.Engine
	log    *zap.Logger

	// OnEvent 事件观察钩子：在策略评估前调用（例如把事件推给回测用行情 stub）。
	OnEvent func(ev model.SwapEvent)
}

// New 构建回放引擎（engine / risk 传 nil 时对应环节被跳过，但会记入 Notes）。
func New(cfg Config, engine *strategy.SignalEngine, riskEngine *risk.Engine, log *zap.Logger) *Engine {
	if log == nil {
		log = zap.NewNop()
	}
	return &Engine{cfg: cfg, engine: engine, risk: riskEngine, log: log}
}

// Run 回放整个事件流并生成报告。
func (e *Engine) Run(ctx context.Context, src EventSource) (*Report, error) {
	if src == nil {
		return nil, errors.New("backtest: nil event source")
	}

	rep := &Report{Config: e.cfg, RunAt: time.Now().UTC()}
	if e.engine == nil {
		rep.Notes = append(rep.Notes, "未注入策略引擎：仅统计事件，不产生信号")
	}
	if e.risk == nil {
		rep.Notes = append(rep.Notes, "未注入风控引擎：跳过仓位与退出风控判定")
	}

	// 回测时钟：由事件时间驱动（TTL / 冷却 / 衰减 与真实时间无关）
	clock := time.Time{}
	if e.engine != nil {
		e.engine.WithClock(func() time.Time {
			if clock.IsZero() {
				return time.Now().UTC()
			}
			return clock
		})
	}

	var (
		cash         = e.cfg.InitialEquityUSD
		peakEquity   = e.cfg.InitialEquityUSD
		maxDrawdown  float64
		trades       []Trade
		equityCurve  []EquityPoint
		positions    = make(map[string]*position)
		symbols      = make(map[string]struct{})
		pending      = make(map[string]*model.Signal) // 待跟风确认的信号
		feesTotal    float64
		rejectedRisk int
		sumWin       float64
		sumLoss      float64
		wins         int
		holdSum      float64
	)

	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		ev, ok, err := src.Next()
		if err != nil {
			return nil, err
		}
		if !ok {
			break
		}

		clock = ev.At
		if ev.At.IsZero() {
			clock = time.Now().UTC()
		}
		if rep.FirstAt.IsZero() {
			rep.FirstAt = clock
		}
		rep.LastAt = clock
		rep.Metrics.Events++
		if ev.TokenOut != "" {
			symbols[ev.TokenOut] = struct{}{}
		}

		if e.OnEvent != nil {
			e.OnEvent(ev)
		}

		// 1) 先用当前事件价格更新持仓标记价并判定退出
		if pos, exists := positions[ev.TokenOut]; exists && ev.PriceUSD > 0 {
			if trade, exited := e.checkExit(clock, pos, ev.PriceUSD); exited {
				trades = append(trades, trade)
				cash += trade.AmountUSD + trade.PnLUSD
				feesTotal += trade.FeesUSD
				holdSum += trade.HoldingMin
				if trade.PnLUSD >= 0 {
					wins++
					sumWin += trade.PnLUSD
				} else {
					sumLoss += -trade.PnLUSD
				}
				delete(positions, ev.TokenOut)
			}
		}

		// 2) 生成信号：跟风确认模式下先挂起，等后续独立买家出现再判定
		if e.engine != nil && e.shouldEnter(ev) {
			sig, sErr := e.engine.Evaluate(ev)
			if sErr != nil {
				e.log.Debug("backtest: evaluate failed", zap.Error(sErr))
			} else if sig != nil && (e.cfg.MinSignalAmountUSD <= 0 || sig.AmountUSD >= e.cfg.MinSignalAmountUSD) {
				rep.Metrics.Signals++
				if !e.cfg.FollowConfirm {
					if pos := e.tryEnter(clock, sig, ev.PriceUSD, cash, &rejectedRisk, rep); pos != nil {
						positions[pos.token] = pos
						cash -= pos.amountUSD
					}
				} else if _, held := positions[sig.Token]; !held {
					pending[sig.Token] = sig
				}
			}
		}

		// 3) 对待确认信号尝试跟风确认（依赖后续独立买家，故必须在后续事件里判定）
		for token, sig := range pending {
			if _, held := positions[token]; held {
				delete(pending, token)
				continue
			}
			confirmed, cErr := e.engine.Confirm(sig)
			if cErr != nil || confirmed == nil {
				continue
			}
			switch confirmed.Status {
			case model.SignalConfirmed:
				if pos := e.tryEnter(clock, confirmed, ev.PriceUSD, cash, &rejectedRisk, rep); pos != nil {
					positions[pos.token] = pos
					cash -= pos.amountUSD
				}
				delete(pending, token)
			case model.SignalExpired:
				delete(pending, token)
			}
		}

		// 4) 权益曲线（现金 + 持仓市值）
		equity := cash
		for _, p := range positions {
			mark := p.entryPrice
			if ev.TokenOut == p.token && ev.PriceUSD > 0 {
				mark = ev.PriceUSD
			}
			equity += p.qty * mark
		}
		if equity > peakEquity {
			peakEquity = equity
		}
		if peakEquity > 0 {
			dd := (peakEquity - equity) / peakEquity
			if dd > maxDrawdown {
				maxDrawdown = dd
			}
		}
		equityCurve = append(equityCurve, EquityPoint{At: clock, EquityUSD: round2(equity)})
	}

	// 收尾：剩余持仓按最新标记价强制结算，避免虚增权益
	for token, pos := range positions {
		mark := pos.entryPrice
		if pos.model != nil && pos.model.CurrentPriceUSD > 0 {
			mark = pos.model.CurrentPriceUSD
		}
		trade := e.settle(rep.LastAt, pos, mark, "回放结束强制结算")
		trades = append(trades, trade)
		cash += trade.AmountUSD + trade.PnLUSD
		feesTotal += trade.FeesUSD
		holdSum += trade.HoldingMin
		if trade.PnLUSD >= 0 {
			wins++
			sumWin += trade.PnLUSD
		} else {
			sumLoss += -trade.PnLUSD
		}
		delete(positions, token)
	}

	sort.Slice(trades, func(i, j int) bool { return trades[i].EntryAt.Before(trades[j].EntryAt) })

	finalEquity := cash
	rep.Trades = trades
	rep.Equity = equityCurve
	rep.Metrics.Symbols = len(symbols)
	rep.Metrics.Trades = len(trades)
	rep.Metrics.Wins = wins
	rep.Metrics.Losses = len(trades) - wins
	rep.Metrics.RejectedByRisk = rejectedRisk
	rep.Metrics.TotalPnLUSD = round2(finalEquity - e.cfg.InitialEquityUSD)
	rep.Metrics.FinalEquityUSD = round2(finalEquity)
	rep.Metrics.MaxDrawdown = round4(maxDrawdown)
	rep.Metrics.FeesPaidUSD = round2(feesTotal)
	if e.cfg.InitialEquityUSD > 0 {
		rep.Metrics.ReturnPct = round4((finalEquity - e.cfg.InitialEquityUSD) / e.cfg.InitialEquityUSD)
	}
	if len(trades) > 0 {
		rep.Metrics.WinRate = round4(float64(wins) / float64(len(trades)))
		rep.Metrics.AvgHoldingMin = round2(holdSum / float64(len(trades)))
	}
	if sumLoss > 0 {
		rep.Metrics.ProfitFactor = round4(sumWin / sumLoss)
	} else if sumWin > 0 {
		rep.Metrics.ProfitFactor = math.Inf(1)
	}
	if wins > 0 {
		rep.Metrics.AvgWinUSD = round2(sumWin / float64(wins))
	}
	if losses := len(trades) - wins; losses > 0 {
		rep.Metrics.AvgLossUSD = round2(-sumLoss / float64(losses))
	}

	rep.Notes = append(rep.Notes,
		"回测复用实盘策略漏斗与风控引擎；成交按事件价格加滑点与手续费模拟",
		"冷却/日亏损等风控状态按回放墙钟计时，与实盘存在语义差异",
		fmt.Sprintf("事件 %d 条，信号 %d 个，成交 %d 笔", rep.Metrics.Events, rep.Metrics.Signals, rep.Metrics.Trades),
	)
	return rep, nil
}

// shouldEnter 入场前提：有价格、有成交额、目标是具体代币。
func (e *Engine) shouldEnter(ev model.SwapEvent) bool {
	return ev.TokenOut != "" && ev.PriceUSD > 0 && ev.AmountUSD > 0
}

// tryEnter 依据风控裁决建仓；返回 nil 表示未入场。
func (e *Engine) tryEnter(now time.Time, sig *model.Signal, markPrice, cash float64, rejected *int, rep *Report) *position {
	price := markPrice
	if price <= 0 {
		price = sig.PriceUSD // 退化：使用信号生成时的价格
	}
	if price <= 0 {
		return nil
	}

	amount := cash * 0.02 // 无风控引擎时的保守默认（2% 权益）
	if e.risk != nil {
		decision, err := e.risk.CheckEntry(model.EntryRequest{
			Chain:        sig.Chain,
			Token:        sig.Token,
			SignalID:     sig.ID,
			Source:       sig.Source,
			Address:      sig.TriggerAddress,
			AmountUSD:    sig.AmountUSD,
			PriceUSD:     price,
			LiquidityUSD: sig.LiquidityUSD,
			SlippageBps:  e.cfg.SlippageBps,
			RequestedAt:  now,
		})
		if err != nil {
			rep.Notes = append(rep.Notes, "风控检查出错："+err.Error())
			*rejected++
			return nil
		}
		if decision == nil || decision.Verdict == model.VerdictReject || decision.AllowedUSD <= 0 {
			*rejected++
			return nil
		}
		amount = decision.AllowedUSD
	}

	if amount <= 0 || amount > cash { // 现金不足则跳过（回测不允许透支）
		return nil
	}
	return e.newPosition(now, sig, price, amount)
}

// newPosition 构建持仓（入场含滑点，手续费单边计入）。
func (e *Engine) newPosition(now time.Time, sig *model.Signal, basePrice, amountUSD float64) *position {
	slip := float64(e.cfg.SlippageBps) / 10000.0
	entry := basePrice * (1 + slip) // 买入按不利方向（更贵）
	if entry <= 0 {
		return nil
	}
	qty := amountUSD / entry
	fee := amountUSD * float64(e.cfg.FeeBps) / 10000.0
	stopLoss := e.cfg.StopLossPct
	if stopLoss <= 0 && e.risk != nil {
		stopLoss = 0.25
	}

	mp := &model.Position{
		Chain:               sig.Chain,
		Token:               sig.Token,
		Amount:              big.NewInt(0),
		EntryPriceUSD:       entry,
		CurrentPriceUSD:     entry,
		PeakPriceUSD:        entry,
		StopLossPct:         stopLoss,
		LiquidityAtEntryUSD: sig.LiquidityUSD,
		CurrentLiquidityUSD: sig.LiquidityUSD,
		SignalID:            sig.ID,
		Status:              model.PositionOpen,
		OpenedAt:            now,
		UpdatedAt:           now,
	}

	return &position{
		signalID:   sig.ID,
		source:     sig.Source,
		chain:      sig.Chain,
		token:      sig.Token,
		entryAt:    now,
		entryPrice: entry,
		amountUSD:  amountUSD,
		qty:        qty,
		peakPrice:  entry,
		feesUSD:    fee,
		model:      mp,
	}
}

// checkExit 判定是否退出（复用实盘风控 + 止盈 + 超时）。
func (e *Engine) checkExit(now time.Time, pos *position, price float64) (Trade, bool) {
	if pos == nil || price <= 0 {
		return Trade{}, false
	}
	if price > pos.peakPrice {
		pos.peakPrice = price
	}
	pos.model.CurrentPriceUSD = price
	pos.model.PeakPriceUSD = pos.peakPrice
	pos.model.UpdatedAt = now

	if e.risk != nil {
		if decision, err := e.risk.CheckExit(pos.model, price, pos.model.LiquidityAtEntryUSD); err == nil && decision != nil {
			if decision.Verdict == model.VerdictAllow {
				return e.settle(now, pos, price, decision.Reason), true
			}
		}
	} else if pos.model.StopLossPct > 0 && price <= pos.entryPrice*(1-pos.model.StopLossPct) {
		return e.settle(now, pos, price, "触发止损"), true
	}

	if e.cfg.TakeProfitPct > 0 && price >= pos.entryPrice*(1+e.cfg.TakeProfitPct) {
		return e.settle(now, pos, price, "触发止盈"), true
	}
	if e.cfg.MaxHoldingMinutes > 0 && now.Sub(pos.entryAt) >= time.Duration(e.cfg.MaxHoldingMinutes)*time.Minute {
		return e.settle(now, pos, price, "超过最大持仓时长"), true
	}
	return Trade{}, false
}

// settle 生成成交记录：出场按滑点不利方向折价，双边手续费与净盈亏一并计入。
func (e *Engine) settle(now time.Time, pos *position, price float64, reason string) Trade {
	slip := float64(e.cfg.SlippageBps) / 10000.0
	exitPrice := price * (1 - slip) // 卖出按不利方向（更便宜）
	if exitPrice < 0 {
		exitPrice = 0
	}
	exitValue := pos.qty * exitPrice
	exitFee := exitValue * float64(e.cfg.FeeBps) / 10000.0
	totalFees := pos.feesUSD + exitFee
	pnl := exitValue - pos.amountUSD - totalFees

	ret := 0.0
	if pos.amountUSD > 0 {
		ret = pnl / pos.amountUSD
	}
	return Trade{
		Token:      pos.token,
		Chain:      pos.chain,
		SignalID:   pos.signalID,
		Source:     string(pos.source),
		EntryAt:    pos.entryAt,
		EntryPrice: round8(pos.entryPrice),
		ExitAt:     now,
		ExitPrice:  round8(exitPrice),
		ExitReason: reason,
		AmountUSD:  round2(pos.amountUSD),
		PnLUSD:     round2(pnl),
		ReturnPct:  round4(ret),
		HoldingMin: round2(now.Sub(pos.entryAt).Minutes()),
		FeesUSD:    round2(totalFees),
	}
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }
func round4(v float64) float64 { return math.Round(v*10000) / 10000 }
func round8(v float64) float64 { return math.Round(v*1e8) / 1e8 }
