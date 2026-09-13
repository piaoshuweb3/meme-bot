package storage

import (
	"context"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"meme-bot/internal/risk"
)

// RedisStateStore 是 risk.StateStore 的 Redis 实现：
// 多实例共享熔断标志、冷却窗口与当日亏损累计。
type RedisStateStore struct {
	client *redis.Client
}

// NewRedisStateStore 构建 Redis 状态存储。
func NewRedisStateStore(client *redis.Client) *RedisStateStore {
	return &RedisStateStore{client: client}
}

// Get 实现 risk.StateStore。
func (s *RedisStateStore) Get(ctx context.Context, key string) (string, bool, error) {
	v, err := s.client.Get(ctx, RedisKey(key)).Result()
	if err == redis.Nil {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v, true, nil
}

// Set 实现 risk.StateStore。
func (s *RedisStateStore) Set(ctx context.Context, key, value string, ttl time.Duration) error {
	return s.client.Set(ctx, RedisKey(key), value, ttl).Err()
}

// Incr 实现 risk.StateStore。
func (s *RedisStateStore) Incr(ctx context.Context, key string, delta float64, ttl time.Duration) (float64, error) {
	full := RedisKey(key)
	v, err := s.client.IncrByFloat(ctx, full, delta).Result()
	if err != nil {
		return 0, err
	}
	// 仅在首次创建时设置 TTL（避免每次累加都刷新过期时间）
	if ttl > 0 && v == delta {
		if err := s.client.Expire(ctx, full, ttl).Err(); err != nil {
			return v, err
		}
	}
	return v, nil
}

// Del 实现 risk.StateStore。
func (s *RedisStateStore) Del(ctx context.Context, key string) error {
	return s.client.Del(ctx, RedisKey(key)).Err()
}

// DailyCounter 读取当日计数（辅助诊断）。
func (s *RedisStateStore) DailyCounter(ctx context.Context, key string) (float64, error) {
	v, err := s.client.Get(ctx, RedisKey(key)).Result()
	if err == redis.Nil {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return strconv.ParseFloat(v, 64)
}

var _ risk.StateStore = (*RedisStateStore)(nil)
