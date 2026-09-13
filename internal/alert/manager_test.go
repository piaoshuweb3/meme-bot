package alert

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"meme-bot/internal/config"
	"meme-bot/internal/model"
	"meme-bot/internal/risk"
)

// countingChannel 记录发送次数与最后一条消息（测试用）。
type countingChannel struct {
	mu   sync.Mutex
	n    int
	last *model.Alert
}

func (c *countingChannel) Name() string { return "test" }

func (c *countingChannel) Send(_ context.Context, a *model.Alert) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n++
	c.last = a
	return nil
}

func (c *countingChannel) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

func TestRaiseDeduplicatesWithinWindow(t *testing.T) {
	ch := &countingChannel{}
	m := New(config.AlertConfig{DedupWindowMinutes: 5}, []model.Channel{ch}, risk.NewMemoryStore(), nil, nil)

	alert := &model.Alert{Level: model.LevelWarning, Title: "价格波动", Token: "0xabc", Chain: "base"}
	if err := m.Raise(context.Background(), alert); err != nil {
		t.Fatalf("raise: %v", err)
	}
	if err := m.Raise(context.Background(), alert); err != nil {
		t.Fatalf("raise 2: %v", err)
	}
	time.Sleep(150 * time.Millisecond) // 异步发送

	if got := ch.count(); got != 1 {
		t.Fatalf("相同告警在窗口内应只发送一次，实际 %d 次", got)
	}
}

func TestCriticalIsNotSuppressed(t *testing.T) {
	ch := &countingChannel{}
	m := New(config.AlertConfig{DedupWindowMinutes: 0}, []model.Channel{ch}, risk.NewMemoryStore(), nil, nil)
	m.SuppressToken("base", "0xabc", time.Hour)

	critical := &model.Alert{Level: model.LevelCritical, Title: "LP 解锁", Token: "0xabc", Chain: "base"}
	if err := m.Raise(context.Background(), critical); err != nil {
		t.Fatalf("raise: %v", err)
	}
	time.Sleep(150 * time.Millisecond)
	if ch.count() != 1 {
		t.Fatal("Critical 告警不应被抑制")
	}

	// 非 Critical 的同类告警应被抑制
	info := &model.Alert{Level: model.LevelInfo, Title: "价格波动", Token: "0xabc", Chain: "base"}
	if err := m.Raise(context.Background(), info); err != nil {
		t.Fatalf("raise info: %v", err)
	}
	time.Sleep(150 * time.Millisecond)
	if ch.count() != 1 {
		t.Fatal("已止损代币的非关键告警应被抑制")
	}
}

func TestRaiseRejectsInvalidLevel(t *testing.T) {
	ch := &countingChannel{}
	m := New(config.AlertConfig{}, []model.Channel{ch}, risk.NewMemoryStore(), nil, nil)
	if err := m.Raise(context.Background(), &model.Alert{Level: "boom", Title: "x"}); err == nil {
		t.Fatal("非法级别应返回错误")
	}
}

func TestFormatMessageEscapesHTML(t *testing.T) {
	msg := formatMessage(&model.Alert{
		Level:   model.LevelCritical,
		Title:   "LP <Unlock>",
		Message: "令牌 & 权限 <危险>",
		Chain:   "base",
		Token:   "0xabc",
	})

	if strings.Contains(msg, "<Unlock>") || strings.Contains(msg, "<危险>") {
		t.Fatalf("消息中的 HTML 特殊字符必须转义：%s", msg)
	}
	if !strings.Contains(msg, "&lt;Unlock&gt;") {
		t.Fatalf("应包含转义后的标题：%s", msg)
	}
	if !strings.Contains(msg, "CRITICAL") {
		t.Fatalf("应包含级别标记：%s", msg)
	}
}

func TestInlineKeyboardSkipsEmptyButtons(t *testing.T) {
	kb := inlineKeyboard([]model.Button{
		{Text: "一键平仓", Data: "close:0x1"},
		{Text: "", Data: "ignore:x"},
		{Text: "忽略", Data: ""},
	})
	if kb == nil {
		t.Fatal("有效按钮应生成键盘")
	}
	rows, ok := kb["inline_keyboard"].([][]map[string]string)
	if !ok {
		t.Fatalf("键盘结构不正确：%#v", kb)
	}
	if len(rows) != 1 || len(rows[0]) != 1 {
		t.Fatalf("空按钮应被过滤，实际 %#v", rows)
	}
	if rows[0][0]["callback_data"] != "close:0x1" {
		t.Fatalf("callback_data 不正确：%#v", rows[0][0])
	}

	if kb := inlineKeyboard(nil); kb != nil {
		t.Fatal("无按钮时应返回 nil（不附加键盘）")
	}
}

func TestSplitAction(t *testing.T) {
	action, payload := splitAction("close:0xabcdef")
	if action != "close" || payload != "0xabcdef" {
		t.Fatalf("解析错误：%s / %s", action, payload)
	}
	action, payload = splitAction("/status")
	if action != "status" || payload != "" {
		t.Fatalf("命令解析错误：%s / %s", action, payload)
	}
}
