package storage

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestRedisKeyNamespacing(t *testing.T) {
	if got := RedisKey(); got != "memebot" {
		t.Fatalf("半空前缀应仅为命名空间：%q", got)
	}
	if got := RedisKey("circuit"); got != "memebot:circuit" {
		t.Fatalf("单段：%q", got)
	}
	if got := RedisKey("cooldown", "base", "0xabc"); got != "memebot:cooldown:base:0xabc" {
		t.Fatalf("多段应顺序拼接：%q", got)
	}
	if got := RedisKey("", "x", ""); got != "memebot:x" {
		t.Fatalf("空段应被跳过（避免出现连续冒号）：%q", got)
	}
}

func TestNewPostgresRejectsInvalidDSN(t *testing.T) {
	cfg := newConfig("not-a-valid-dsn")
	if _, err := NewPostgres(context.Background(), cfg); err == nil {
		t.Fatal("非法 DSN 应报错")
	}
}

func TestNewPostgresFailsOnUnreachableHost(t *testing.T) {
	cfg := newConfig("postgres://u:p@127.0.0.1:1/db?sslmode=disable&connect_timeout=1")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := NewPostgres(ctx, cfg); err == nil {
		t.Fatal("不可达主机应返回错误而非静默成功")
	}
}

// TestRedisStateStoreIntegration 需要真实 Redis：
// 设置 MEMEBOT_TEST_REDIS_ADDR 或本地 6380/6379 可用时执行，否则跳过。
func TestRedisStateStoreIntegration(t *testing.T) {
	candidates := []string{os.Getenv("MEMEBOT_TEST_REDIS_ADDR"), "127.0.0.1:6380", "127.0.0.1:6379"}
	var client *redis.Client
	for _, addr := range candidates {
		if addr == "" {
			continue
		}
		c := redis.NewClient(&redis.Options{Addr: addr, DialTimeout: 2 * time.Second})
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		err := c.Ping(ctx).Err()
		cancel()
		if err == nil {
			client = c
			t.Logf("使用本地 Redis：%s", addr)
			break
		}
		_ = c.Close()
	}
	if client == nil {
		t.Skip("本地无可用 Redis，跳过集成用例")
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	store := NewRedisStateStore(client)
	key := "test:" + time.Now().UTC().Format("150405.000000000")
	defer func() { _ = store.Del(context.Background(), key) }()

	if v, ok, err := store.Get(ctx, key); err != nil || ok || v != "" {
		t.Fatalf("未写入应返回 (空,false,nil)：%q %v %v", v, ok, err)
	}
	if err := store.Set(ctx, key, "42", time.Minute); err != nil {
		t.Fatalf("Set 失败：%v", err)
	}
	if v, ok, err := store.Get(ctx, key); err != nil || !ok || v != "42" {
		t.Fatalf("Get 失败：%q %v %v", v, ok, err)
	}
	if v, err := store.DailyCounter(ctx, key); err != nil || v != 42 {
		t.Fatalf("DailyCounter 失败：%v %v", v, err)
	}

	if v, err := store.Incr(ctx, key, 1.5, time.Minute); err != nil || v != 43.5 {
		t.Fatalf("首次 Incr 失败：%v %v", v, err)
	}
	if v, err := store.Incr(ctx, key, 0.5, time.Minute); err != nil || v != 44 {
		t.Fatalf("再次 Incr 应继续累加：%v %v", v, err)
	}

	if err := store.Del(ctx, key); err != nil {
		t.Fatalf("Del 失败：%v", err)
	}
	if _, ok, _ := store.Get(ctx, key); ok {
		t.Fatal("Del 后不应再命中")
	}
	if v, err := store.DailyCounter(ctx, key); err != nil || v != 0 {
		t.Fatalf("缺失键应为 0：%v %v", v, err)
	}
}
