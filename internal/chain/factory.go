package chain

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"go.uber.org/zap"

	"meme-bot/internal/config"
	"meme-bot/internal/model"
)

// Factory 链适配器工厂：持有所有已构建的 adapter，供上层按链 ID 获取。
type Factory struct {
	cfg      *config.Config
	log      *zap.Logger
	mu       sync.RWMutex
	adapters map[string]model.ChainAdapter
	skipped  []string
}

// NewFactory 按配置构建所有启用的链。
//
// 降级策略：某条链的适配器未注册或构建失败时，记录警告并跳过，
// 保证进程仍可启动（便于分阶段开发与本地调试）。
func NewFactory(cfg *config.Config, log *zap.Logger) (*Factory, error) {
	if cfg == nil {
		return nil, fmt.Errorf("nil config")
	}
	if log == nil {
		log = zap.NewNop()
	}
	f := &Factory{
		cfg:      cfg,
		log:      log,
		adapters: make(map[string]model.ChainAdapter),
	}

	ids := enabledChainIDs(cfg)
	for _, id := range ids {
		chCfg, ok := cfg.Chains[id]
		if !ok {
			f.skipped = append(f.skipped, id)
			log.Warn("chain config not found, skipped", zap.String("chain", id))
			continue
		}
		if !chCfg.Supported {
			f.skipped = append(f.skipped, id)
			log.Info("chain disabled by config, skipped", zap.String("chain", id))
			continue
		}
		ctor, ok := lookup(id)
		if !ok {
			f.skipped = append(f.skipped, id)
			log.Warn("chain adapter not registered, skipped", zap.String("chain", id),
				zap.Strings("registered", Registered()))
			continue
		}
		adapter, err := ctor(cfg, chCfg, log)
		if err != nil {
			f.skipped = append(f.skipped, id)
			log.Error("build chain adapter failed, skipped", zap.String("chain", id), zap.Error(err))
			continue
		}
		f.adapters[id] = adapter
	}

	if len(f.adapters) == 0 {
		log.Warn("no chain adapter available, running in degraded mode")
	}
	return f, nil
}

func enabledChainIDs(cfg *config.Config) []string {
	ids := make([]string, 0, len(cfg.Chains))
	if len(cfg.EnabledList) > 0 {
		for _, id := range cfg.EnabledList {
			ids = append(ids, strings.ToLower(strings.TrimSpace(id)))
		}
		return ids
	}
	for id := range cfg.Chains {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Get 按链 ID 获取适配器。
func (f *Factory) Get(chainID string) (model.ChainAdapter, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	a, ok := f.adapters[strings.ToLower(chainID)]
	if !ok {
		return nil, fmt.Errorf("unsupported or unavailable chain: %s", chainID)
	}
	return a, nil
}

// List 已就绪的链 ID。
func (f *Factory) List() []string {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]string, 0, len(f.adapters))
	for k := range f.adapters {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Skipped 因未注册/构建失败/配置关闭而跳过的链。
func (f *Factory) Skipped() []string {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]string, len(f.skipped))
	copy(out, f.skipped)
	return out
}

// Len 已就绪链数量。
func (f *Factory) Len() int {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return len(f.adapters)
}
