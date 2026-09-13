package solana

import (
	"math/big"
	"strings"
	"testing"
)

const (
	wsolMintTest  = "So11111111111111111111111111111111111111112"
	tokenMintTest = "TOKENMINTxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"
	userTest      = "USER111"
)

// buySample 是一笔"买入"交易：SOL 减少、代币增加。
const buySample = `{
  "meta": {
    "err": null,
    "preTokenBalances": [
      {"accountIndex": 1, "owner": "USER111", "mint": "So11111111111111111111111111111111111111112", "uiTokenAmount": {"amount": "1000000000", "decimals": 9}},
      {"accountIndex": 2, "owner": "USER111", "mint": "TOKENMINTxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx", "uiTokenAmount": {"amount": "0", "decimals": 6}}
    ],
    "postTokenBalances": [
      {"accountIndex": 1, "owner": "USER111", "mint": "So11111111111111111111111111111111111111112", "uiTokenAmount": {"amount": "900000000", "decimals": 9}},
      {"accountIndex": 2, "owner": "USER111", "mint": "TOKENMINTxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx", "uiTokenAmount": {"amount": "25000000", "decimals": 6}}
    ]
  },
  "transaction": {"signatures": ["SIG_BUY"], "message": {"accountKeys": [{"pubkey": "USER111", "signer": true}]}}
}`

// sellSample 是反向的"卖出"交易。
const sellSample = `{
  "meta": {
    "err": null,
    "preTokenBalances": [
      {"accountIndex": 1, "owner": "USER111", "mint": "So11111111111111111111111111111111111111112", "uiTokenAmount": {"amount": "900000000", "decimals": 9}},
      {"accountIndex": 2, "owner": "USER111", "mint": "TOKENMINTxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx", "uiTokenAmount": {"amount": "25000000", "decimals": 6}}
    ],
    "postTokenBalances": [
      {"accountIndex": 1, "owner": "USER111", "mint": "So11111111111111111111111111111111111111112", "uiTokenAmount": {"amount": "1000000000", "decimals": 9}},
      {"accountIndex": 2, "owner": "USER111", "mint": "TOKENMINTxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx", "uiTokenAmount": {"amount": "0", "decimals": 6}}
    ]
  },
  "transaction": {"signatures": ["SIG_SELL"], "message": {"accountKeys": [{"pubkey": "USER111", "signer": true}]}}
}`

func TestParseSwapBuy(t *testing.T) {
	parsed, err := ParseSwapFromTransaction([]byte(buySample), userTest)
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if parsed.Failed {
		t.Fatal("成功交易不应标记为失败")
	}
	if parsed.Signature != "SIG_BUY" {
		t.Fatalf("签名解析错误：%s", parsed.Signature)
	}
	if parsed.TokenIn != wsolMintTest || parsed.TokenOut != tokenMintTest {
		t.Fatalf("方向判定错误：in=%s out=%s", parsed.TokenIn, parsed.TokenOut)
	}
	if parsed.AmountIn.Int64() != 100000000 { // 0.1 SOL
		t.Fatalf("卖出金额应为 100000000 lamports，实际 %s", parsed.AmountIn)
	}
	if parsed.AmountOut.Int64() != 25000000 { // 25 token（6 位小数）
		t.Fatalf("买入金额应为 25000000，实际 %s", parsed.AmountOut)
	}
	if parsed.DecimalsIn != 9 || parsed.DecimalsOut != 6 {
		t.Fatalf("精度解析错误：%d/%d", parsed.DecimalsIn, parsed.DecimalsOut)
	}
}

func TestParseSwapSell(t *testing.T) {
	parsed, err := ParseSwapFromTransaction([]byte(sellSample), userTest)
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if parsed.TokenIn != tokenMintTest || parsed.TokenOut != wsolMintTest {
		t.Fatalf("卖出方向判定错误：in=%s out=%s", parsed.TokenIn, parsed.TokenOut)
	}
	if parsed.AmountIn.Int64() != 25000000 || parsed.AmountOut.Int64() != 100000000 {
		t.Fatalf("金额解析错误：in=%s out=%s", parsed.AmountIn, parsed.AmountOut)
	}
}

func TestParseSwapFailedTransaction(t *testing.T) {
	failed := strings.Replace(buySample, `"err": null`, `"err": {"InstructionError": [3, "Custom"]}`, 1)
	parsed, err := ParseSwapFromTransaction([]byte(failed), userTest)
	if err != nil {
		t.Fatalf("失败交易也应可解析（仅标记）：%v", err)
	}
	if !parsed.Failed {
		t.Fatal("应标记为失败交易")
	}
	if parsed.TokenIn != "" || parsed.TokenOut != "" {
		t.Fatal("失败交易不应给出方向")
	}
}

func TestParseSwapWithoutBalanceChange(t *testing.T) {
	noChange := strings.Replace(buySample, `{"accountIndex": 2, "owner": "USER111", "mint": "TOKENMINTxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx", "uiTokenAmount": {"amount": "25000000", "decimals": 6}}`, `{"accountIndex": 2, "owner": "USER111", "mint": "TOKENMINTxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx", "uiTokenAmount": {"amount": "0", "decimals": 6}}`, 1)
	if _, err := ParseSwapFromTransaction([]byte(noChange), userTest); err == nil {
		t.Fatal("无净变化时应报错（非 swap）")
	}
}

func TestParseSwapOwnerFallbackToFeePayer(t *testing.T) {
	// 指定 owner 在该交易中无变化 → 回退到 accountKeys[0]（fee payer）
	parsed, err := ParseSwapFromTransaction([]byte(buySample), "OTHER_OWNER")
	if err != nil {
		t.Fatalf("回退解析失败：%v", err)
	}
	if parsed.Owner != userTest {
		t.Fatalf("应回退到 fee payer：%s", parsed.Owner)
	}
	if parsed.TokenOut != tokenMintTest {
		t.Fatalf("回退后方向应正确：%s", parsed.TokenOut)
	}
}

func TestParseSwapMultiHopAggregation(t *testing.T) {
	// 多跳路由：同一账户在同一 mint 上出现两条余额条目（不同账户索引）→ 净变化聚合
	multiHop := `{
	  "meta": {
	    "err": null,
	    "preTokenBalances": [
	      {"accountIndex": 1, "owner": "USER111", "mint": "` + wsolMintTest + `", "uiTokenAmount": {"amount": "1000000", "decimals": 9}},
	      {"accountIndex": 2, "owner": "USER111", "mint": "` + wsolMintTest + `", "uiTokenAmount": {"amount": "500000", "decimals": 9}}
	    ],
	    "postTokenBalances": [
	      {"accountIndex": 1, "owner": "USER111", "mint": "` + wsolMintTest + `", "uiTokenAmount": {"amount": "200000", "decimals": 9}},
	      {"accountIndex": 3, "owner": "USER111", "mint": "` + tokenMintTest + `", "uiTokenAmount": {"amount": "7000", "decimals": 6}}
	    ]
	  },
	  "transaction": {"signatures": ["SIG_MULTI"], "message": {"accountKeys": [{"pubkey": "USER111", "signer": true}]}}
	}`
	parsed, err := ParseSwapFromTransaction([]byte(multiHop), "USER111")
	if err != nil {
		t.Fatalf("多跳解析失败：%v", err)
	}
	// 两笔 SOL 账户分别减少，聚合后仍为卖出 SOL（取最大净流出）
	if parsed.TokenIn != wsolMintTest || parsed.TokenOut != tokenMintTest {
		t.Fatalf("多跳方向判定错误：in=%s out=%s", parsed.TokenIn, parsed.TokenOut)
	}
	if parsed.AmountOut.Int64() != 7000 {
		t.Fatalf("买入金额应为 7000，实际 %s", parsed.AmountOut)
	}
}

func TestParseSwapInvalidInput(t *testing.T) {
	if _, err := ParseSwapFromTransaction(nil, userTest); err == nil {
		t.Fatal("空输入应报错")
	}
	if _, err := ParseSwapFromTransaction([]byte("{not-json}"), userTest); err == nil {
		t.Fatal("非法 JSON 应报错")
	}
	if _, err := ParseSwapFromTransaction([]byte(`{"transaction":{}}`), userTest); err == nil {
		t.Fatal("缺少 meta 应报错")
	}
}

func TestComputeTokenDeltas(t *testing.T) {
	pre := []tokenBalance{{AccountIndex: 1, Owner: "A", Mint: "M", UITokenAmount: struct {
		Amount   string `json:"amount"`
		Decimals uint8  `json:"decimals"`
	}{Amount: "100", Decimals: 6}}}
	post := []tokenBalance{{AccountIndex: 1, Owner: "A", Mint: "M", UITokenAmount: struct {
		Amount   string `json:"amount"`
		Decimals uint8  `json:"decimals"`
	}{Amount: "250", Decimals: 6}}}

	deltas := computeTokenDeltas(pre, post)
	if len(deltas) != 1 {
		t.Fatalf("应有 1 条净变化，实际 %d", len(deltas))
	}
	if deltas[0].Delta.Cmp(big.NewInt(150)) != 0 {
		t.Fatalf("净变化应为 +150，实际 %s", deltas[0].Delta)
	}
	if !hasDeltaFor(deltas, "A") || hasDeltaFor(deltas, "B") {
		t.Fatal("owner 判定错误")
	}

	// 零变化会被过滤
	if got := computeTokenDeltas(pre, pre); len(got) != 0 {
		t.Fatalf("零变化应被过滤，实际 %d", len(got))
	}
}
