package address

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"meme-bot/internal/model"
)

// MemoryStore 是 model.ProfileStore 的内存实现（本地开发与单元测试用）。
type MemoryStore struct {
	mu       sync.RWMutex
	profiles map[string]*model.AddressProfile
	trades   []*model.TradeRecord
}

// NewMemoryStore 构建内存画像存储。
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{profiles: make(map[string]*model.AddressProfile)}
}

func pkey(chain, address string) string {
	return strings.ToLower(chain) + "|" + strings.ToLower(address)
}

// Get 实现 model.ProfileStore。
func (m *MemoryStore) Get(_ context.Context, chain, address string) (*model.AddressProfile, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.profiles[pkey(chain, address)]
	if !ok {
		return nil, nil
	}
	cp := *p
	cp.Tags = append([]model.AddressTag(nil), p.Tags...)
	return &cp, nil
}

// Upsert 实现 model.ProfileStore。
func (m *MemoryStore) Upsert(_ context.Context, p *model.AddressProfile) error {
	if p == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *p
	cp.Tags = append([]model.AddressTag(nil), p.Tags...)
	m.profiles[pkey(p.Chain, p.Address)] = &cp
	return nil
}

// ListTop 实现 model.ProfileStore。
func (m *MemoryStore) ListTop(_ context.Context, chain string, limit int) ([]*model.AddressProfile, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*model.AddressProfile, 0, len(m.profiles))
	for _, p := range m.profiles {
		if chain != "" && !strings.EqualFold(p.Chain, chain) {
			continue
		}
		cp := *p
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RecentScore > out[j].RecentScore })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// IsBlacklisted 实现 model.ProfileStore。
func (m *MemoryStore) IsBlacklisted(_ context.Context, chain, address string) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.profiles[pkey(chain, address)]
	return ok && p.IsBlacklisted, nil
}

// Blacklist 实现 model.ProfileStore。
func (m *MemoryStore) Blacklist(_ context.Context, chain, address, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := pkey(chain, address)
	p, ok := m.profiles[key]
	if !ok {
		p = &model.AddressProfile{Address: address, Chain: chain}
		m.profiles[key] = p
	}
	p.IsBlacklisted = true
	p.BlacklistReason = reason
	p.UpdatedAt = time.Now().UTC()
	return nil
}

// RecordTrade 实现 model.ProfileStore。
func (m *MemoryStore) RecordTrade(_ context.Context, rec *model.TradeRecord) error {
	if rec == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *rec
	m.trades = append(m.trades, &cp)
	return nil
}

// RecentTrades 实现 model.ProfileStore。
func (m *MemoryStore) RecentTrades(_ context.Context, chain, address string, since time.Time, limit int) ([]*model.TradeRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []*model.TradeRecord
	for _, t := range m.trades {
		if !strings.EqualFold(t.Chain, chain) || !strings.EqualFold(t.Address, address) {
			continue
		}
		if !since.IsZero() && t.At.Before(since) {
			continue
		}
		cp := *t
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}

var _ model.ProfileStore = (*MemoryStore)(nil)
