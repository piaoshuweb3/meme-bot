package base

import (
	"context"
	"fmt"
	"strings"

	"github.com/ethereum/go-ethereum/common"
)

// TokenDecimals 读取 ERC20 精度。
//
// 为什么暴露它：上层需要把「USD 金额」换算成「最小单位数量」才能构建交易，
// 换算必须使用链上真实 decimals（不同代币差异极大，用 18 兜底会算错数量级）。
func (a *Adapter) TokenDecimals(ctx context.Context, token string) (uint8, error) {
	token = strings.TrimSpace(token)
	if !common.IsHexAddress(token) {
		return 0, fmt.Errorf("base: invalid token %q", token)
	}
	return a.evm.TokenDecimals(ctx, common.HexToAddress(token))
}
