package base

import (
	"context"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	"meme-bot/internal/model"
)

// Uniswap V3 风格 Swap 事件：
//
//	event Swap(address indexed sender, address indexed recipient,
//	           int256 amount0, int256 amount1,
//	             uint160 sqrtPriceX96, uint128 liquidity, int24 tick)
//
// 与 V2 的关键差异：
//   - 金额是**有符号 int256**：正数表示流入池子，负数表示流出池子；
//   - 方向由两个金额的符号共同决定（不需要额外读取池子状态）；
//   - 数据段固定 5 个字（160 字节）：amount0 / amount1 / sqrtPriceX96 / liquidity / tick。
const v3SwapDataWords = 5

// decodeInt256 解析 32 字节二补码有符号整数。
func decodeInt256(b []byte) *big.Int {
	if len(b) < 32 {
		return big.NewInt(0)
	}
	v := new(big.Int).SetBytes(b[:32])
	if b[0]&0x80 != 0 { // 最高位为 1 → 负数
		v.Sub(v, new(big.Int).Lsh(big.NewInt(1), 256))
	}
	return v
}

// parseV3Amounts 从 V3 Swap 事件 data 段提取两个有符号金额。
func parseV3Amounts(data []byte) (amount0, amount1 *big.Int, ok bool) {
	if len(data) < v3SwapDataWords*32 {
		return nil, nil, false
	}
	return decodeInt256(data[0:32]), decodeInt256(data[32:64]), true
}

// v3Direction 依据两个有符号金额判定买卖方向与绝对数量。
//
// 约定（Uniswap V3）：正数 = 进入池子（用户付出），负数 = 离开池子（用户获得）。
//   - amount0 > 0 且 amount1 < 0：用户卖出 token0、买入 token1
//   - amount0 < 0 且 amount1 > 0：用户买入 token0、卖出 token1
func v3Direction(amount0, amount1 *big.Int, token0, token1 string) (tokenIn, tokenOut string, amountIn, amountOut *big.Int, ok bool) {
	if amount0 == nil || amount1 == nil {
		return "", "", nil, nil, false
	}
	abs := func(v *big.Int) *big.Int { return new(big.Int).Abs(v) }

	switch {
	case amount0.Sign() > 0 && amount1.Sign() < 0:
		return token0, token1, abs(amount0), abs(amount1), true
	case amount0.Sign() < 0 && amount1.Sign() > 0:
		return token1, token0, abs(amount1), abs(amount0), true
	default:
		// 同号/零金额：不是有效的单边交换（可能是加/撤流动性或异常日志）
		return "", "", nil, nil, false
	}
}

// parseV3SwapLog 解析 V3 Swap 日志为统一事件（含方向与金额；USD 由 enrichUSD 补齐）。
func (a *Adapter) parseV3SwapLog(ctx context.Context, lg types.Log) (model.SwapEvent, bool) {
	ev := model.SwapEvent{
		Chain:  a.ChainID(),
		TxHash: lg.TxHash.Hex(),
		Pool:   lg.Address.Hex(),
		At:     time.Now().UTC(),
	}
	if len(lg.Topics) >= 3 {
		ev.Sender = common.BytesToAddress(lg.Topics[1].Bytes()).Hex()
		ev.Recipient = common.BytesToAddress(lg.Topics[2].Bytes()).Hex()
	} else if len(lg.Topics) >= 2 {
		ev.Sender = common.BytesToAddress(lg.Topics[1].Bytes()).Hex()
	}

	amount0, amount1, ok := parseV3Amounts(lg.Data)
	if !ok {
		return ev, false
	}

	t0, t1, err := a.evm.PoolTokens(ctx, lg.Address)
	if err != nil {
		// 无法确定 token0/token1 时仍返回事件（金额绝对值可参考），交由上层按需处理
		ev.AmountIn, ev.AmountOut = new(big.Int).Abs(amount0), new(big.Int).Abs(amount1)
		return ev, true
	}

	tokenIn, tokenOut, amountIn, amountOut, ok := v3Direction(amount0, amount1, t0, t1)
	if !ok {
		return ev, false
	}
	ev.TokenIn, ev.TokenOut = tokenIn, tokenOut
	ev.AmountIn, ev.AmountOut = amountIn, amountOut

	a.enrichUSD(ctx, &ev)
	return ev, true
}
