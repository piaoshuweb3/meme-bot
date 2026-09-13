// Package logger 提供全局结构化日志（zap）。
package logger

import (
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"meme-bot/internal/config"
)

// New 依据运行模式构建 logger：dry_run/开发用可读格式，live/生产用 JSON。
func New(cfg *config.Config) (*zap.Logger, error) {
	level := zapcore.InfoLevel
	if cfg != nil && cfg.Mode != config.ModeLive {
		level = zapcore.DebugLevel
	}

	var zcfg zap.Config
	if cfg != nil && cfg.Mode == config.ModeLive {
		zcfg = zap.NewProductionConfig()
	} else {
		zcfg = zap.NewDevelopmentConfig()
	}
	zcfg.Level = zap.NewAtomicLevelAt(level)
	zcfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder

	return zcfg.Build()
}

// Must 构建 logger，失败时 panic（仅用于启动阶段）。
func Must(cfg *config.Config) *zap.Logger {
	l, err := New(cfg)
	if err != nil {
		panic(err)
	}
	return l
}

// Nop 返回一个丢弃所有日志的 logger（测试用）。
func Nop() *zap.Logger { return zap.NewNop() }
