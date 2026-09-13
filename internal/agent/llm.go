// Package agent 实现 LLM Agent 层：Observe → Reason → Plan → Act → Reflect 循环、
// 工具注册表、记忆与执行前风控钩子。
//
// 定位（对齐文书）：Agent 负责“逻辑密集型、非抢速度”的任务（空投路径规划、多签、
// 低频流动性优化等），所有 Act 必须经过风控层，默认需要人工确认。
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"meme-bot/internal/config"
)

// Message 是 Agent 与 LLM 之间的消息。
type Message struct {
	Role      string     `json:"role"` // system | user | assistant | tool
	Content   string     `json:"content,omitempty"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	// ToolCallID 在 role=tool 时必填，对应触发该结果的调用
	ToolCallID string `json:"tool_call_id,omitempty"`
	Name       string `json:"name,omitempty"`
}

// ToolCall 是 LLM 请求调用的工具。
type ToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// ToolSchema 工具描述（JSON Schema）。
type ToolSchema struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// LLMClient 抽象，便于在 OpenAI / 兼容网关 / 本地模型之间切换。
type LLMClient interface {
	Chat(ctx context.Context, messages []Message, tools []ToolSchema) (*Message, error)
}

// OpenAIClient 通过 OpenAI 兼容的 /chat/completions 接口调用模型。
type OpenAIClient struct {
	apiKey  string
	baseURL string
	model   string
	http    *http.Client
}

// NewOpenAIClient 依据配置构建客户端。
func NewOpenAIClient(cfg config.AgentConfig) (*OpenAIClient, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, errors.New("agent: LLM API Key 未配置（设置 MEMEBOT_AGENT_API_KEY）")
	}
	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	model := cfg.Model
	if model == "" {
		model = "gpt-4o-mini"
	}
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	return &OpenAIClient{
		apiKey:  cfg.APIKey,
		baseURL: base,
		model:   model,
		http:    &http.Client{Timeout: timeout},
	}, nil
}

type chatRequest struct {
	Model      string        `json:"model"`
	Messages   []chatMessage `json:"messages"`
	Tools      []chatTool    `json:"tools,omitempty"`
	ToolChoice string        `json:"tool_choice,omitempty"`
}

type chatMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content,omitempty"`
	ToolCalls  []chatToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	Name       string         `json:"name,omitempty"`
}

type chatTool struct {
	Type     string       `json:"type"`
	Function chatFunction `json:"function"`
}

type chatFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type chatToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
	Usage struct {
		TotalTokens int `json:"total_tokens"`
	} `json:"usage"`
}

// Chat 实现 LLMClient。
func (c *OpenAIClient) Chat(ctx context.Context, messages []Message, tools []ToolSchema) (*Message, error) {
	reqBody := chatRequest{Model: c.model}
	for _, m := range messages {
		cm := chatMessage{Role: m.Role, Content: m.Content, ToolCallID: m.ToolCallID, Name: m.Name}
		for _, tc := range m.ToolCalls {
			var ctc chatToolCall
			ctc.ID = tc.ID
			ctc.Type = "function"
			ctc.Function.Name = tc.Name
			ctc.Function.Arguments = string(tc.Arguments)
			cm.ToolCalls = append(cm.ToolCalls, ctc)
		}
		reqBody.Messages = append(reqBody.Messages, cm)
	}
	for _, t := range tools {
		reqBody.Tools = append(reqBody.Tools, chatTool{
			Type: "function",
			Function: chatFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			},
		})
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("agent: llm http %d: %s", resp.StatusCode, clip(string(raw), 300))
	}

	var out chatResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("agent: decode llm response: %w", err)
	}
	if out.Error != nil {
		return nil, fmt.Errorf("agent: llm error: %s", out.Error.Message)
	}
	if len(out.Choices) == 0 {
		return nil, errors.New("agent: llm 返回空结果")
	}

	msg := &Message{Role: "assistant", Content: out.Choices[0].Message.Content}
	for _, tc := range out.Choices[0].Message.ToolCalls {
		msg.ToolCalls = append(msg.ToolCalls, ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: json.RawMessage(defaultArgs(tc.Function.Arguments)),
		})
	}
	return msg, nil
}

func defaultArgs(s string) string {
	if strings.TrimSpace(s) == "" {
		return "{}"
	}
	return s
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

var _ LLMClient = (*OpenAIClient)(nil)
