package model

import (
	"context"
	"time"
)

// ---------------------------------------------------------------------------
// 告警
// ---------------------------------------------------------------------------

// Level 告警级别。
type Level string

const (
	LevelInfo     Level = "info"
	LevelWarning  Level = "warning"
	LevelCritical Level = "critical"
)

// Valid 校验级别合法性。
func (l Level) Valid() bool {
	switch l {
	case LevelInfo, LevelWarning, LevelCritical:
		return true
	}
	return false
}

// Button Telegram 内联按钮。
type Button struct {
	Text string `json:"text"`
	// Data 形如 "action:payload"，例如 "close:0xabc..."
	Data string `json:"data"`
}

// Alert 告警消息。
type Alert struct {
	ID        string         `json:"id"`
	Level     Level          `json:"level"`
	Title     string         `json:"title"`
	Message   string         `json:"message"`
	Chain     string         `json:"chain,omitempty"`
	Token     string         `json:"token,omitempty"`
	Address   string         `json:"address,omitempty"`
	ActionURL string         `json:"action_url,omitempty"`
	Buttons   []Button       `json:"buttons,omitempty"`
	Timestamp time.Time      `json:"timestamp"`
	Extra     map[string]any `json:"extra,omitempty"`
}

// Channel 告警通道。
type Channel interface {
	Name() string
	Send(ctx context.Context, a *Alert) error
}

// AlertSink 告警端口（供其他模块调用，避免直接依赖 alert 包实现）。
type AlertSink interface {
	Raise(ctx context.Context, a *Alert) error
}

// AlertActionHandler 处理 Telegram 按钮回调（action, payload）。
type AlertActionHandler interface {
	HandleAction(ctx context.Context, userID int64, action, payload string) string
}
