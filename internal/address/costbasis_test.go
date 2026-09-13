package address

import (
	"math"
	"testing"
	"time"

	"meme-bot/internal/model"
)

var cbBase = time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

func cbTrade(min int, side model.Side, token string, amount, price float64) *model.TradeRecord {
	return &model.TradeRecord{
		Chain: "base", Address: "0x1", Token: token, Side: side,
		AmountUSD: amount, PriceUSD: price, At: cbBase.Add(time.Duration(min) * time.Minute),
	}
}

func TestCostBasisWinAndLoss(t *testing.T) {
	basis := ComputeCostBasis([]*model.TradeRecord{
		cbTrade(0, model.SideBuy, "T", 100, 1.0),   // 100 qty，成本 100
		cbTrade(10, model.SideSell, "T", 150, 1.5), // 卖 100 qty → +50
		cbTrade(20, model.SideBuy, "U", 200, 2.0),  // 100 qty，成本 200
		cbTrade(30, model.SideSell, "U", 150, 1.5), // 卖 100 qty → -50
	})

	if basis.ClosedTrades != 2 {
		t.Fatalf("应有 2 笔平仓，实际 %d", basis.ClosedTrades)
	}
	if basis.Wins != 1 || basis.Losses != 1 {
		t.Fatalf("应 1 胜 1 负，实际 %d/%d", basis.Wins, basis.Losses)
	}
	if math.Abs(basis.RealizedPnLUSD) > 1e-6 {
		t.Fatalf("净盈亏应为 0，实际 %v", basis.RealizedPnLUSD)
	}
	if basis.HoldingQty != 0 || math.Abs(basis.HoldingCostUSD) > 1e-6 {
		t.Fatalf("应已清仓，剩余 %v qty / %v cost", basis.HoldingQty, basis.HoldingCostUSD)
	}
	if len(basis.TradePnLs) != 2 || math.Abs(basis.TradePnLs[0]-50) > 1e-6 || math.Abs(basis.TradePnLs[1]+50) > 1e-6 {
		t.Fatalf("逐笔盈亏异常：%v", basis.TradePnLs)
	}
}

func TestCostBasisPartialClose(t *testing.T) {
	// 买 100 qty（成本 100）后只卖 40 qty → 剩余 60 qty，成本 60
	basis := ComputeCostBasis([]*model.TradeRecord{
		cbTrade(0, model.SideBuy, "T", 100, 1.0),
		cbTrade(5, model.SideSell, "T", 60, 1.5), // 卖 40 qty，成本 40 → +20
	})

	if basis.ClosedTrades != 1 {
		t.Fatalf("应有 1 笔平仓，实际 %d", basis.ClosedTrades)
	}
	if math.Abs(basis.TradePnLs[0]-20) > 1e-6 {
		t.Fatalf("部分平仓盈亏应为 +20，实际 %v", basis.TradePnLs[0])
	}
	if math.Abs(basis.HoldingQty-60) > 1e-6 {
		t.Fatalf("剩余数量应为 60，实际 %v", basis.HoldingQty)
	}
	if math.Abs(basis.HoldingCostUSD-60) > 1e-6 {
		t.Fatalf("剩余成本应为 60，实际 %v", basis.HoldingCostUSD)
	}
}

func TestCostBasisWeightedAverage(t *testing.T) {
	// 先买 100 qty @1（成本 100），再买 100 qty @2（成本 200）→ 均价 1.5
	// 卖 200 qty @2.0 → 收入 400，成本 300 → +100
	basis := ComputeCostBasis([]*model.TradeRecord{
		cbTrade(0, model.SideBuy, "T", 100, 1.0),
		cbTrade(1, model.SideBuy, "T", 200, 2.0),
		cbTrade(2, model.SideSell, "T", 400, 2.0),
	})

	if math.Abs(basis.RealizedPnLUSD-100) > 1e-6 {
		t.Fatalf("加权平均成本法应得 +100，实际 %v", basis.RealizedPnLUSD)
	}
}

func TestCostBasisEdgeCases(t *testing.T) {
	// 无持仓的卖出（数据缺口）：跳过，不 panic、不计入
	basis := ComputeCostBasis([]*model.TradeRecord{
		cbTrade(0, model.SideSell, "T", 100, 1.0),
	})
	if basis.ClosedTrades != 0 || basis.RealizedPnLUSD != 0 {
		t.Fatalf("无持仓卖出应被忽略：%+v", basis)
	}

	// 缺价格：跳过
	basis = ComputeCostBasis([]*model.TradeRecord{
		cbTrade(0, model.SideBuy, "T", 100, 0),
		cbTrade(1, model.SideSell, "T", 120, 0),
	})
	if basis.ClosedTrades != 0 || basis.HoldingQty != 0 {
		t.Fatalf("缺价格应被跳过：%+v", basis)
	}

	// 卖出数量超过持仓（数据缺口）：按持仓截断，成本归零
	basis = ComputeCostBasis([]*model.TradeRecord{
		cbTrade(0, model.SideBuy, "T", 100, 1.0),  // 100 qty
		cbTrade(1, model.SideSell, "T", 500, 1.0), // 试图卖 500 qty
	})
	if basis.HoldingQty != 0 {
		t.Fatalf("超卖后持仓应归零，实际 %v", basis.HoldingQty)
	}
	if math.Abs(basis.TradePnLs[0]-0) > 1e-6 {
		t.Fatalf("按成本价卖出应盈亏为 0，实际 %v", basis.TradePnLs[0])
	}

	// 空输入
	if got := ComputeCostBasis(nil); got.ClosedTrades != 0 || got.HoldingCostUSD != 0 {
		t.Fatalf("空输入应返回零值：%+v", got)
	}
}
