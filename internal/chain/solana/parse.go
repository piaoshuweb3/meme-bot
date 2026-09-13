package solana

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
)

// tokenBalance 是 jsonParsed 交易里的余额条目。
type tokenBalance struct {
	AccountIndex  int    `json:"accountIndex"`
	Owner         string `json:"owner"`
	Mint          string `json:"mint"`
	UITokenAmount struct {
		Amount   string `json:"amount"`
		Decimals uint8  `json:"decimals"`
	} `json:"uiTokenAmount"`
}

// parsedTransaction 是 getTransaction(encoding=jsonParsed) 响应的最小子集。
type parsedTransaction struct {
	Meta *struct {
		Err               *json.RawMessage `json:"err"`
		PreTokenBalances  []tokenBalance   `json:"preTokenBalances"`
		PostTokenBalances []tokenBalance   `json:"postTokenBalances"`
	} `json:"meta"`
	Transaction *struct {
		Signatures []string `json:"signatures"`
		Message    struct {
			AccountKeys []struct {
				Pubkey string `json:"pubkey"`
				Signer bool   `json:"signer"`
			} `json:"accountKeys"`
		} `json:"message"`
	} `json:"transaction"`
}

// TokenDelta 某账户在某 mint 上的净变化（最小单位，卖出为负）。
type TokenDelta struct {
	Owner    string
	Mint     string
	Decimals uint8
	Delta    *big.Int
}

// ParsedSwap 金额级 Swap 解析结果。
type ParsedSwap struct {
	Owner       string
	Signature   string
	Failed      bool
	TokenIn     string
	TokenOut    string
	AmountIn    *big.Int
	AmountOut   *big.Int
	DecimalsIn  uint8
	DecimalsOut uint8
}

// ParseSwapFromTransaction 从 jsonParsed 交易中提取金额级 Swap 信息。
//
// 为什么用「余额差值法」而不是解析指令：
//   - SPL Token / Token-2022 / 各聚合器（Jupiter 多跳、共享账户）的指令布局各不相同，
//     逐指令解析既脆弱又容易漏；
//   - preTokenBalances / postTokenBalances 由 RPC 直接给出，**净变化天然聚合多跳路由**，
//     是金额级事实的唯一可靠来源。
//
// 方向判定：目标账户净增的代币 = 买入（TokenOut），净减的代币 = 卖出（TokenIn）。
func ParseSwapFromTransaction(raw []byte, owner string) (*ParsedSwap, error) {
	if len(raw) == 0 {
		return nil, errors.New("solana: 空交易数据")
	}
	var tx parsedTransaction
	if err := json.Unmarshal(raw, &tx); err != nil {
		return nil, fmt.Errorf("solana: 交易 JSON 解析失败: %w", err)
	}
	if tx.Meta == nil {
		return nil, errors.New("solana: 交易缺少 meta 字段（未使用 jsonParsed 编码？）")
	}

	out := &ParsedSwap{Owner: owner}
	if tx.Transaction != nil && len(tx.Transaction.Signatures) > 0 {
		out.Signature = tx.Transaction.Signatures[0]
	}
	if tx.Meta.Err != nil {
		out.Failed = true // 失败交易：标记后返回，不做方向判定
		return out, nil
	}

	deltas := computeTokenDeltas(tx.Meta.PreTokenBalances, tx.Meta.PostTokenBalances)
	if len(deltas) == 0 {
		return nil, errors.New("solana: 交易中没有代币余额变化（可能不是 swap）")
	}

	// 优先用指定 owner；其无变化时回退到 fee payer（accountKeys[0]）
	target := owner
	if target == "" || !hasDeltaFor(deltas, target) {
		if tx.Transaction != nil && len(tx.Transaction.Message.AccountKeys) > 0 {
			target = tx.Transaction.Message.AccountKeys[0].Pubkey
		}
	}
	if target != "" {
		out.Owner = target
	}

	var bought, sold *TokenDelta
	for i := range deltas {
		d := deltas[i]
		if target != "" && !strings.EqualFold(d.Owner, target) {
			continue
		}
		switch {
		case d.Delta.Sign() > 0:
			if bought == nil || d.Delta.Cmp(bought.Delta) > 0 {
				bought = &deltas[i]
			}
		case d.Delta.Sign() < 0:
			if sold == nil || d.Delta.Cmp(sold.Delta) < 0 {
				sold = &deltas[i]
			}
		}
	}
	if bought == nil || sold == nil {
		return nil, fmt.Errorf("solana: 账户 %s 未出现代币净流入/流出（非 swap 或数据不完整）", target)
	}

	out.TokenIn = sold.Mint
	out.AmountIn = new(big.Int).Abs(sold.Delta)
	out.DecimalsIn = sold.Decimals
	out.TokenOut = bought.Mint
	out.AmountOut = bought.Delta
	out.DecimalsOut = bought.Decimals
	return out, nil
}

// computeTokenDeltas 计算 pre → post 的净变化（按 accountIndex|mint 配对）。
func computeTokenDeltas(pre, post []tokenBalance) []TokenDelta {
	type key struct {
		index int
		mint  string
	}
	index := make(map[key]*TokenDelta, len(pre)+len(post))

	upsert := func(b tokenBalance, sign int) {
		k := key{index: b.AccountIndex, mint: b.Mint}
		d, ok := index[k]
		if !ok {
			d = &TokenDelta{Owner: b.Owner, Mint: b.Mint, Decimals: b.UITokenAmount.Decimals, Delta: big.NewInt(0)}
			index[k] = d
		}
		if d.Owner == "" {
			d.Owner = b.Owner
		}
		if b.UITokenAmount.Decimals > 0 {
			d.Decimals = b.UITokenAmount.Decimals
		}
		v, ok := new(big.Int).SetString(strings.TrimSpace(b.UITokenAmount.Amount), 10)
		if !ok {
			return
		}
		if sign > 0 {
			d.Delta.Add(d.Delta, v)
		} else {
			d.Delta.Sub(d.Delta, v)
		}
	}

	for _, b := range pre {
		upsert(b, -1)
	}
	for _, b := range post {
		upsert(b, +1)
	}

	out := make([]TokenDelta, 0, len(index))
	for _, d := range index {
		if d.Delta.Sign() == 0 {
			continue
		}
		out = append(out, *d)
	}
	return out
}

// hasDeltaFor 判断某 owner 是否存在净变化。
func hasDeltaFor(deltas []TokenDelta, owner string) bool {
	for _, d := range deltas {
		if strings.EqualFold(d.Owner, owner) {
			return true
		}
	}
	return false
}
