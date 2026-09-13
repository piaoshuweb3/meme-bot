package address

import (
	"sort"

	"meme-bot/internal/model"
)

// CostBasis 由交易流水重放得到的真实盈亏与剩余持仓。
type CostBasis struct {
	// RealizedPnLUSD 已实现盈亏合计（按加权平均成本法配对）。
	RealizedPnLUSD float64
	// HoldingQty 回放结束时的剩余持仓数量。
	HoldingQty float64
	// HoldingCostUSD 剩余持仓的成本（USD）。
	HoldingCostUSD float64
	// ClosedTrades 完成配对的平仓笔数。
	ClosedTrades int
	// Wins / Losses 平仓盈亏的正负计数。
	Wins, Losses int
	// TradePnLs 每笔平仓的已实现盈亏（用于一致性分析）。
	TradePnLs []float64
}

// ComputeCostBasis 用**加权平均成本法**从交易流水重放地址的真实盈亏。
//
// 为什么需要它：`trade_records.pnl_usd` 可能是采集时的估算值（或为空），
// 直接依赖会让 WinRate / ProfitFactor / MaxDrawdown 失真。这里按时间顺序配对：
//
//	买入：qty += amount/price，cost += amount
//	卖出：avgCost = cost/qty，realized += amount - avgCost*qty，qty/cost 相应减少
//
// 数量由 `AmountUSD / PriceUSD` 推算（流水只保证金额与价格可信）。
func ComputeCostBasis(trades []*model.TradeRecord) CostBasis {
	sorted := make([]*model.TradeRecord, 0, len(trades))
	for _, t := range trades {
		if t != nil && t.AmountUSD > 0 {
			sorted = append(sorted, t)
		}
	}
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].At.Before(sorted[j].At) })

	var (
		res  CostBasis
		qty  float64
		cost float64
	)

	for _, t := range sorted {
		price := t.PriceUSD
		if price <= 0 {
			continue // 缺价格的流水无法配对
		}
		delta := t.AmountUSD / price // 代币数量

		switch t.Side {
		case model.SideBuy:
			qty += delta
			cost += t.AmountUSD
		case model.SideSell:
			if qty <= 0 {
				// 无持仓可卖的卖出（例如我们只观测到部分流水）：按 0 成本计，保守记盈利为零
				continue
			}
			sold := delta
			if sold > qty {
				sold = qty // 不允许卖出超过持仓（数据缺口时截断）
			}
			avgCost := cost / qty
			proceeds := sold * price
			realized := proceeds - avgCost*sold

			res.RealizedPnLUSD += realized
			res.TradePnLs = append(res.TradePnLs, realized)
			res.ClosedTrades++
			if realized >= 0 {
				res.Wins++
			} else {
				res.Losses++
			}

			qty -= sold
			cost -= avgCost * sold
			if qty <= 1e-12 {
				qty, cost = 0, 0
			}
		}
	}

	res.HoldingQty = qty
	res.HoldingCostUSD = cost
	return res
}
