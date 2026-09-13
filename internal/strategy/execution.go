package strategy

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"meme-bot/internal/config"
	"meme-bot/internal/metrics"
	"meme-bot/internal/model"
)

// AdapterProvider 提供链适配器（chain.Factory 满足该接口）。
type AdapterProvider interface {
	Get(chainID string) (model.ChainAdapter, error)
}

// Executor 执行引擎：驱动“构建 → 签名 → 广播（私有通道优先）→ 确认 → 落库 → 告警”闭环。
//
// 实现 model.ExecutorPort。dry_run 模式下不发送任何真实交易，只产出模拟订单与告警。
type Executor struct {
	adapters AdapterProvider
	signer   model.SwapSigner
	// solanaSigner 负责 Solana 交易签名（Ed25519，输出 base64）
	solanaSigner model.SolanaTxSigner
	risk         model.RiskPort
	positions    model.PositionStore
	alerts       model.AlertSink
	reg          *metrics.Registry
	log          *zap.Logger
	cfg          config.RiskConfig
	dryRun       bool
	// nativeTokens 链 -> 原生代币地址（用于卖出时构造 TokenIn/TokenOut）
	nativeTokens map[string]string
	// confirmTimeout 等待交易确认的超时
	confirmTimeout time.Duration
}

// NewExecutor 构建执行引擎。
func NewExecutor(
	adapters AdapterProvider,
	signer model.SwapSigner,
	solanaSigner model.SolanaTxSigner,
	risk model.RiskPort,
	positions model.PositionStore,
	alerts model.AlertSink,
	reg *metrics.Registry,
	log *zap.Logger,
	cfg config.RiskConfig,
	dryRun bool,
	nativeTokens map[string]string,
) *Executor {
	if log == nil {
		log = zap.NewNop()
	}
	return &Executor{
		adapters:       adapters,
		signer:         signer,
		solanaSigner:   solanaSigner,
		risk:           risk,
		positions:      positions,
		alerts:         alerts,
		reg:            reg,
		log:            log,
		cfg:            cfg,
		dryRun:         dryRun,
		nativeTokens:   nativeTokens,
		confirmTimeout: 120 * time.Second,
	}
}

var _ model.ExecutorPort = (*Executor)(nil)

// Execute 实现 model.ExecutorPort。
func (x *Executor) Execute(intent *model.TradeIntent) (*model.ExecutionResult, error) {
	if intent == nil {
		return nil, errors.New("executor: nil intent")
	}
	if intent.OrderID == "" {
		intent.OrderID = uuid.NewString()
	}
	if intent.Side == "" {
		intent.Side = model.SideBuy
	}

	order := &model.Order{
		ID:          intent.OrderID,
		Chain:       intent.Chain,
		TokenIn:     intent.TokenIn,
		TokenOut:    intent.TokenOut,
		Side:        intent.Side,
		AmountIn:    intent.AmountIn,
		Status:      model.OrderPending,
		SlippageBps: intent.SlippageBps,
		SignalID:    intent.SignalID,
		DryRun:      x.dryRun || intent.DryRun,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}

	adapter, err := x.adapters.Get(intent.Chain)
	if err != nil {
		return x.fail(order, "chain adapter unavailable: "+err.Error())
	}

	// 熔断/风控复核（执行前再确认一次，防止绕过）
	if paused, reason := x.risk.Paused(); paused {
		return x.fail(order, "系统熔断中："+reason)
	}

	if order.DryRun {
		order.Status = model.OrderFilled
		order.UpdatedAt = time.Now().UTC()
		if x.reg != nil {
			x.reg.Inc("memebot_orders_total", 1)
		}
		x.log.Info("dry-run order simulated",
			zap.String("chain", order.Chain), zap.String("token", order.TokenOut),
			zap.String("side", string(order.Side)))
		if x.alerts != nil {
			_ = x.alerts.Raise(context.Background(), &model.Alert{
				Level:   model.LevelInfo,
				Title:   "模拟下单（dry_run）",
				Message: fmt.Sprintf("方向 %s，代币 %s，金额 %s（最小单位）", order.Side, order.TokenOut, amountString(order.AmountIn)),
				Chain:   order.Chain,
				Token:   order.TokenOut,
			})
		}
		return &model.ExecutionResult{Order: order, Status: order.Status, DryRun: true}, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// 收款地址固定为签名地址（拒绝外部任意指定收款地址，避免资金被引导到第三方）。
	// 按链选择签名器：Solana 用 Ed25519 签名器，EVM 用 EIP-1559 签名器。
	recipient := ""
	if isSolanaChain(intent.Chain) {
		if x.solanaSigner == nil {
			return x.fail(order, "no solana signer configured（未配置 MEMEBOT_CHAINS_SOLANA_PRIVATE_KEY，拒绝构建交易）")
		}
		if addr, aerr := x.solanaSigner.Address(intent.Chain); aerr == nil {
			recipient = addr
		}
	} else if x.signer != nil {
		if addr, aerr := x.signer.Address(intent.Chain); aerr == nil {
			recipient = addr
		}
	}

	params := model.SwapParams{
		Chain:         intent.Chain,
		TokenIn:       intent.TokenIn,
		TokenOut:      intent.TokenOut,
		AmountIn:      intent.AmountIn,
		SlippageBps:   intent.SlippageBps,
		Recipient:     recipient,
		Deadline:      time.Now().Add(5 * time.Minute).Unix(),
		PreferPrivate: true,
	}

	// 1) 构建
	unsigned, err := adapter.BuildSwap(ctx, params)
	if err != nil {
		return x.fail(order, "build swap: "+err.Error())
	}
	order.Router = unsigned.Router

	// 2) Gas 估算
	if gas, err := adapter.EstimateGas(ctx, unsigned); err == nil && gas != nil {
		order.SlippageBps = params.SlippageBps
		if unsigned.GasLimit == 0 {
			unsigned.GasLimit = gas.GasLimit
		}
	}

	// 3) 签名：Solana 交易已组装成 wire-format（Serialized 非空），签 Ed25519；EVM 走 EIP-1559
	var rawTx string
	if len(unsigned.Serialized) > 0 {
		if x.solanaSigner == nil {
			return x.fail(order, "no solana signer configured（未配置 Solana 私钥，拒绝发送交易）")
		}
		signed, serr := x.solanaSigner.SignSolanaTx(ctx, intent.Chain, unsigned.Serialized)
		if serr != nil {
			return x.fail(order, "sign solana tx: "+serr.Error())
		}
		rawTx = signed
	} else {
		if x.signer == nil {
			return x.fail(order, "no signer configured（未配置签名器，拒绝发送交易）")
		}
		signed, serr := x.signer.SignSwap(ctx, intent.Chain, unsigned, params)
		if serr != nil {
			return x.fail(order, "sign swap: "+serr.Error())
		}
		rawTx = signed
	}

	// 4) 广播（适配器内部私有通道优先）
	order.Status = model.OrderSubmitted
	order.Attempts++
	order.UpdatedAt = time.Now().UTC()

	txHash, err := adapter.SendTransaction(ctx, rawTx)
	if err != nil {
		return x.fail(order, "send transaction: "+err.Error())
	}
	order.TxHash = txHash
	order.Status = model.OrderConfirming
	order.UpdatedAt = time.Now().UTC()

	if x.alerts != nil {
		_ = x.alerts.Raise(context.Background(), &model.Alert{
			Level:   model.LevelInfo,
			Title:   "交易已广播",
			Message: fmt.Sprintf("方向 %s，代币 %s，交易 %s", order.Side, order.TokenOut, txHash),
			Chain:   order.Chain,
			Token:   order.TokenOut,
		})
	}

	// 5) 等待确认
	receipt, err := adapter.WaitConfirmation(ctx, txHash, x.confirmTimeout)
	if err != nil {
		return x.fail(order, "confirmation: "+err.Error())
	}
	if receipt.Status != 1 {
		return x.fail(order, "交易执行失败（链上状态非成功）")
	}
	order.Status = model.OrderFilled
	order.UpdatedAt = time.Now().UTC()

	if x.reg != nil {
		x.reg.Inc("memebot_orders_total", 1)
	}

	// 6) 落库：开仓 / 平仓
	if err := x.settle(intent, order); err != nil {
		x.log.Error("settle position failed", zap.Error(err))
	}

	return &model.ExecutionResult{
		Order:        order,
		TxHash:       txHash,
		Status:       order.Status,
		FilledAmount: signedAmount(intent.AmountIn, intent.Side),
		DryRun:       false,
	}, nil
}

// MarkPrice 实现 model.ExecutorPort：更新标记价格并触发风控退出。
func (x *Executor) MarkPrice(p *model.Position, priceUSD, liquidityUSD float64) (*model.RiskDecision, error) {
	if p == nil {
		return nil, errors.New("executor: nil position")
	}

	if priceUSD > 0 {
		p.CurrentPriceUSD = priceUSD
		if priceUSD > p.PeakPriceUSD {
			p.PeakPriceUSD = priceUSD
		}
	}
	if liquidityUSD > 0 {
		p.CurrentLiquidityUSD = liquidityUSD
	}
	p.UpdatedAt = time.Now().UTC()

	decision, err := x.risk.CheckExit(p, p.CurrentPriceUSD, p.CurrentLiquidityUSD)
	if err != nil {
		return nil, err
	}
	if x.positions != nil {
		if err := x.positions.Update(p); err != nil {
			x.log.Warn("update position failed", zap.Error(err))
		}
	}
	if decision == nil || decision.Verdict != model.VerdictAllow {
		return decision, nil
	}

	// 触发退出：生成卖出意图并执行
	realized := (p.CurrentPriceUSD - p.EntryPriceUSD) * tokensAmount(p)
	intent := &model.TradeIntent{
		Chain:       p.Chain,
		TokenIn:     p.Token,
		TokenOut:    x.nativeToken(p.Chain),
		AmountIn:    p.Amount,
		Side:        model.SideSell,
		SlippageBps: x.cfg.MaxSlippageBps,
		PositionID:  p.ID,
		OrderID:     uuid.NewString(),
		DryRun:      x.dryRun || p.DryRun,
		Reason:      decision.Reason,
		CreatedAt:   time.Now().UTC(),
	}
	if _, err := x.Execute(intent); err != nil {
		return decision, err
	}

	if x.positions != nil {
		if err := x.positions.Close(p.ID, p.CurrentPriceUSD, realized); err != nil {
			x.log.Warn("close position failed", zap.Error(err))
		}
	}

	if x.alerts != nil {
		level := model.LevelWarning
		if realized < 0 {
			level = model.LevelCritical
		}
		_ = x.alerts.Raise(context.Background(), &model.Alert{
			Level:   level,
			Title:   "已执行退出",
			Message: fmt.Sprintf("%s；实现盈亏 %.2f USD", decision.Reason, realized),
			Chain:   p.Chain,
			Token:   p.Token,
		})
	}
	return decision, nil
}

// settle 交易成交后的持仓处理。
func (x *Executor) settle(intent *model.TradeIntent, order *model.Order) error {
	if x.positions == nil {
		return nil
	}
	switch intent.Side {
	case model.SideBuy:
		if intent.PositionID != "" {
			return nil // 加仓场景由调用方处理
		}
		p := &model.Position{
			ID:                  uuid.NewString(),
			Chain:               intent.Chain,
			Token:               intent.TokenOut,
			TokenSymbol:         order.TokenSymbol,
			Amount:              intent.AmountIn,
			EntryPriceUSD:       0, // 由行情源在首次 MarkPrice 时补齐
			CurrentPriceUSD:     0,
			PeakPriceUSD:        0,
			StopLossPct:         x.cfg.StopLossPct,
			TrailingStopPct:     x.cfg.TrailingStopPct,
			LiquidityAtEntryUSD: 0,
			Status:              model.PositionOpen,
			SignalID:            intent.SignalID,
			DryRun:              x.dryRun,
			OpenedAt:            time.Now().UTC(),
			UpdatedAt:           time.Now().UTC(),
		}
		if err := x.positions.Open(p); err != nil {
			return err
		}
		if x.reg != nil {
			x.reg.Inc("memebot_positions_open", 1)
		}
		return nil
	case model.SideSell:
		if intent.PositionID == "" {
			return nil
		}
		return x.positions.Close(intent.PositionID, 0, 0)
	default:
		return nil
	}
}

func (x *Executor) fail(order *model.Order, reason string) (*model.ExecutionResult, error) {
	order.Status = model.OrderFailed
	order.Error = reason
	order.UpdatedAt = time.Now().UTC()
	if x.reg != nil {
		x.reg.Inc("memebot_orders_failed_total", 1)
	}
	x.log.Error("order failed", zap.String("chain", order.Chain), zap.String("reason", reason))
	if x.alerts != nil {
		_ = x.alerts.Raise(context.Background(), &model.Alert{
			Level:   model.LevelWarning,
			Title:   "下单失败",
			Message: reason,
			Chain:   order.Chain,
			Token:   order.TokenOut,
		})
	}
	return &model.ExecutionResult{Order: order, Status: order.Status, Error: reason}, fmt.Errorf("executor: %s", reason)
}

// isSolanaChain 判断是否 Solana 链（决定签名与交易编码路径）。
func isSolanaChain(chain string) bool {
	return strings.EqualFold(strings.TrimSpace(chain), "solana")
}

func (x *Executor) nativeToken(chain string) string {
	if v, ok := x.nativeTokens[strings.ToLower(chain)]; ok && v != "" {
		return v
	}
	return "0xEeeeeEeeeEeEeeEeEeEeeEEEeeeeEeeeeeeeEEeE"
}

func amountString(a *big.Int) string {
	if a == nil {
		return "0"
	}
	return a.String()
}

func signedAmount(a *big.Int, side model.Side) *big.Int {
	if a == nil {
		return big.NewInt(0)
	}
	if side == model.SideSell {
		return new(big.Int).Neg(a)
	}
	return a
}

// tokensAmount 把最小单位数量换算为可读数量（用于盈亏估算）。
func tokensAmount(p *model.Position) float64 {
	if p == nil || p.Amount == nil {
		return 0
	}
	f := new(big.Float).SetInt(p.Amount)
	scale := new(big.Float).SetFloat64(1)
	for i := uint8(0); i < p.Decimals; i++ {
		scale.Mul(scale, big.NewFloat(10))
	}
	f.Quo(f, scale)
	out, _ := f.Float64()
	return out
}
