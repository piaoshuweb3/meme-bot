package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
)

// ToolFunc 是工具的执行函数。
type ToolFunc func(ctx context.Context, args json.RawMessage) (string, error)

// Tool 是工具定义（Schema + 实现）。
type Tool struct {
	Schema ToolSchema
	Fn     ToolFunc
}

// ToolRegistry 工具注册表（并发安全）。
type ToolRegistry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

// NewToolRegistry 构建注册表。
func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{tools: make(map[string]Tool)}
}

// Register 注册工具。
func (r *ToolRegistry) Register(t Tool) error {
	if t.Schema.Name == "" {
		return fmt.Errorf("agent: tool name is required")
	}
	if t.Fn == nil {
		return fmt.Errorf("agent: tool %s has no implementation", t.Schema.Name)
	}
	if len(t.Schema.Parameters) == 0 {
		t.Schema.Parameters = json.RawMessage(`{"type":"object","properties":{}}`)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[t.Schema.Name] = t
	return nil
}

// MustRegister 注册工具并在失败时 panic（用于内置工具装配）。
func (r *ToolRegistry) MustRegister(t Tool) {
	if err := r.Register(t); err != nil {
		panic(err)
	}
}

// Schemas 返回全部工具描述（按名称排序，保证提示词稳定）。
func (r *ToolRegistry) Schemas() []ToolSchema {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]ToolSchema, 0, len(names))
	for _, name := range names {
		out = append(out, r.tools[name].Schema)
	}
	return out
}

// Names 返回已注册工具名。
func (r *ToolRegistry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.tools))
	for name := range r.tools {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Execute 执行工具调用。
func (r *ToolRegistry) Execute(ctx context.Context, call ToolCall) (string, error) {
	r.mu.RLock()
	t, ok := r.tools[call.Name]
	r.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("agent: tool not found: %s", call.Name)
	}
	args := call.Arguments
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	return t.Fn(ctx, args)
}
