// Package solana 实现 Solana 链适配器。
//
// 设计选择：直接用标准库走 JSON-RPC（不引入 solana-go），
// 原因：需要的方法很少（余额/广播/确认/优先级费），自实现更可控、依赖更少。
//
// 能力边界（诚实标注）：
//   - 只读：余额（SOL / SPL）、价格、流动性、安全、持仓集中度
//   - 交易：Jupiter 构建交易（返回未签名/部分签名交易的 base64）；签名由外部钱包或
//     Stage 3 的本地 ed25519 签名器完成（Solana 交易反序列化 + 签名替换复杂度较高）
//   - 事件：基于 getSignaturesForAddress 的轮询，产出 txHash/时间；金额级解析需要
//     Geyser/专用解析（Stage 3）
package solana

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"go.uber.org/zap"

	"meme-bot/internal/chain"
	"meme-bot/internal/chain/aggregator"
	"meme-bot/internal/chain/privatetx"
	"meme-bot/internal/config"
	"meme-bot/internal/model"
)

const (
	// tokenProgramID 是 SPL Token 程序地址。
	tokenProgramID = "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA"
	// wrappedSOL 是 SOL 的包装代币 mint。
	wrappedSOL = "So11111111111111111111111111111111111111112"
)

// MarketSource 行情数据源。
type MarketSource interface {
	PriceUSD(ctx context.Context, chainID, token string) (price float64, source string, err error)
	LiquidityUSD(ctx context.Context, chainID, token string) (liquidityUSD, volume24hUSD float64, pool string, err error)
}

// SecuritySource 安全与持仓数据源。
type SecuritySource interface {
	Security(ctx context.Context, chainID, token string) (*model.SecurityReport, error)
	Concentration(ctx context.Context, chainID, token string, topN int) (*model.Concentration, error)
}

// Deps 适配器依赖。
type Deps struct {
	Market    MarketSource
	Security  SecuritySource
	Jupiter   *aggregator.Jupiter
	PrivateTx *privatetx.Manager
	// PriorityFeeLamports 动态优先费上限（micro-lamports 换算前的 lamports 预算）
	PriorityFeeLamports int
}

var defaultDeps Deps

// Configure 注入全局依赖；需在 chain.NewFactory 之前调用。
func Configure(d Deps) { defaultDeps = d }

func init() {
	chain.Register("solana", func(cfg *config.Config, ch config.Chain, log *zap.Logger) (model.ChainAdapter, error) {
		return NewWithDeps(cfg, ch, log, defaultDeps)
	})
}

// Adapter 是 Solana 链适配器实现。
type Adapter struct {
	cfg  config.Chain
	log  *zap.Logger
	deps Deps
	rpc  *chain.RPCPool
}

var _ model.ChainAdapter = (*Adapter)(nil)

// NewWithDeps 构建 Solana 适配器。
func NewWithDeps(cfg *config.Config, ch config.Chain, log *zap.Logger, deps Deps) (model.ChainAdapter, error) {
	if log == nil {
		log = zap.NewNop()
	}
	if len(ch.RPCURLs) == 0 {
		return nil, fmt.Errorf("solana: no rpc url configured")
	}
	return &Adapter{
		cfg:  ch,
		log:  log,
		deps: deps,
		rpc:  chain.NewRPCPool("solana", ch.RPCURLs),
	}, nil
}

// ChainID 实现 model.ChainAdapter。
func (a *Adapter) ChainID() string {
	if a.cfg.ChainID != "" {
		return a.cfg.ChainID
	}
	return "solana"
}

// NativeToken 实现 model.ChainAdapter。
func (a *Adapter) NativeToken() model.Token {
	return model.Token{
		Chain:    a.ChainID(),
		Address:  a.cfg.NativeToken.Address,
		Symbol:   a.cfg.NativeToken.Symbol,
		Decimals: a.cfg.NativeToken.Decimals,
	}
}

// ExplorerURL 区块浏览器链接（告警上下文用）。
func (a *Adapter) ExplorerURL(txOrAddr string) string {
	if txOrAddr == "" {
		return "https://solscan.io"
	}
	if len(txOrAddr) > 40 {
		return "https://solscan.io/tx/" + txOrAddr
	}
	return "https://solscan.io/account/" + txOrAddr
}

// Balance 实现 model.ChainAdapter：SOL（lamports）或 SPL 代币余额。
func (a *Adapter) Balance(ctx context.Context, address, token string) (*model.Balance, error) {
	address = strings.TrimSpace(address)
	if address == "" {
		return nil, errors.New("solana: empty address")
	}

	if token == "" || token == wrappedSOL || token == a.cfg.NativeToken.Address {
		var out struct {
			Value uint64 `json:"value"`
		}
		if err := a.rpc.Call(ctx, "getBalance", []any{address, map[string]string{"commitment": "confirmed"}}, &out); err != nil {
			return nil, fmt.Errorf("solana: getBalance: %w", err)
		}
		return &model.Balance{Amount: new(big.Int).SetUint64(out.Value), Decimals: 9}, nil
	}

	// SPL 代币
	var accs struct {
		Value []struct {
			Account struct {
				Data struct {
					Parsed struct {
						Info struct {
							Mint        string `json:"mint"`
							TokenAmount struct {
								Amount   string `json:"amount"`
								Decimals uint8  `json:"decimals"`
							} `json:"tokenAmount"`
						} `json:"info"`
					} `json:"parsed"`
				} `json:"data"`
			} `json:"account"`
		} `json:"value"`
	}
	params := []any{
		address,
		map[string]string{"programId": tokenProgramID},
		map[string]any{"encoding": "jsonParsed"},
	}
	if err := a.rpc.Call(ctx, "getTokenAccountsByOwner", params, &accs); err != nil {
		return nil, fmt.Errorf("solana: getTokenAccountsByOwner: %w", err)
	}

	total := big.NewInt(0)
	var decimals uint8 = 9
	for _, acc := range accs.Value {
		info := acc.Account.Data.Parsed.Info
		if !strings.EqualFold(info.Mint, token) {
			continue
		}
		amt, ok := new(big.Int).SetString(info.TokenAmount.Amount, 10)
		if !ok {
			continue
		}
		total.Add(total, amt)
		decimals = info.TokenAmount.Decimals
	}
	return &model.Balance{Amount: total, Decimals: decimals}, nil
}

// TokenPrice 实现 model.ChainAdapter。
func (a *Adapter) TokenPrice(ctx context.Context, token string) (*model.Price, error) {
	if a.deps.Market == nil {
		return nil, errors.New("solana: market source not configured")
	}
	price, source, err := a.deps.Market.PriceUSD(ctx, a.ChainID(), token)
	if err != nil {
		return nil, err
	}
	return &model.Price{Chain: a.ChainID(), Token: token, USD: price, At: time.Now().UTC(), Source: source}, nil
}

// Liquidity 实现 model.ChainAdapter。
func (a *Adapter) Liquidity(ctx context.Context, token string) (*model.LiquidityInfo, error) {
	if a.deps.Market == nil {
		return nil, errors.New("solana: market source not configured")
	}
	liq, vol, pool, err := a.deps.Market.LiquidityUSD(ctx, a.ChainID(), token)
	if err != nil {
		return nil, err
	}
	return &model.LiquidityInfo{
		Chain:        a.ChainID(),
		Token:        token,
		PoolAddress:  pool,
		LiquidityUSD: liq,
		Volume24hUSD: vol,
		UpdatedAt:    time.Now().UTC(),
	}, nil
}

// PoolInfo 实现 model.ChainAdapter。
func (a *Adapter) PoolInfo(ctx context.Context, pool string) (*model.PoolInfo, error) {
	var out struct {
		Value *struct {
			Owner      string `json:"owner"`
			Lamports   uint64 `json:"lamports"`
			Executable bool   `json:"executable"`
		} `json:"value"`
	}
	if err := a.rpc.Call(ctx, "getAccountInfo", []any{pool, map[string]string{"encoding": "base64"}}, &out); err != nil {
		return nil, fmt.Errorf("solana: getAccountInfo: %w", err)
	}
	if out.Value == nil {
		return nil, fmt.Errorf("solana: account %s not found", pool)
	}
	return &model.PoolInfo{Chain: a.ChainID(), Address: pool}, nil
}

// BuildSwap 实现 model.ChainAdapter：通过 Jupiter 构建交易（返回 base64 序列化交易）。
//
// 若系统未配置签名器，交易保持未签名状态，需由外部钱包签名后再广播。
func (a *Adapter) BuildSwap(ctx context.Context, params model.SwapParams) (*model.UnsignedTx, error) {
	if a.deps.Jupiter == nil {
		return nil, errors.New("solana: jupiter aggregator not configured")
	}
	in := params.TokenIn
	if strings.TrimSpace(in) == "" {
		in = wrappedSOL
	}
	out := params.TokenOut
	if strings.TrimSpace(out) == "" {
		return nil, errors.New("solana: TokenOut required")
	}

	// 报价的预期输出（expectedOut）在执行层做滑点校验，此处仅用于构建交易
	quoteRaw, expectedOut, err := a.deps.Jupiter.QuoteRaw(ctx, in, out, params.AmountIn.String(), params.SlippageBps)
	if err != nil {
		return nil, err
	}
	if expectedOut == "" {
		return nil, fmt.Errorf("solana: jupiter 未返回预期输出金额")
	}

	fee := a.deps.PriorityFeeLamports
	if fee == 0 {
		fee = 2000 // 默认 2000 lamports 优先费，可由配置覆盖
	}
	txB64, err := a.deps.Jupiter.BuildTx(ctx, quoteRaw, params.Recipient, fee)
	if err != nil {
		return nil, err
	}
	raw, err := base64.StdEncoding.DecodeString(txB64)
	if err != nil {
		return nil, fmt.Errorf("solana: decode jupiter tx: %w", err)
	}

	return &model.UnsignedTx{
		Chain:      a.ChainID(),
		To:         params.Recipient,
		Serialized: raw,
		Value:      big.NewInt(0),
		Router:     "jupiter",
		Private:    params.PreferPrivate,
	}, nil
}

// EstimateGas 实现 model.ChainAdapter：返回动态优先费（micro-lamports/CU）。
func (a *Adapter) EstimateGas(ctx context.Context, tx *model.UnsignedTx) (*model.GasEstimate, error) {
	var fees []struct {
		Slot              uint64 `json:"slot"`
		PrioritizationFee uint64 `json:"prioritizationFee"`
	}
	params := []any{[]string{}}
	if err := a.rpc.Call(ctx, "getRecentPrioritizationFees", params, &fees); err != nil {
		return nil, fmt.Errorf("solana: getRecentPrioritizationFees: %w", err)
	}

	var sum, n uint64
	for _, f := range fees {
		sum += f.PrioritizationFee
		n++
	}
	avg := uint64(0)
	if n > 0 {
		avg = sum / n
	}
	// 上限保护：避免优先费失控
	feeCap := a.cfg.MaxPriorityFeeMicroLamports
	if feeCap == 0 {
		feeCap = 200000
	}
	if avg > feeCap {
		avg = feeCap
	}
	return &model.GasEstimate{GasLimit: 200000, ComputeUnitPrice: avg}, nil
}

// SendTransaction 实现 model.ChainAdapter：私有通道（Jito）优先，公开 RPC 降级。
//
// 注意：入参是**已签名**交易的 base64 字符串。
func (a *Adapter) SendTransaction(ctx context.Context, signedTx string) (string, error) {
	signedTx = strings.TrimSpace(signedTx)
	if signedTx == "" {
		return "", errors.New("solana: empty signed tx")
	}

	if a.deps.PrivateTx != nil && a.deps.PrivateTx.Available(a.ChainID()) {
		bundleID, sender, err := a.deps.PrivateTx.Send(ctx, a.ChainID(), signedTx, privatetx.Options{Encoding: "base64"})
		if err == nil {
			a.log.Info("tx submitted via private channel",
				zap.String("channel", sender), zap.String("bundle", bundleID))
			return bundleID, nil
		}
		a.log.Warn("jito bundle failed, falling back to public rpc", zap.Error(err))
	}

	var sig string
	params := []any{
		signedTx,
		map[string]any{"encoding": "base64", "skipPreflight": false, "maxRetries": 3},
	}
	if err := a.rpc.Call(ctx, "sendTransaction", params, &sig); err != nil {
		return "", fmt.Errorf("solana: sendTransaction: %w", err)
	}
	return sig, nil
}

// WaitConfirmation 实现 model.ChainAdapter：轮询 getSignatureStatuses。
func (a *Adapter) WaitConfirmation(ctx context.Context, txHash string, timeout time.Duration) (*model.TxReceipt, error) {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		var out struct {
			Value []*struct {
				Slot               uint64      `json:"slot"`
				Err                interface{} `json:"err"`
				ConfirmationStatus string      `json:"confirmationStatus"`
			} `json:"value"`
		}
		params := []any{[]string{txHash}, map[string]any{"searchTransactionHistory": true}}
		if err := a.rpc.Call(ctx, "getSignatureStatuses", params, &out); err == nil && len(out.Value) > 0 && out.Value[0] != nil {
			st := out.Value[0]
			if st.ConfirmationStatus == "confirmed" || st.ConfirmationStatus == "finalized" {
				status := uint64(1)
				if st.Err != nil {
					status = 0
				}
				return &model.TxReceipt{
					Chain:       a.ChainID(),
					TxHash:      txHash,
					Status:      status,
					BlockNumber: st.Slot,
					At:          time.Now().UTC(),
				}, nil
			}
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("solana: confirmation timeout after %s", timeout)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

// TokenSecurity 实现 model.ChainAdapter。
func (a *Adapter) TokenSecurity(ctx context.Context, token string) (*model.SecurityReport, error) {
	if a.deps.Security == nil {
		return nil, errors.New("solana: security source not configured")
	}
	return a.deps.Security.Security(ctx, a.ChainID(), token)
}

// HolderConcentration 实现 model.ChainAdapter。
func (a *Adapter) HolderConcentration(ctx context.Context, token string, topN int) (*model.Concentration, error) {
	if a.deps.Security == nil {
		return nil, errors.New("solana: security source not configured")
	}
	return a.deps.Security.Concentration(ctx, a.ChainID(), token, topN)
}

// SubscribeSwaps 实现 model.ChainAdapter：轮询 getSignaturesForAddress。
//
// 说明：这里产出的是“账户活动”级别的事件（txHash/时间），用于快速发现新动作；
// 精确的 Swap 金额与方向需要交易解析（Geyser 或 Jupiter/DexScreener 兜底），Stage 3 补全。
func (a *Adapter) SubscribeSwaps(ctx context.Context, token string, handler func(model.SwapEvent)) (model.Unsubscribe, error) {
	if handler == nil {
		return nil, errors.New("solana: nil handler")
	}
	account := strings.TrimSpace(token)
	if account == "" {
		return nil, errors.New("solana: empty account/mint")
	}

	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})

	go func() {
		defer close(done)
		ticker := time.NewTicker(8 * time.Second)
		defer ticker.Stop()

		seen := make(map[string]struct{}, 256)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			sigs, err := a.recentSignatures(ctx, account, 20)
			if err != nil {
				a.log.Warn("poll signatures failed", zap.String("account", account), zap.Error(err))
				continue
			}
			for _, s := range sigs {
				if _, ok := seen[s.Signature]; ok {
					continue
				}
				seen[s.Signature] = struct{}{}
				at := time.Now().UTC()
				if s.BlockTime != nil {
					at = time.Unix(*s.BlockTime, 0).UTC()
				}
				handler(model.SwapEvent{
					Chain:  a.ChainID(),
					TxHash: s.Signature,
					Pool:   account,
					Sender: account,
					At:     at,
				})
			}
			if len(seen) > 4096 {
				seen = make(map[string]struct{}, 256)
			}
		}
	}()

	return func() {
		cancel()
		<-done
	}, nil
}

type signatureInfo struct {
	Signature string `json:"signature"`
	Slot      uint64 `json:"slot"`
	BlockTime *int64 `json:"blockTime"`
	Err       any    `json:"err"`
}

func (a *Adapter) recentSignatures(ctx context.Context, account string, limit int) ([]signatureInfo, error) {
	var out []signatureInfo
	params := []any{account, map[string]any{"limit": limit}}
	if err := a.rpc.Call(ctx, "getSignaturesForAddress", params, &out); err != nil {
		return nil, err
	}
	return out, nil
}
