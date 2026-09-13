package risk

import (
	"context"
	"strconv"
	"sync"
	"time"
)

// MemoryStore 是 StateStore 的内存实现（单实例/测试用，进程重启即丢失）。
//
// 生产环境请使用 Redis 实现（见 internal/storage/state.go），以保证多实例共享熔断状态。
type MemoryStore struct {
	mu   sync.Mutex
	data map[string]memEntry
}

type memEntry struct {
	value   string
	expires time.Time
}

// NewMemoryStore 构建内存状态存储。
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{data: make(map[string]memEntry)}
}

func (m *MemoryStore) gc() {
	now := time.Now()
	for k, v := range m.data {
		if !v.expires.IsZero() && now.After(v.expires) {
			delete(m.data, k)
		}
	}
}

// Get 实现 StateStore。
func (m *MemoryStore) Get(_ context.Context, key string) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gc()
	e, ok := m.data[key]
	if !ok {
		return "", false, nil
	}
	return e.value, true, nil
}

// Set 实现 StateStore。
func (m *MemoryStore) Set(_ context.Context, key, value string, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	e := memEntry{value: value}
	if ttl > 0 {
		e.expires = time.Now().Add(ttl)
	}
	m.data[key] = e
	return nil
}

// Incr 实现 StateStore。
func (m *MemoryStore) Incr(_ context.Context, key string, delta float64, ttl time.Duration) (float64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.data[key]
	cur := 0.0
	if ok {
		cur, _ = strconv.ParseFloat(e.value, 64)
	}
	cur += delta
	e.value = strconv.FormatFloat(cur, 'f', -1, 64)
	if ok && !e.expires.IsZero() {
		// 保留原 TTL
	} else if ttl > 0 {
		e.expires = time.Now().Add(ttl)
	}
	m.data[key] = e
	return cur, nil
}

// Del 实现 StateStore。
func (m *MemoryStore) Del(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, key)
	return nil
}

var _ StateStore = (*MemoryStore)(nil)
