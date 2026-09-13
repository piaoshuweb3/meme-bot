package alert

import (
	"context"

	"go.uber.org/zap"

	"meme-bot/internal/model"
)

// LogChannel 把告警写入结构化日志（本地开发与 dry_run 阶段的默认通道）。
type LogChannel struct {
	log *zap.Logger
}

// NewLogChannel 构建日志通道。
func NewLogChannel(log *zap.Logger) *LogChannel {
	if log == nil {
		log = zap.NewNop()
	}
	return &LogChannel{log: log}
}

// Name 实现 model.Channel。
func (c *LogChannel) Name() string { return "log" }

// Send 实现 model.Channel。
func (c *LogChannel) Send(_ context.Context, a *model.Alert) error {
	if a == nil {
		return nil
	}
	fields := []zap.Field{
		zap.String("level", string(a.Level)),
		zap.String("title", a.Title),
		zap.String("message", a.Message),
		zap.String("chain", a.Chain),
		zap.String("token", a.Token),
		zap.String("address", a.Address),
	}
	switch a.Level {
	case model.LevelCritical:
		c.log.Error("ALERT", fields...)
	case model.LevelWarning:
		c.log.Warn("ALERT", fields...)
	default:
		c.log.Info("ALERT", fields...)
	}
	return nil
}

// MultiChannel 把多个通道聚合为一个（便于一次性注册）。
type MultiChannel struct {
	channels []model.Channel
}

// NewMultiChannel 构建聚合通道。
func NewMultiChannel(channels ...model.Channel) *MultiChannel {
	return &MultiChannel{channels: channels}
}

// Name 实现 model.Channel。
func (m *MultiChannel) Name() string { return "multi" }

// Send 实现 model.Channel：逐个发送，返回首个错误。
func (m *MultiChannel) Send(ctx context.Context, a *model.Alert) error {
	for _, c := range m.channels {
		if err := c.Send(ctx, a); err != nil {
			return err
		}
	}
	return nil
}

var _ model.Channel = (*LogChannel)(nil)
var _ model.Channel = (*MultiChannel)(nil)
