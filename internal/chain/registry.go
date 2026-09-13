// Package chain 提供多链抽象：注册表 + 工厂 + 行情聚合。
//
// 扩展新链的完整成本：
//  1. 新建 internal/chain/<name> 包，实现 model.ChainAdapter；
//  2. 在包内 init() 中调用 chain.Register("<name>", New)；
//  3. 在 configs/chains.yaml 增加该链参数，并把 supported 置为 true。
//
// 上层策略与执行层零修改。
package chain

import (
	"sort"
	"sync"

	"go.uber.org/zap"

	"meme-bot/internal/config"
	"meme-bot/internal/model"
)

// Constructor 链适配器构造函数。
type Constructor func(cfg *config.Config, ch config.Chain, log *zap.Logger) (model.ChainAdapter, error)

var (
	registryMu sync.RWMutex
	registry   = make(map[string]Constructor)
)

// Register 注册链适配器构造器（并发安全，通常在包 init() 中调用）。
func Register(name string, c Constructor) {
	if name == "" || c == nil {
		return
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[name] = c
}

// Registered 返回已注册的链名（排序）。
func Registered() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]string, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func lookup(name string) (Constructor, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	c, ok := registry[name]
	return c, ok
}
