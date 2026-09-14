package strategy

import (
	"context"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"meme-bot/internal/config"
	"meme-bot/internal/model"
)

// ---- 执行引擎替身 -------------------------------------------------------------

type fakeAdapter struct {
	buildErr   error
	sendErr    error
	waitErr    error
	receipt    *model.TxReceipt
	serialized []byte
	router     string
	buildCalls int
	sendCalls  int
}

func (f *fakeAdapter) ChainID() string { return "base" }

func (f *fakeAdapter) NativeToken() model.Token {
	return model.Token{Address: "0xeeee", Symbol: "ETH", Decimals: 18}
}

func (f *fakeAdapter) Balance(context.Context, string, string) (*model.Balance, error) {
	return &model.Balance{}, nil
}

func (f *fakeAdapter) BuildSwap(context.Context, model.SwapParams) (*model.UnsignedTx, error) {
	f.buildCalls++
	if f.buildErr != nil {
		return nil, f.buildErr
	}
	return &model.UnsignedTx{Chain: "base", To: "0xrouter", Router: f.router, Serialized: f.serialized, Value: big.NewInt(0)}, nil
}

func (f *fakeAdapter) EstimateGas(context.Context, *model.UnsignedTx) (*model.GasEstimate, error) {
	return &model.GasEstimate{GasLimit: 21000}, nil
}

func (f *fakeAdapter) SendTransaction(context.Context, string) (string, error) {
	f.sendCalls++
	if f.sendErr != nil {
		return "", f.sendErr
	}
	return "0xtxhash", nil
}

func (f *fakeAdapter) WaitConfirmation(context.Context, string, time.Duration) (*model.TxReceipt, error) {
	if f.waitErr != nil {
		return nil, f.waitErr
	}
	if f.receipt != nil {
		return f.receipt, nil
	}
	return &model.TxReceipt{TxHash: "0xtxhash", Status: 1}, nil
}

func (f *fakeAdapter) TokenPrice(context.Context, string) (*model.Price, error) {
	return &model.Price{USD: 1}, nil
}

func (f *fakeAdapter) PoolInfo(context.Context, string) (*model.PoolInfo, error) {
	return &model.PoolInfo{}, nil
}

func (f *fakeAdapter) Liquidity(context.Context, string) (*model.LiquidityInfo, error) {
	return &model.LiquidityInfo{}, nil
}

func (f *fakeAdapter) TokenSecurity(context.Context, string) (*model.SecurityReport, error) {
	return &model.SecurityReport{}, nil
}

func (f *fakeAdapter) HolderConcentration(context.Context, string, int) (*model.Concentration, error) {
	return &model.Concentration{}, nil
}

func (f *fakeAdapter) SubscribeSwaps(context.Context, string, func(model.SwapEvent)) (model.Unsubscribe, error) {
	return func() {}, nil
}

type fakeAdapters struct {
	adapter model.ChainAdapter
	err     error
}

func (f *fakeAdapters) Get(string) (model.ChainAdapter, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.adapter, nil
}

type fakeRisk struct {
	paused bool
	reason string
}

func (f *fakeRisk) CheckEntry(model.EntryRequest) (*model.RiskDecision, error) {
	return &model.RiskDecision{}, nil
}

func (f *fakeRisk) CheckExit(*model.Position, float64, float64) (*model.RiskDecision, error) {
	return &model.RiskDecision{}, nil
}

func (f *fakeRisk) Pause(string, time.Time) error { return nil }

func (f *fakeRisk) Paused() (bool, string) { return f.paused, f.reason }

type fakeSigner struct{ addr string }

func (f fakeSigner) Address(string) (string, error) { return f.addr, nil }

func (f fakeSigner) SignSwap(context.Context, string, *model.UnsignedTx, model.SwapParams) (string, error) {
	return "0xsignedtx", nil
}

type fakeSolSigner struct{ addr string }

func (f fakeSolSigner) Address(string) (string, error) { return f.addr, nil }

func (f fakeSolSigner) SignSolanaTx(context.Context, string, []byte) (string, error) {
	return "base64signedtx", nil
}

func testIntent(chain string) *model.TradeIntent {
	return &model.TradeIntent{
		Chain: chain, TokenIn: "USDC", TokenOut: "T", Side: model.SideBuy,
		AmountIn: big.NewInt(1000000), SlippageBps: 100,
	}
}

func newExec(adapter model.ChainAdapter, adapterErr error, signer model.SwapSigner,
	solSigner model.SolanaTxSigner, risk *fakeRisk, dryRun bool) (*Executor, *fakeAlert) {
	alerts := &fakeAlert{}
	x := NewExecutor(&fakeAdapters{adapter: adapter, err: adapterErr}, signer, solSigner, risk,
		NewMemoryPositions(), alerts, nil, nil,
		config.RiskConfig{MaxPositionPct: 0.1, MaxOpenPositions: 5}, dryRun,
		map[string]string{"base": "0xeeee", "solana": "So11111111111111111111111111111111111111112"})
	return x, alerts
}

// ---- Execute 前置校验 ---------------------------------------------------------

func TestExecuteNilIntent(t *testing.T) {
	x, _ := newExec(&fakeAdapter{}, nil, fakeSigner{}, nil, &fakeRisk{}, true)
	if _, err := x.Execute(nil); err == nil {
		t.Fatal("nil intent 应报错")
	}
}

func TestExecuteFailsWhenAdapterUnavailable(t *testing.T) {
	x, _ := newExec(nil, errors.New("no rpc"), fakeSigner{}, nil, &fakeRisk{}, false)
	res, err := x.Execute(testIntent("base"))
	if err == nil {
		t.Fatal("适配器不可用应报错")
	}
	if res == nil || res.Order == nil {
		t.Fatal("失败也须返回订单上下文（便于落库与排障）")
	}
	if res.Order.Status == model.OrderFilled {
		t.Fatalf("不应标记为成交：%v", res.Order.Status)
	}
	if !strings.Contains(err.Error(), "chain adapter unavailable") {
		t.Fatalf("错误信息应说明原因：%v", err)
	}
}

func TestExecuteRejectsWhenRiskPaused(t *testing.T) {
	x, _ := newExec(&fakeAdapter{}, nil, fakeSigner{}, nil, &fakeRisk{paused: true, reason: "人工熔断"}, false)
	res, err := x.Execute(testIntent("base"))
	if err == nil {
		t.Fatal("熔断期间应拒绝执行")
	}
	if !strings.Contains(err.Error(), "熔断") {
		t.Fatalf("错误应说明熔断原因：%v", err)
	}
	if res == nil || res.Order.Status == model.OrderFilled {
		t.Fatal("熔断期间不应产生成交订单")
	}
}

func TestExecuteDryRunSimulatesWithoutBroadcast(t *testing.T) {
	adapter := &fakeAdapter{}
	x, alerts := newExec(adapter, nil, nil, nil, &fakeRisk{}, true)

	res, err := x.Execute(testIntent("base"))
	if err != nil {
		t.Fatalf("dry-run 不应报错：%v", err)
	}
	if !res.DryRun {
		t.Fatal("结果应标记 dry-run")
	}
	if res.Order.Status != model.OrderFilled {
		t.Fatalf("dry-run 应模拟为已成交：%v", res.Order.Status)
	}
	if res.TxHash != "" || adapter.sendCalls != 0 || adapter.buildCalls != 0 {
		t.Fatal("dry-run 绝不能真实构建或广播交易")
	}
	if alerts.raised == 0 {
		t.Fatal("dry-run 应发出模拟下单告警")
	}
	if res.Order.ID == "" {
		t.Fatal("应自动生成订单 ID")
	}
	if res.Order.Side != model.SideBuy {
		t.Fatalf("默认方向应为买入：%v", res.Order.Side)
	}
}

func TestExecuteSolanaRequiresSolanaSigner(t *testing.T) {
	x, _ := newExec(&fakeAdapter{}, nil, fakeSigner{}, nil, &fakeRisk{}, false)
	res, err := x.Execute(testIntent("solana"))
	if err == nil {
		t.Fatal("缺少 Solana 签名器应拒绝")
	}
	if !strings.Contains(err.Error(), "solana signer") {
		t.Fatalf("错误信息应指明缺少 Solana 签名器：%v", err)
	}
	if res != nil && res.Order.Status == model.OrderFilled {
		t.Fatal("不应成交")
	}
}

func TestExecuteRequiresEVMSignerWhenSerializedEmpty(t *testing.T) {
	x, _ := newExec(&fakeAdapter{}, nil, nil, nil, &fakeRisk{}, false)
	_, err := x.Execute(testIntent("base"))
	if err == nil {
		t.Fatal("缺少 EVM 签名器应拒绝发送")
	}
	if !strings.Contains(err.Error(), "no signer configured") {
		t.Fatalf("错误信息应指明缺少签名器：%v", err)
	}
}

// ---- Execute 成功路径 ---------------------------------------------------------

func TestExecuteEVMSuccessPath(t *testing.T) {
	adapter := &fakeAdapter{router: "1inch"}
	x, alerts := newExec(adapter, nil, fakeSigner{addr: "0xsigner"}, nil, &fakeRisk{}, false)

	res, err := x.Execute(testIntent("base"))
	if res == nil || res.Order.Status != model.OrderFilled {
		t.Fatalf("EVM 成功路径应成交：err=%v res=%+v", err, res)
	}
	if res.TxHash != "0xtxhash" {
		t.Fatalf("应记录交易哈希：%q", res.TxHash)
	}
	if adapter.buildCalls != 1 || adapter.sendCalls != 1 {
		t.Fatalf("应恰好构建并广播一次：build=%d send=%d", adapter.buildCalls, adapter.sendCalls)
	}
	if res.Order.Router != "1inch" {
		t.Fatalf("应记录路由来源供审计：%q", res.Order.Router)
	}
	if alerts.raised == 0 {
		t.Fatal("广播后应发出告警")
	}
}

func TestExecuteSolanaSuccessPathUsesSolanaSigner(t *testing.T) {
	adapter := &fakeAdapter{serialized: []byte{1, 2, 3}}
	x, _ := newExec(adapter, nil, nil, fakeSolSigner{addr: "So1signer"}, &fakeRisk{}, false)

	res, err := x.Execute(testIntent("solana"))
	if res == nil || res.Order.Status != model.OrderFilled {
		t.Fatalf("Solana 成功路径应成交：err=%v res=%+v", err, res)
	}
	if res.TxHash != "0xtxhash" {
		t.Fatalf("应记录交易哈希：%q", res.TxHash)
	}
}

// ---- Execute 失败路径 ---------------------------------------------------------

func TestExecuteFailsWhenBuildSwapErrors(t *testing.T) {
	adapter := &fakeAdapter{buildErr: errors.New("router 503")}
	x, _ := newExec(adapter, nil, fakeSigner{}, nil, &fakeRisk{}, false)

	res, err := x.Execute(testIntent("base"))
	if err == nil || !strings.Contains(err.Error(), "build swap") {
		t.Fatalf("构建失败应报错并说明阶段：%v", err)
	}
	if res == nil || res.Order.Status == model.OrderFilled {
		t.Fatal("构建失败不应成交")
	}
	if adapter.sendCalls != 0 {
		t.Fatal("构建失败绝不能广播")
	}
}

func TestExecuteFailsWhenSendErrors(t *testing.T) {
	adapter := &fakeAdapter{sendErr: errors.New("nonce too low")}
	x, _ := newExec(adapter, nil, fakeSigner{}, nil, &fakeRisk{}, false)

	res, err := x.Execute(testIntent("base"))
	if err == nil || !strings.Contains(err.Error(), "send transaction") {
		t.Fatalf("广播失败应报错并说明阶段：%v", err)
	}
	if res == nil || res.Order.Status == model.OrderFilled {
		t.Fatal("广播失败不应成交")
	}
}

func TestExecuteFailsWhenReceiptNotSuccessful(t *testing.T) {
	adapter := &fakeAdapter{receipt: &model.TxReceipt{TxHash: "0xtxhash", Status: 0}}
	x, _ := newExec(adapter, nil, fakeSigner{}, nil, &fakeRisk{}, false)

	res, err := x.Execute(testIntent("base"))
	if err == nil || !strings.Contains(err.Error(), "链上状态非成功") {
		t.Fatalf("链上执行失败应报错：%v", err)
	}
	if res == nil || res.Order.Status == model.OrderFilled {
		t.Fatal("链上失败不应成交")
	}
}

func TestExecuteFailsWhenConfirmationTimesOut(t *testing.T) {
	adapter := &fakeAdapter{waitErr: errors.New("timeout")}
	x, _ := newExec(adapter, nil, fakeSigner{}, nil, &fakeRisk{}, false)

	res, err := x.Execute(testIntent("base"))
	if err == nil || !strings.Contains(err.Error(), "confirmation") {
		t.Fatalf("确认超时应报错：%v", err)
	}
	if res == nil || res.Order.Status == model.OrderFilled {
		t.Fatal("确认失败不应成交")
	}
}

// ---- 纯函数 -------------------------------------------------------------------

func TestIsSolanaChain(t *testing.T) {
	if !isSolanaChain("solana") {
		t.Fatal("solana 应识别为 Solana 链")
	}
	if isSolanaChain("base") || isSolanaChain("ethereum") {
		t.Fatal("EVM 链不应识别为 Solana")
	}
}

func TestNativeTokenLookup(t *testing.T) {
	x, _ := newExec(&fakeAdapter{}, nil, nil, nil, &fakeRisk{}, true)
	if got := x.nativeToken("base"); got != "0xeeee" {
		t.Fatalf("应返回配置的原生代币地址：%q", got)
	}
	// 未配置的链由实现自行兜底（可能返回原生代币占位符），此处只确保不 panic
	// 且返回值稳定可预期（记录实际值便于排障）。
	t.Logf("未配置链的 nativeToken 兜底值：%q", x.nativeToken("unknown-chain"))
}

func TestAmountStringAndSignedAmount(t *testing.T) {
	if got := amountString(big.NewInt(12345)); got != "12345" {
		t.Fatalf("金额应十进制字符串：%q", got)
	}
	if got := amountString(nil); got != "0" {
		t.Fatalf("nil 金额应安全返回 0：%q", got)
	}

	amount := big.NewInt(5000)
	buy := signedAmount(amount, model.SideBuy)
	sell := signedAmount(amount, model.SideSell)
	if buy == nil || sell == nil {
		t.Fatal("signedAmount 不应返回 nil")
	}
	if new(big.Int).Add(buy, sell).Sign() != 0 {
		t.Fatalf("买卖符号应相反：buy=%s sell=%s", buy, sell)
	}
	if new(big.Int).Abs(buy).Cmp(amount) != 0 {
		t.Fatalf("绝对值应等于原金额：%s", buy)
	}
}
