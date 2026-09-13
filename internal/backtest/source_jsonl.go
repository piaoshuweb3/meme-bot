package backtest

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"time"

	"meme-bot/internal/model"
)

// JSONLEvent 是回放文件的行格式（刻意精简：只需策略与风控需要的字段）。
//
// 示例（每行一个对象）：
//
//	{"chain":"base","tx_hash":"0x..","token_out":"0xTOKEN","sender":"0x..",
//	 "amount_usd":12000,"price_usd":0.00042,"at":"2026-09-01T10:00:00Z"}
type JSONLEvent struct {
	Chain     string    `json:"chain"`
	TxHash    string    `json:"tx_hash"`
	Pool      string    `json:"pool"`
	TokenIn   string    `json:"token_in"`
	TokenOut  string    `json:"token_out"`
	Sender    string    `json:"sender"`
	Recipient string    `json:"recipient"`
	AmountUSD float64   `json:"amount_usd"`
	PriceUSD  float64   `json:"price_usd"`
	At        time.Time `json:"at"`
}

// SliceSource 内存事件源（测试与小样本回放）。
type SliceSource struct {
	events []model.SwapEvent
	idx    int
}

// NewSliceSource 构建内存事件源（自动按时间升序排序）。
func NewSliceSource(events []model.SwapEvent) *SliceSource {
	sorted := make([]model.SwapEvent, len(events))
	copy(sorted, events)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].At.Before(sorted[j].At) })
	return &SliceSource{events: sorted}
}

// Next 实现 EventSource。
func (s *SliceSource) Next() (model.SwapEvent, bool, error) {
	if s.idx >= len(s.events) {
		return model.SwapEvent{}, false, nil
	}
	ev := s.events[s.idx]
	s.idx++
	return ev, true, nil
}

// Len 事件总数。
func (s *SliceSource) Len() int { return len(s.events) }

// LoadJSONL 从文件加载事件（空行与以 # 开头的注释行会被跳过）。
func LoadJSONL(path string) (*SliceSource, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("backtest: 打开事件文件失败: %w", err)
	}
	defer func() { _ = f.Close() }()
	return LoadJSONLReader(f)
}

// LoadJSONLReader 从 io.Reader 加载事件。
func LoadJSONLReader(r io.Reader) (*SliceSource, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	var events []model.SwapEvent
	line := 0
	for scanner.Scan() {
		line++
		raw := scanner.Bytes()
		trimmed := string(raw)
		if len(trimmed) == 0 || trimmed[0] == '#' {
			continue
		}
		var item JSONLEvent
		if err := json.Unmarshal(raw, &item); err != nil {
			return nil, fmt.Errorf("backtest: 第 %d 行 JSON 解析失败: %w", line, err)
		}
		events = append(events, item.ToSwapEvent())
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("backtest: 读取事件失败: %w", err)
	}
	if len(events) == 0 {
		return nil, fmt.Errorf("backtest: %s 中没有有效事件", "输入")
	}
	return NewSliceSource(events), nil
}

// ToSwapEvent 转换为统一的领域事件。
func (j JSONLEvent) ToSwapEvent() model.SwapEvent {
	at := j.At
	if at.IsZero() {
		at = time.Now().UTC()
	}
	return model.SwapEvent{
		Chain:     j.Chain,
		TxHash:    j.TxHash,
		Pool:      j.Pool,
		TokenIn:   j.TokenIn,
		TokenOut:  j.TokenOut,
		Sender:    j.Sender,
		Recipient: j.Recipient,
		AmountUSD: j.AmountUSD,
		PriceUSD:  j.PriceUSD,
		At:        at,
	}
}

// SampleJSONL 生成一小段示例事件（用于 --emit-sample）。
func SampleJSONL(w io.Writer) error {
	base := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	type row struct {
		sender string
		amount float64
		price  float64
		min    int
	}
	rows := []row{
		{"0xWhaleA", 12000, 0.00040, 0},
		{"0xFollowerB", 6000, 0.00042, 2},
		{"0xWhaleA", 9000, 0.00055, 30},
		{"0xWhaleA", 9000, 0.00030, 60}, // 触发止损
	}
	enc := json.NewEncoder(w)
	for _, r := range rows {
		ev := JSONLEvent{
			Chain: "base", TxHash: "0xsample" + r.sender[len(r.sender)-1:], Pool: "0xPoolSample",
			TokenIn: "0xNativeETH", TokenOut: "0xSampleToken", Sender: r.sender,
			AmountUSD: r.amount, PriceUSD: r.price, At: base.Add(time.Duration(r.min) * time.Minute),
		}
		if err := enc.Encode(ev); err != nil {
			return err
		}
	}
	return nil
}
