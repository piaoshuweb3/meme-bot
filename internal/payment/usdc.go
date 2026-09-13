package payment

import (
	"context"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

// PendingOrder 待支付订单（最小视图，避免 payment 包依赖订阅实现）。
type PendingOrder struct {
	OrderID   string
	UserID    int64
	AmountUSD float64
	CreatedAt time.Time
}

// TransferLog 链上转账日志（确认数由 LogSource 填充）。
type TransferLog struct {
	TxHash        string
	From          string
	To            string
	Value         *big.Int
	BlockNumber   uint64
	LogIndex      uint64
	Confirmations uint64
	At            time.Time
}

// LogSource 提供某资产的转入日志与当前区块高度。
type LogSource interface {
	RecentTransfers(ctx context.Context, asset, to string, fromBlock uint64) ([]TransferLog, error)
	BlockNumber(ctx context.Context) (uint64, error)
}

// OrderSource 提供待支付订单并回写支付结果。
type OrderSource interface {
	PendingOrders(ctx context.Context, since time.Time) ([]PendingOrder, error)
	MarkPaid(ctx context.Context, orderID, txHash, payer string) error
}

// WatcherConfig 收款监听配置。
type WatcherConfig struct {
	Asset           string
	PayTo           string
	Decimals        int
	Confirmations   uint64
	PollInterval    time.Duration
	OrderWindow     time.Duration
	AmountTolerance float64
	StartBehind     uint64
}

func (c WatcherConfig) withDefaults() WatcherConfig {
	if c.Decimals <= 0 {
		c.Decimals = 6
	}
	if c.Confirmations == 0 {
		c.Confirmations = 12
	}
	if c.PollInterval <= 0 {
		c.PollInterval = 30 * time.Second
	}
	if c.OrderWindow <= 0 {
		c.OrderWindow = 48 * time.Hour
	}
	if c.StartBehind == 0 {
		c.StartBehind = 500
	}
	return c
}

// Watcher 监听链上稳定币收款并激活对应订单。
type Watcher struct {
	cfg    WatcherConfig
	logs   LogSource
	orders OrderSource
	log    *zap.Logger

	mu     sync.Mutex
	cursor uint64
	seen   map[string]struct{}
}

// NewWatcher 构建监听器。
func NewWatcher(cfg WatcherConfig, logs LogSource, orders OrderSource, log *zap.Logger) *Watcher {
	if log == nil {
		log = zap.NewNop()
	}
	return &Watcher{cfg: cfg.withDefaults(), logs: logs, orders: orders, log: log, seen: make(map[string]struct{})}
}

// Run 阻塞轮询直到 ctx 取消。
func (w *Watcher) Run(ctx context.Context) error {
	w.log.Info("usdc watcher started",
		zap.String("asset", w.cfg.Asset), zap.String("pay_to", w.cfg.PayTo),
		zap.Uint64("confirmations", w.cfg.Confirmations), zap.Duration("interval", w.cfg.PollInterval))

	ticker := time.NewTicker(w.cfg.PollInterval)
	defer ticker.Stop()

	if w.logs != nil {
		if head, err := w.logs.BlockNumber(ctx); err == nil {
			start := uint64(0)
			if head > w.cfg.StartBehind {
				start = head - w.cfg.StartBehind
			}
			w.mu.Lock()
			w.cursor = start
			w.mu.Unlock()
		}
	}

	for {
		if _, err := w.PollOnce(ctx); err != nil {
			w.log.Warn("usdc watcher poll failed", zap.Error(err))
		}
		select {
		case <-ctx.Done():
			w.log.Info("usdc watcher stopped")
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// PollOnce 执行一轮：拉取日志 → 匹配订单 → 标记已支付；返回本轮激活订单数。
func (w *Watcher) PollOnce(ctx context.Context) (int, error) {
	if w.logs == nil || w.orders == nil {
		return 0, fmt.Errorf("usdc watcher: 缺少 logs/orders 依赖")
	}

	w.mu.Lock()
	fromBlock := w.cursor
	w.mu.Unlock()

	transfers, err := w.logs.RecentTransfers(ctx, w.cfg.Asset, w.cfg.PayTo, fromBlock)
	if err != nil {
		return 0, fmt.Errorf("usdc watcher: 拉取转账失败: %w", err)
	}
	head, err := w.logs.BlockNumber(ctx)
	if err != nil {
		return 0, fmt.Errorf("usdc watcher: 获取区块高度失败: %w", err)
	}

	var maxBlock = fromBlock
	confirmed := make([]TransferLog, 0, len(transfers))
	for _, t := range transfers {
		if t.BlockNumber > maxBlock {
			maxBlock = t.BlockNumber
		}
		if !strings.EqualFold(t.To, w.cfg.PayTo) {
			continue
		}
		confs := t.Confirmations
		if confs == 0 && head >= t.BlockNumber {
			confs = head - t.BlockNumber + 1
		}
		if confs < w.cfg.Confirmations {
			w.log.Debug("usdc: 等待确认", zap.String("tx", t.TxHash), zap.Uint64("confirmations", confs))
			continue
		}
		t.Confirmations = confs
		confirmed = append(confirmed, t)
	}

	orders, err := w.orders.PendingOrders(ctx, time.Now().UTC().Add(-w.cfg.OrderWindow))
	if err != nil {
		return 0, fmt.Errorf("usdc watcher: 读取待支付订单失败: %w", err)
	}
	if len(confirmed) == 0 || len(orders) == 0 {
		w.advanceCursor(maxBlock)
		return 0, nil
	}

	matches := MatchOrders(orders, confirmed, w.cfg.Decimals, w.cfg.AmountTolerance)
	activated := 0
	for orderID, transfer := range matches {
		w.mu.Lock()
		_, dup := w.seen[transfer.TxHash]
		w.mu.Unlock()
		if dup {
			continue
		}
		if err := w.orders.MarkPaid(ctx, orderID, transfer.TxHash, transfer.From); err != nil {
			w.log.Error("usdc: 激活订单失败",
				zap.String("order_id", orderID), zap.String("tx", transfer.TxHash), zap.Error(err))
			continue
		}
		w.mu.Lock()
		w.seen[transfer.TxHash] = struct{}{}
		w.mu.Unlock()
		activated++
		w.log.Info("usdc: 订单已支付并激活",
			zap.String("order_id", orderID), zap.String("tx", transfer.TxHash),
			zap.String("payer", transfer.From), zap.String("amount", usdFromUnits(transfer.Value, w.cfg.Decimals)))
	}

	w.advanceCursor(maxBlock)
	return activated, nil
}

func (w *Watcher) advanceCursor(block uint64) {
	w.mu.Lock()
	if block > w.cursor {
		w.cursor = block
	}
	w.mu.Unlock()
}

// Cursor 当前扫描游标（诊断用）。
func (w *Watcher) Cursor() uint64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.cursor
}

// MatchOrders 把链上转账与待支付订单配对（纯函数，便于测试）。
//
// 匹配规则（严格、防误配）：
//   - 金额必须等于订单金额（按 decimals 换算到最小单位），容差 toleranceUSD 默认 0；
//   - 一笔订单只匹配一笔转账；一笔转账只匹配一笔订单；
//   - 转账按确认数降序优先（确认更充分的先成交）；同金额多单按创建时间最早优先。
func MatchOrders(orders []PendingOrder, transfers []TransferLog, decimals int, toleranceUSD float64) map[string]TransferLog {
	out := make(map[string]TransferLog)
	if len(orders) == 0 || len(transfers) == 0 {
		return out
	}
	if decimals <= 0 {
		decimals = 6
	}
	tolerance := usdToUnits(toleranceUSD, decimals)

	byAmount := make(map[string][]PendingOrder)
	for _, o := range orders {
		if o.OrderID == "" || o.AmountUSD <= 0 {
			continue
		}
		key := usdToUnits(o.AmountUSD, decimals).String()
		byAmount[key] = append(byAmount[key], o)
	}
	for k := range byAmount {
		list := byAmount[k]
		sort.SliceStable(list, func(i, j int) bool { return list[i].CreatedAt.Before(list[j].CreatedAt) })
		byAmount[k] = list
	}

	sorted := make([]TransferLog, len(transfers))
	copy(sorted, transfers)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Confirmations > sorted[j].Confirmations })

	usedOrders := make(map[string]struct{})
	usedTx := make(map[string]struct{})

	for _, t := range sorted {
		if t.Value == nil || t.Value.Sign() <= 0 || t.TxHash == "" {
			continue
		}
		if _, ok := usedTx[t.TxHash]; ok {
			continue
		}

		var matched *PendingOrder
		if candidates, ok := byAmount[t.Value.String()]; ok {
			for i := range candidates {
				if _, used := usedOrders[candidates[i].OrderID]; used {
					continue
				}
				matched = &candidates[i]
				break
			}
		}
		if matched == nil && tolerance.Sign() > 0 {
			for i := range orders {
				if _, used := usedOrders[orders[i].OrderID]; used {
					continue
				}
				want := usdToUnits(orders[i].AmountUSD, decimals)
				diff := new(big.Int).Sub(t.Value, want)
				diff.Abs(diff)
				if diff.Cmp(tolerance) <= 0 {
					matched = &orders[i]
					break
				}
			}
		}
		if matched == nil {
			continue
		}

		usedOrders[matched.OrderID] = struct{}{}
		usedTx[t.TxHash] = struct{}{}
		out[matched.OrderID] = t
	}
	return out
}

// usdToUnits 把 USD 金额换算为最小单位（截断到整数）。
func usdToUnits(usd float64, decimals int) *big.Int {
	if usd <= 0 {
		return big.NewInt(0)
	}
	scale := new(big.Float).SetFloat64(1)
	for i := 0; i < decimals; i++ {
		scale.Mul(scale, big.NewFloat(10))
	}
	f := new(big.Float).SetFloat64(usd)
	f.Mul(f, scale)
	units, _ := f.Int(nil)
	return units
}

// usdFromUnits 最小单位 → 可读 USD 字符串（日志用）。
func usdFromUnits(v *big.Int, decimals int) string {
	if v == nil {
		return "0"
	}
	f := new(big.Float).SetInt(v)
	scale := new(big.Float).SetFloat64(1)
	for i := 0; i < decimals; i++ {
		scale.Mul(scale, big.NewFloat(10))
	}
	f.Quo(f, scale)
	return f.Text('f', decimals)
}
