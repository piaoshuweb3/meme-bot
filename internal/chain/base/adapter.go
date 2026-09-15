// Package base 实现 Base（EVM）链适配器。
//
// 能力覆盖 model.ChainAdapter：
//   - 只读：余额 / 价格 / 流动性 / 安全报告 / 持仓集中度
//   - 交易：通过 1inch 聚合器构建 calldata；签名由 internal/chain/signer 负责
//   - 事件：eth_getLogs 轮询订阅 Swap 事件（V2 完整解析，V3 见注释）
//
// 外部数据（价格/流动性/安全）通过依赖注入获得，便于替换数据源与测试：
//
//	base.Configure(base.Deps{Market: provider.NewMarket(...), Security: provider.NewSecurity(...)})
package base

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"go.uber.org/zap"

	"meme-bot/internal/chain"
	"meme-bot/internal/chain/aggregator"
	"meme-bot/internal/chain/privatetx"
	"meme-bot/internal/config"
	"meme-bot/internal/model"
)

// nativeSentinel 是 1inch 用来表示原生代币（ETH/BNB）的哨兵地址。
const nativeSentinel = "0xEeeeeEeeeEeEeeEeEeEeeEEEeeeeEeeeeeeeEEeE"

// etherscanBaseURL 用于告警消息中的区块浏览器链接。
const etherscanBaseURL = "https://basescan.org"

// MarketSource 行情数据源（价格 / 流动性）。
type MarketSource interface {
	PriceUSD(ctx context.Context, chainID, token string) (price float64, source string, err error)
	LiquidityUSD(ctx context.Context, chainID, token string) (liquidityUSD, volume24hUSD float64, pool string, err error)
	// PairConsistency 多池价格一致性交叉校验：(是否一致, 依据, err)。
	PairConsistency(ctx context.Context, chainID, token string) (consistent bool, detail string, err error)
}

// SecuritySource 安全与持仓数据源。
type SecuritySource interface {
	Security(ctx context.Context, chainID, token string) (*model.SecurityReport, error)
	Concentration(ctx context.Context, chainID, token string, topN int) (*model.Concentration, error)
	// Sellability 实证可卖性：(是否可卖, 依据, err)；nil 表示未检测/证据不足。
	// 刻意使用基本类型而非 provider 的报表类型，避免 base 反向依赖 provider。
	Sellability(ctx context.Context, chainID, token, pool string) (*bool, string, error)
}

// Deps 适配器依赖（可为 nil，对应能力会明确报错而不是静默返回假数据）。
type Deps struct {
	Market     MarketSource
	Security   SecuritySource
	Aggregator aggregator.Client
	PrivateTx  *privatetx.Manager
}

var defaultDeps Deps

// Configure 注入全局依赖；需在 chain.NewFactory 之前调用。
func Configure(d Deps) { defaultDeps = d }

func init() {
	chain.Register("base", func(cfg *config.Config, ch config.Chain, log *zap.Logger) (model.ChainAdapter, error) {
		return NewWithDeps(cfg, ch, log, defaultDeps)
	})
}

// Adapter 是 Base/EVM 链适配器实现。
type Adapter struct {
	cfg     config.Chain
	log     *zap.Logger
	deps    Deps
	client  *ethclient.Client
	evm     *evmRPC
	chainID *big.Int

	mu   sync.RWMutex
	subs int
}

var _ model.ChainAdapter = (*Adapter)(nil)

// NewWithDeps 构建适配器（显式依赖注入版本，便于测试与自定义数据源）。
func NewWithDeps(cfg *config.Config, ch config.Chain, log *zap.Logger, deps Deps) (model.ChainAdapter, error) {
	if log == nil {
		log = zap.NewNop()
	}
	if len(ch.RPCURLs) == 0 {
		return nil, fmt.Errorf("base: no rpc url configured")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var (
		client *ethclient.Client
		errs   []string
	)
	for _, url := range ch.RPCURLs {
		c, err := ethclient.DialContext(ctx, url)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", maskURL(url), err))
			continue
		}
		client = c
		log.Info("base rpc connected", zap.String("url", maskURL(url)))
		break
	}
	if client == nil {
		return nil, fmt.Errorf("base: all rpc failed: %s", strings.Join(errs, "; "))
	}

	chainID := new(big.Int).SetInt64(ch.EVMChainID)
	if id, err := client.ChainID(ctx); err == nil && id != nil {
		chainID = id
	}

	a := &Adapter{
		cfg:     ch,
		log:     log,
		deps:    deps,
		client:  client,
		evm:     newEVMRPC(client),
		chainID: chainID,
	}
	return a, nil
}

// ChainID 实现 model.ChainAdapter。
func (a *Adapter) ChainID() string {
	if a.cfg.ChainID != "" {
		return a.cfg.ChainID
	}
	return "base"
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
		return etherscanBaseURL
	}
	if strings.HasPrefix(txOrAddr, "0x") && len(txOrAddr) == 66 {
		return etherscanBaseURL + "/tx/" + txOrAddr
	}
	return etherscanBaseURL + "/address/" + txOrAddr
}

// Balance 实现 model.ChainAdapter：原生代币或 ERC20 余额。
func (a *Adapter) Balance(ctx context.Context, address, token string) (*model.Balance, error) {
	if !common.IsHexAddress(address) {
		return nil, fmt.Errorf("base: invalid address %q", address)
	}
	owner := common.HexToAddress(address)

	// 原生代币
	if token == "" || strings.EqualFold(token, nativeSentinel) || strings.EqualFold(token, a.cfg.NativeToken.Address) {
		bal, err := a.client.BalanceAt(ctx, owner, nil)
		if err != nil {
			return nil, fmt.Errorf("base: balanceAt: %w", err)
		}
		return &model.Balance{Amount: bal, Decimals: a.cfg.NativeToken.Decimals}, nil
	}

	if !common.IsHexAddress(token) {
		return nil, fmt.Errorf("base: invalid token %q", token)
	}
	tokenAddr := common.HexToAddress(token)
	bal, err := a.evm.ERC20Balance(ctx, tokenAddr, owner)
	if err != nil {
		return nil, err
	}
	dec, err := a.evm.TokenDecimals(ctx, tokenAddr)
	if err != nil {
		dec = 18 // 退化为最常见精度，调用方可从行情源覆盖
	}
	return &model.Balance{Amount: bal, Decimals: dec}, nil
}

// BuildSwap 实现 model.ChainAdapter：通过聚合器构建 calldata。
func (a *Adapter) BuildSwap(ctx context.Context, params model.SwapParams) (*model.UnsignedTx, error) {
	if a.deps.Aggregator == nil {
		return nil, errors.New("base: aggregator not configured (需要设置 1inch API Key 或降级到原生 Router)")
	}
	req := aggregator.Request{
		ChainID:     a.chainID.Int64(),
		TokenIn:     normalizeToken(params.TokenIn, a.cfg.NativeToken.Address),
		TokenOut:    normalizeToken(params.TokenOut, a.cfg.NativeToken.Address),
		AmountIn:    params.AmountIn.String(),
		From:        params.Recipient,
		SlippageBps: params.SlippageBps,
		Deadline:    params.Deadline,
	}
	route, err := a.deps.Aggregator.Build(ctx, req)
	if err != nil {
		return nil, err
	}
	value := big.NewInt(0)
	if route.Value != "" && route.Value != "0" {
		if v, ok := new(big.Int).SetString(route.Value, 10); ok {
			value = v
		}
	}
	return &model.UnsignedTx{
		Chain:    a.ChainID(),
		To:       route.To,
		Data:     common.FromHex(route.Data),
		Value:    value,
		GasLimit: route.Gas,
		Router:   route.Router,
		Private:  params.PreferPrivate,
	}, nil
}

// EstimateGas 实现 model.ChainAdapter。
func (a *Adapter) EstimateGas(ctx context.Context, tx *model.UnsignedTx) (*model.GasEstimate, error) {
	if tx == nil {
		return nil, errors.New("base: nil tx")
	}
	to := common.HexToAddress(tx.To)
	msg := ethereumCallMsg{To: &to, Value: tx.Value, Data: tx.Data}

	limit := tx.GasLimit
	if limit == 0 {
		est, err := a.client.EstimateGas(ctx, msg)
		if err != nil {
			return nil, fmt.Errorf("base: estimateGas: %w", err)
		}
		limit = est + est/10 // +10% 余量
	}

	head, err := a.client.HeaderByNumber(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("base: header: %w", err)
	}
	baseFee := head.BaseFee
	tipCap := big.NewInt(1_000_000) // 0.001 gwei 下限
	if suggested, err := a.client.SuggestGasTipCap(ctx); err == nil && suggested != nil {
		tipCap = suggested
	}
	maxFee := new(big.Int).Mul(tipCap, big.NewInt(2))
	if baseFee != nil {
		maxFee = new(big.Int).Add(new(big.Int).Mul(baseFee, big.NewInt(2)), tipCap)
	}

	return &model.GasEstimate{
		GasLimit:       limit,
		MaxFeePerGas:   maxFee,
		MaxPriorityFee: tipCap,
	}, nil
}

// SendTransaction 实现 model.ChainAdapter：私有通道优先，公开通道降级。
//
// 注意：传入的是**已签名**原始交易（由 model.SwapSigner 产出）。
func (a *Adapter) SendTransaction(ctx context.Context, signedTx string) (string, error) {
	if strings.TrimSpace(signedTx) == "" {
		return "", errors.New("base: empty signed tx")
	}

	if a.deps.PrivateTx != nil && a.deps.PrivateTx.Available(a.ChainID()) {
		hash, sender, err := a.deps.PrivateTx.Send(ctx, a.ChainID(), signedTx, privatetx.Options{Encoding: "hex"})
		if err == nil {
			a.log.Info("tx sent via private channel",
				zap.String("channel", sender), zap.String("tx", hash))
			return hash, nil
		}
		// 私有通道失败：记录告警级日志，允许降级（风控层会收到通知）
		a.log.Warn("private channel failed, falling back to public rpc", zap.Error(err))
	}

	raw, err := decodeHexTx(signedTx)
	if err != nil {
		return "", err
	}
	var tx types.Transaction
	if err := tx.UnmarshalBinary(raw); err != nil {
		return "", fmt.Errorf("base: decode raw tx: %w", err)
	}
	if err := a.client.SendTransaction(ctx, &tx); err != nil {
		return "", fmt.Errorf("base: sendTransaction: %w", err)
	}
	return tx.Hash().Hex(), nil
}

// WaitConfirmation 实现 model.ChainAdapter：轮询等待回执。
func (a *Adapter) WaitConfirmation(ctx context.Context, txHash string, timeout time.Duration) (*model.TxReceipt, error) {
	if !common.IsHexAddress(txHash) && len(txHash) != 66 {
		return nil, fmt.Errorf("base: invalid tx hash %q", txHash)
	}
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	hash := common.HexToHash(txHash)
	for {
		receipt, err := a.client.TransactionReceipt(ctx, hash)
		if err == nil && receipt != nil {
			return &model.TxReceipt{
				Chain:       a.ChainID(),
				TxHash:      txHash,
				Status:      receipt.Status,
				BlockNumber: receipt.BlockNumber.Uint64(),
				GasUsed:     receipt.GasUsed,
				At:          time.Now().UTC(),
			}, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("base: confirmation timeout after %s", timeout)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

// TokenPrice 实现 model.ChainAdapter。
func (a *Adapter) TokenPrice(ctx context.Context, token string) (*model.Price, error) {
	if a.deps.Market == nil {
		return nil, errors.New("base: market source not configured")
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
		return nil, errors.New("base: market source not configured")
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
	if !common.IsHexAddress(pool) {
		return nil, fmt.Errorf("base: invalid pool %q", pool)
	}
	addr := common.HexToAddress(pool)
	ok, err := a.evm.HasCode(ctx, addr)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("base: %s is not a contract", pool)
	}
	t0, t1, err := a.evm.PoolTokens(ctx, addr)
	if err != nil {
		// V3 池子没有 token0()/token1()，此处不视为致命错误
		t0, t1 = "", ""
	}
	return &model.PoolInfo{
		Chain:   a.ChainID(),
		Address: addr.Hex(),
		Token0:  t0,
		Token1:  t1,
	}, nil
}

// TokenSecurity 实现 model.ChainAdapter。
func (a *Adapter) TokenSecurity(ctx context.Context, token string) (*model.SecurityReport, error) {
	if a.deps.Security == nil {
		return nil, errors.New("base: security source not configured")
	}
	rep, err := a.deps.Security.Security(ctx, a.ChainID(), token)
	if err != nil {
		return nil, err
	}
	a.attachSellability(ctx, token, rep)
	return rep, nil
}

// attachSellability 附加"实证可卖性"结论。
//
// 降级策略：探针失败只记 debug 日志，不影响主流程——静态报告本身已可用，
// 实证只是增强；但探针失败也绝不写成"可卖"（保持 nil = 未检测）。
func (a *Adapter) attachSellability(ctx context.Context, token string, rep *model.SecurityReport) {
	if rep == nil || a.deps.Security == nil {
		return
	}
	pool, err := a.resolvePoolAddress(ctx, token)
	if err != nil {
		a.log.Debug("sellability: 池子解析失败", zap.String("token", token), zap.Error(err))
		return
	}
	sellable, evidence, err := a.deps.Security.Sellability(ctx, a.ChainID(), token, pool.Hex())
	if err != nil {
		a.log.Debug("sellability: 探针失败（不影响静态报告）", zap.String("token", token), zap.Error(err))
		return
	}
	if sellable == nil {
		return
	}
	rep.Sellable = sellable
	rep.SellEvidence = evidence
	if !*sellable {
		// "有买入样本却零卖出"属强风险信号，直接计入 Risky 供策略层裁决
		rep.Risky = true
	}

	// 多源交叉校验：同代币多池价格严重偏离 → 数据可疑。
	// 只标记不硬拦（可能是异常池而非真风险），交策略层与人工复核。
	if a.deps.Market != nil {
		if consistent, detail, cerr := a.deps.Market.PairConsistency(ctx, a.ChainID(), token); cerr == nil && !consistent {
			rep.DataConflict = true
			rep.ConflictDetail = detail
			rep.Risky = true
		}
	}
}

// HolderConcentration 实现 model.ChainAdapter。
func (a *Adapter) HolderConcentration(ctx context.Context, token string, topN int) (*model.Concentration, error) {
	if a.deps.Security == nil {
		return nil, errors.New("base: security source not configured")
	}
	return a.deps.Security.Concentration(ctx, a.ChainID(), token, topN)
}

// SubscribeSwaps 实现 model.ChainAdapter：用 eth_getLogs 轮询实现（HTTP RPC 友好）。
//
// 说明：
//   - V2 风格 Swap 事件完整解析（方向、金额、sender、txHash）
//   - V3 风格事件只保留 txHash/sender（集中流动性的金额换算需要 sqrtPriceX96 数学，Stage 3 补全）
//   - token 参数按“池子地址”解释；如需按代币监听，请由上层传入其主池
func (a *Adapter) SubscribeSwaps(ctx context.Context, token string, handler func(model.SwapEvent)) (model.Unsubscribe, error) {
	if handler == nil {
		return nil, errors.New("base: nil handler")
	}
	pool, err := a.resolvePoolAddress(ctx, token)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})

	a.mu.Lock()
	a.subs++
	a.mu.Unlock()

	go func() {
		defer close(done)
		defer func() {
			a.mu.Lock()
			a.subs--
			a.mu.Unlock()
		}()

		ticker := time.NewTicker(6 * time.Second)
		defer ticker.Stop()

		var lastBlock uint64
		if head, err := a.client.HeaderByNumber(ctx, nil); err == nil {
			if head.Number != nil && head.Number.Uint64() > 3 {
				lastBlock = head.Number.Uint64() - 3
			}
		}

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			events, next, err := a.pollSwaps(ctx, pool, lastBlock)
			if err != nil {
				a.log.Warn("poll swaps failed", zap.String("pool", pool.Hex()), zap.Error(err))
				continue
			}
			for _, ev := range events {
				handler(ev)
			}
			lastBlock = next
		}
	}()

	return func() {
		cancel()
		<-done
	}, nil
}

// resolvePoolAddress 把“池子地址或代币地址”解析为可监听的池子地址。
func (a *Adapter) resolvePoolAddress(ctx context.Context, token string) (common.Address, error) {
	if !common.IsHexAddress(token) {
		return common.Address{}, fmt.Errorf("base: invalid pool/token %q", token)
	}
	addr := common.HexToAddress(token)
	if ok, err := a.evm.HasCode(ctx, addr); err == nil && ok {
		return addr, nil
	}
	if a.deps.Market == nil {
		return common.Address{}, fmt.Errorf("base: %s is not a contract and no market source to resolve pool", token)
	}
	_, _, pool, err := a.deps.Market.LiquidityUSD(ctx, a.ChainID(), token)
	if err != nil {
		return common.Address{}, fmt.Errorf("base: resolve pool for %s: %w", token, err)
	}
	if !common.IsHexAddress(pool) {
		return common.Address{}, fmt.Errorf("base: market source returned invalid pool %q", pool)
	}
	return common.HexToAddress(pool), nil
}

// pollSwaps 拉取 [from, latest] 区间的 Swap 日志并解析，返回事件与下一个起始区块。
func (a *Adapter) pollSwaps(ctx context.Context, pool common.Address, from uint64) ([]model.SwapEvent, uint64, error) {
	head, err := a.client.HeaderByNumber(ctx, nil)
	if err != nil {
		return nil, from, err
	}
	latest := head.Number.Uint64()
	if latest <= from {
		return nil, latest, nil
	}

	query := ethereum.FilterQuery{
		FromBlock: new(big.Int).SetUint64(from),
		ToBlock:   new(big.Int).SetUint64(latest),
		Addresses: []common.Address{pool},
		Topics: [][]common.Hash{{
			v2SwapTopic(),
			v3SwapTopic(),
		}},
	}
	logs, err := a.client.FilterLogs(ctx, query)
	if err != nil {
		return nil, from, err
	}

	events := make([]model.SwapEvent, 0, len(logs))
	for _, lg := range logs {
		if ev, ok := a.parseSwapLog(ctx, lg); ok {
			events = append(events, ev)
		}
	}
	return events, latest, nil
}

// parseSwapLog 解析单条 Swap 日志。
func (a *Adapter) parseSwapLog(ctx context.Context, lg types.Log) (model.SwapEvent, bool) {
	ev := model.SwapEvent{
		Chain:  a.ChainID(),
		TxHash: lg.TxHash.Hex(),
		Pool:   lg.Address.Hex(),
		At:     time.Now().UTC(),
	}
	if len(lg.Topics) >= 3 {
		ev.Sender = common.HexToHash(lg.Topics[2].Hex()).Hex()
		ev.Sender = common.BytesToAddress(lg.Topics[2].Bytes()).Hex()
	}

	switch lg.Topics[0] {
	case v2SwapTopic():
		if len(lg.Data) < 128 {
			return ev, true
		}
		amount0In := new(big.Int).SetBytes(lg.Data[0:32])
		amount1In := new(big.Int).SetBytes(lg.Data[32:64])
		amount0Out := new(big.Int).SetBytes(lg.Data[64:96])
		amount1Out := new(big.Int).SetBytes(lg.Data[96:128])

		t0, t1, err := a.evm.PoolTokens(ctx, lg.Address)
		if err != nil {
			// 无法确定方向时仍返回事件，交由上层按原始金额处理
			ev.AmountIn = maxBig(amount0In, amount1In)
			ev.AmountOut = maxBig(amount0Out, amount1Out)
			return ev, true
		}
		switch {
		case amount0In.Sign() > 0 && amount1Out.Sign() > 0:
			// 卖出 token0 换 token1
			ev.TokenIn, ev.TokenOut = t0, t1
			ev.AmountIn, ev.AmountOut = amount0In, amount1Out
		case amount1In.Sign() > 0 && amount0Out.Sign() > 0:
			// 卖出 token1 换 token0
			ev.TokenIn, ev.TokenOut = t1, t0
			ev.AmountIn, ev.AmountOut = amount1In, amount0Out
		default:
			return ev, false
		}
		a.enrichUSD(ctx, &ev)
		return ev, true
	case v3SwapTopic():
		// V3：解析有符号金额并判定方向（见 v3.go）
		return a.parseV3SwapLog(ctx, lg)
	default:
		return ev, false
	}
}

// enrichUSD 尽力补齐事件的美元金额与价格（失败不影响事件产出）。
func (a *Adapter) enrichUSD(ctx context.Context, ev *model.SwapEvent) {
	if a.deps.Market == nil || ev.TokenIn == "" {
		return
	}
	priceIn, _, err := a.deps.Market.PriceUSD(ctx, a.ChainID(), ev.TokenIn)
	if err != nil || priceIn <= 0 {
		return
	}
	dec, err := a.evm.TokenDecimals(ctx, common.HexToAddress(ev.TokenIn))
	if err != nil {
		dec = 18
	}
	ev.AmountUSD = toFloat(ev.AmountIn, dec) * priceIn
	if ev.AmountOut != nil && ev.AmountOut.Sign() > 0 {
		ev.PriceUSD = ev.AmountUSD / toFloat(ev.AmountOut, 18)
	}
}

func normalizeToken(token, native string) string {
	if strings.TrimSpace(token) == "" {
		return nativeSentinel
	}
	if strings.EqualFold(token, native) {
		return nativeSentinel
	}
	return token
}

func maxBig(a, b *big.Int) *big.Int {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if a.Cmp(b) >= 0 {
		return a
	}
	return b
}

// toFloat 把最小单位金额按精度换算为人类可读浮点数（仅用于展示与估算）。
func toFloat(amount *big.Int, decimals uint8) float64 {
	if amount == nil {
		return 0
	}
	f := new(big.Float).SetInt(amount)
	scale := new(big.Float).SetFloat64(1)
	for i := uint8(0); i < decimals; i++ {
		scale.Mul(scale, big.NewFloat(10))
	}
	f.Quo(f, scale)
	out, _ := f.Float64()
	return out
}
