package payment

import (
	"context"
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"go.uber.org/zap"
)

type stubLogs struct {
	head uint64
	list []TransferLog
}

func (s *stubLogs) RecentTransfers(context.Context, string, string, uint64) ([]TransferLog, error) {
	return s.list, nil
}
func (s *stubLogs) BlockNumber(context.Context) (uint64, error) { return s.head, nil }

type stubOrders struct {
	mu     sync.Mutex
	orders []PendingOrder
	paid   map[string]string
}

func (s *stubOrders) PendingOrders(context.Context, time.Time) ([]PendingOrder, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]PendingOrder(nil), s.orders...), nil
}

func (s *stubOrders) MarkPaid(_ context.Context, orderID, txHash, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.paid == nil {
		s.paid = make(map[string]string)
	}
	s.paid[orderID] = txHash
	return nil
}

func units(usd float64, decimals int) *big.Int { return usdToUnits(usd, decimals) }

func TestMatchOrdersExact(t *testing.T) {
	now := time.Now().UTC()
	orders := []PendingOrder{
		{OrderID: "o1", AmountUSD: 49, CreatedAt: now},
		{OrderID: "o2", AmountUSD: 199, CreatedAt: now},
	}
	transfers := []TransferLog{
		{TxHash: "0xtx1", To: deadAddr, Value: units(49, 6), Confirmations: 15},
		{TxHash: "0xtx2", To: deadAddr, Value: units(199, 6), Confirmations: 15},
	}

	got := MatchOrders(orders, transfers, 6, 0)
	if len(got) != 2 {
		t.Fatalf("应匹配 2 笔订单，实际 %d", len(got))
	}
	if got["o1"].TxHash != "0xtx1" || got["o2"].TxHash != "0xtx2" {
		t.Fatalf("匹配结果错误：%+v", got)
	}
}

func TestMatchOrdersMismatchToleranceAndReuse(t *testing.T) {
	now := time.Now().UTC()
	orders := []PendingOrder{{OrderID: "o1", AmountUSD: 49, CreatedAt: now}}

	// 金额不符（50 vs 49）且无容差 → 不匹配
	if got := MatchOrders(orders, []TransferLog{
		{TxHash: "0xb", To: deadAddr, Value: units(50, 6), Confirmations: 20},
	}, 6, 0); len(got) != 0 {
		t.Fatal("金额不符且无容差时不应匹配")
	}

	// 容差 1.5 USD → 50 可匹配 49
	if got := MatchOrders(orders, []TransferLog{
		{TxHash: "0xb", To: deadAddr, Value: units(50, 6), Confirmations: 20},
	}, 6, 1.5); len(got) != 1 {
		t.Fatal("容差范围内应匹配")
	}

	// 两笔转账争抢同一订单 → 只成交一笔，且优先确认数更高者
	got := MatchOrders(orders, []TransferLog{
		{TxHash: "0xa", To: deadAddr, Value: units(49, 6), Confirmations: 12},
		{TxHash: "0xb", To: deadAddr, Value: units(49, 6), Confirmations: 30},
	}, 6, 0)
	if len(got) != 1 {
		t.Fatalf("一笔订单只应匹配一笔转账，实际 %d", len(got))
	}
	if got["o1"].TxHash != "0xb" {
		t.Fatalf("应优先确认数更高的转账，实际 %s", got["o1"].TxHash)
	}
}

func TestWatcherActivatesConfirmedTransferAndIsIdempotent(t *testing.T) {
	orders := &stubOrders{orders: []PendingOrder{
		{OrderID: "o1", UserID: 7, AmountUSD: 49, CreatedAt: time.Now().UTC()},
	}}
	logs := &stubLogs{head: 1000, list: []TransferLog{
		{TxHash: "0xpaid", From: "0xpayer", To: deadAddr, Value: units(49, 6), BlockNumber: 995},
	}}
	w := NewWatcher(WatcherConfig{
		Asset: baseUSDC, PayTo: deadAddr, Confirmations: 3, OrderWindow: time.Hour,
	}, logs, orders, zap.NewNop())

	activated, err := w.PollOnce(context.Background())
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if activated != 1 {
		t.Fatalf("应激活 1 笔订单，实际 %d", activated)
	}
	if orders.paid["o1"] != "0xpaid" {
		t.Fatalf("订单应被标记为已支付：%+v", orders.paid)
	}
	if w.Cursor() != 995 {
		t.Fatalf("游标应推进到 995，实际 %d", w.Cursor())
	}

	// 同一 tx 再次轮询 → 不重复激活
	activated, err = w.PollOnce(context.Background())
	if err != nil {
		t.Fatalf("poll 2: %v", err)
	}
	if activated != 0 {
		t.Fatalf("重复轮询不应再次激活，实际 %d", activated)
	}
}

func TestWatcherWaitsForConfirmations(t *testing.T) {
	orders := &stubOrders{orders: []PendingOrder{{OrderID: "o1", AmountUSD: 49, CreatedAt: time.Now().UTC()}}}
	logs := &stubLogs{head: 1000, list: []TransferLog{
		{TxHash: "0xnew", To: deadAddr, Value: units(49, 6), BlockNumber: 1000, Confirmations: 1},
	}}
	w := NewWatcher(WatcherConfig{Asset: baseUSDC, PayTo: deadAddr, Confirmations: 12, OrderWindow: time.Hour},
		logs, orders, zap.NewNop())

	activated, err := w.PollOnce(context.Background())
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if activated != 0 {
		t.Fatalf("确认数不足不应激活，实际 %d", activated)
	}
	if _, ok := orders.paid["o1"]; ok {
		t.Fatal("确认不足时不应标记已支付")
	}
}

func TestWatcherIgnoresForeignRecipient(t *testing.T) {
	orders := &stubOrders{orders: []PendingOrder{{OrderID: "o1", AmountUSD: 49, CreatedAt: time.Now().UTC()}}}
	logs := &stubLogs{head: 1000, list: []TransferLog{
		{TxHash: "0xother", To: "0x000000000000000000000000000000000000bEEF", Value: units(49, 6), Confirmations: 20},
	}}
	w := NewWatcher(WatcherConfig{Asset: baseUSDC, PayTo: deadAddr, Confirmations: 3, OrderWindow: time.Hour},
		logs, orders, zap.NewNop())

	activated, err := w.PollOnce(context.Background())
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if activated != 0 {
		t.Fatalf("非收款地址的转账不应激活订单，实际 %d", activated)
	}
}

func TestHexAndTopicHelpers(t *testing.T) {
	if v, err := parseHexUint64("0x3e8"); err != nil || v != 1000 {
		t.Fatalf("parseHexUint64 失败：%v %d", err, v)
	}
	if v, err := parseHexBig("0x1"); err != nil || v.Int64() != 1 {
		t.Fatalf("parseHexBig 失败：%v %v", err, v)
	}
	if _, err := parseHexUint64("0x"); err == nil {
		t.Fatal("空十六进制应报错")
	}
	if _, err := parseHexBig("zz"); err == nil {
		t.Fatal("非法十六进制应报错")
	}

	topic := padTopic(deadAddr)
	if got := topicToAddress(topic); got != common.HexToAddress(deadAddr).Hex() {
		t.Fatalf("topic 往返失败：%s", got)
	}
	if got := topicToAddress("0x1234"); got != "" {
		t.Fatalf("过短 topic 应返回空串，实际 %s", got)
	}

	if got := usdFromUnits(units(49, 6), 6); got != "49.000000" {
		t.Fatalf("金额格式化异常：%s", got)
	}
}
