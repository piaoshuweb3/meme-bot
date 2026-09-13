package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"meme-bot/internal/config"
)

// NewRedis 建立 Redis 客户端并做一次 ping。
func NewRedis(ctx context.Context, cfg *config.Config) (*redis.Client, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	})
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}
	return client, nil
}

// RedisKey 统一 key 命名空间，避免与其他应用冲突。
func RedisKey(parts ...string) string {
	out := "memebot"
	for _, p := range parts {
		if p == "" {
			continue
		}
		out += ":" + p
	}
	return out
}
