package payment

import (
	"math/big"
	"strings"
)

// parseAmount 解析最小单位十进制金额。
func parseAmount(s string) (*big.Int, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, false
	}
	v, ok := new(big.Int).SetString(s, 10)
	if !ok || v.Sign() < 0 {
		return nil, false
	}
	return v, true
}
