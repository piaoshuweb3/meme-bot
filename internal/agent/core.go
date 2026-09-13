package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"go.uber.org/zap"
)

// Plan 是 Agent 生成的行动计划。
type Plan struct {
	Thought string     `json:"thought"`
	Actions []ToolCall `json:"actions"`
}

// MemoryStore 记忆存储（短期会话 + 长期结构化记忆）。
type MemoryStore interface {
	Save(ctx context.Context, agentName, scope string, content string) error
	Recall(ctx context.Context, agentName, scope string, limit int) ([]string, error)
}

// Memory 是 MemoryStore 的内存实现。
type Memory struct {
	mu   sync.RWMutex
	data map[string][]string
}

// NewMemory 构建内存记忆。
func NewMemory() *Memory { return &Memory{data: make(map[string][]string)} }

// Save 实现 MemoryStore。
func (m *Memory) Save(_ context.Context, agentName, scope, content string) error {
	key := agentName + "|" + scope
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[key] = append(m.data[key], content)
	if len(m.data[key]) > 500 {
		m.data[key] = m.data[key][len(m.data[key])-500:]
	}
	return nil
}

// Recall 实现 MemoryStore。
func (m *Memory) Recall(_ context.Context, agentName, scope string, limit int) ([]string, error) {
	key := agentName + "|" + scope
	m.mu.RLock()
	defer m.mu.RUnlock()
	all := m.data[key]
	if limit <= 0 || limit > len(all) {
		limit = len(all)
	}
	out := make([]string, 0, limit)
	out = append(out, all[len(all)-limit:]...)
	return out, nil
}

var _ MemoryStore = (*Memory)(nil)

// StepHook 在每一步之后被调用（用于落库与审计）。
type StepHook func(ctx context.Context, step int, msg Message, result string)

// Agent 是 ReAct 风格的核心循环。
type Agent struct {
	Name         string
	SystemPrompt string
	LLM          LLMClient
	Tools        *ToolRegistry
	Memory       MemoryStore
	MaxSteps     int
	Logger       *zap.Logger

	// PreActHook 在所有工具执行前调用（风控钩子：金额上限、频率、黑名单）。
	PreActHook func(ctx context.Context, call ToolCall) error
	// PostStepHook 用于审计留痕。
	PostStepHook StepHook
}

// New 构建 Agent。
func New(name, systemPrompt string, llm LLMClient, tools *ToolRegistry, memory MemoryStore, maxSteps int, log *zap.Logger) *Agent {
	if log == nil {
		log = zap.NewNop()
	}
	if memory == nil {
		memory = NewMemory()
	}
	if tools == nil {
		tools = NewToolRegistry()
	}
	if maxSteps <= 0 {
		maxSteps = 12
	}
	return &Agent{
		Name:         name,
		SystemPrompt: systemPrompt,
		LLM:          llm,
		Tools:        tools,
		Memory:       memory,
		MaxSteps:     maxSteps,
		Logger:       log,
	}
}

// Run 执行一次任务，返回最终回答。
func (a *Agent) Run(ctx context.Context, userInput string) (string, error) {
	if a.LLM == nil {
		return "", errors.New("agent: LLM client 未配置")
	}

	messages := []Message{
		{Role: "system", Content: a.SystemPrompt},
	}
	if a.Memory != nil {
		if recalled, err := a.Memory.Recall(ctx, a.Name, "default", 10); err == nil && len(recalled) > 0 {
			messages = append(messages, Message{
				Role:    "system",
				Content: "历史记忆（最近 10 条，仅供参考）：\n- " + joinLines(recalled),
			})
		}
	}
	messages = append(messages, Message{Role: "user", Content: userInput})

	for step := 0; step < a.MaxSteps; step++ {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}

		resp, err := a.LLM.Chat(ctx, messages, a.Tools.Schemas())
		if err != nil {
			return "", fmt.Errorf("agent: llm chat: %w", err)
		}
		messages = append(messages, *resp)

		// 没有工具调用 → 任务结束
		if len(resp.ToolCalls) == 0 {
			if a.Memory != nil {
				_ = a.Memory.Save(ctx, a.Name, "default",
					fmt.Sprintf("%s → %s", clip(userInput, 120), clip(resp.Content, 200)))
			}
			return resp.Content, nil
		}

		// 执行工具
		for _, call := range resp.ToolCalls {
			if a.PreActHook != nil {
				if err := a.PreActHook(ctx, call); err != nil {
					result := "风控拒绝：" + err.Error()
					messages = append(messages, Message{Role: "tool", Name: call.Name, ToolCallID: call.ID, Content: result})
					if a.PostStepHook != nil {
						a.PostStepHook(ctx, step, *resp, result)
					}
					continue
				}
			}
			result, err := a.Tools.Execute(ctx, call)
			if err != nil {
				result = "工具执行失败：" + err.Error()
			}
			a.Logger.Debug("tool executed",
				zap.String("agent", a.Name), zap.String("tool", call.Name), zap.Int("step", step))

			messages = append(messages, Message{Role: "tool", Name: call.Name, ToolCallID: call.ID, Content: result})
			if a.PostStepHook != nil {
				a.PostStepHook(ctx, step, *resp, result)
			}
		}
	}
	return "", fmt.Errorf("agent: 超过最大步数 %d", a.MaxSteps)
}

// RunWithPlan 让 LLM 输出结构化计划（Plan），不直接执行工具。
//
// 用于“生成计划 → 人工确认 → 执行”的安全模式。
func (a *Agent) RunWithPlan(ctx context.Context, userInput string) (*Plan, error) {
	if a.LLM == nil {
		return nil, errors.New("agent: LLM client 未配置")
	}
	prompt := a.SystemPrompt + "\n\n请只输出 JSON（不要额外文本），格式：{\"thought\":\"...\",\"actions\":[{\"id\":\"1\",\"name\":\"工具名\",\"arguments\":{}}]}"
	messages := []Message{
		{Role: "system", Content: prompt},
		{Role: "user", Content: userInput},
	}
	resp, err := a.LLM.Chat(ctx, messages, nil)
	if err != nil {
		return nil, err
	}
	plan := &Plan{}
	if err := json.Unmarshal([]byte(extractJSON(resp.Content)), plan); err != nil {
		return nil, fmt.Errorf("agent: 解析计划失败: %w (原始内容: %s)", err, clip(resp.Content, 200))
	}
	return plan, nil
}

// ToolGuard 生成一个简单的风控钩子：校验金额上限与工具白名单。
func ToolGuard(maxAmountUSD float64, allowTools map[string]bool) func(context.Context, ToolCall) error {
	return func(_ context.Context, call ToolCall) error {
		if len(allowTools) > 0 && !allowTools[call.Name] {
			return fmt.Errorf("工具 %s 不在白名单内", call.Name)
		}
		var args map[string]any
		if len(call.Arguments) > 0 {
			_ = json.Unmarshal(call.Arguments, &args)
		}
		if maxAmountUSD > 0 {
			for _, key := range []string{"amount_usd", "amountUsd", "max_usd"} {
				if v, ok := args[key]; ok {
					if f, ok := v.(float64); ok && f > maxAmountUSD {
						return fmt.Errorf("请求金额 %.2f 超过上限 %.2f", f, maxAmountUSD)
					}
				}
			}
		}
		return nil
	}
}

func joinLines(items []string) string {
	var b strings.Builder
	for i, s := range items {
		if i > 0 {
			b.WriteString("\n- ")
		}
		b.WriteString(s)
	}
	return b.String()
}

// extractJSON 从模型输出中提取 JSON 片段（容忍 ```json 包裹与前后解释文本）。
func extractJSON(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "```json", "")
	s = strings.ReplaceAll(s, "```", "")
	s = strings.TrimSpace(s)

	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start >= 0 && end > start {
		return s[start : end+1]
	}
	return s
}
