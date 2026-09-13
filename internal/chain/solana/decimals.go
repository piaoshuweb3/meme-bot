package solana

import (
	"context"
	"fmt"
	"strings"
)

// TokenDecimals 读取 SPL 代币的精度（通过 getAccountInfo + jsonParsed 解析 mint）。
func (a *Adapter) TokenDecimals(ctx context.Context, token string) (uint8, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return 0, fmt.Errorf("solana: empty mint")
	}
	if token == wrappedSOL || token == a.cfg.NativeToken.Address {
		return 9, nil
	}

	var out struct {
		Value *struct {
			Data struct {
				Parsed struct {
					Info struct {
						Decimals uint8 `json:"decimals"`
					} `json:"info"`
				} `json:"parsed"`
			} `json:"data"`
		} `json:"value"`
	}
	params := []any{token, map[string]any{"encoding": "jsonParsed"}}
	if err := a.rpc.Call(ctx, "getAccountInfo", params, &out); err != nil {
		return 0, fmt.Errorf("solana: getAccountInfo(%s): %w", token, err)
	}
	if out.Value == nil {
		return 0, fmt.Errorf("solana: mint %s not found", token)
	}
	return out.Value.Data.Parsed.Info.Decimals, nil
}
