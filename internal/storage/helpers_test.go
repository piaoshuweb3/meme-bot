package storage

import "meme-bot/internal/config"

// newConfig 构造仅含数据库 DSN 的最小配置（测试用，避免触发 config.Validate）。
func newConfig(dsn string) *config.Config {
	cfg := &config.Config{}
	cfg.Database.DSN = dsn
	return cfg
}
