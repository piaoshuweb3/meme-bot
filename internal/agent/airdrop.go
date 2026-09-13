package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	"meme-bot/internal/config"
	"meme-bot/internal/model"
)

// AirdropTask 空投任务（Agent 的场景化状态机）。
type AirdropTask struct {
	Project     string     `json:"project"`
	Chain       string     `json:"chain"`
	TaskKey     string     `json:"task_key"`
	Description string     `json:"description,omitempty"`
	SnapshotAt  *time.Time `json:"snapshot_at,omitempty"`
	DeadlineAt  *time.Time `json:"deadline_at,omitempty"`
	Status      string     `json:"status"`
}

// AirdropSource 空投数据源（由 provider 或自建服务实现）。
type AirdropSource interface {
	Tasks(ctx context.Context, project string) ([]AirdropTask, error)
	Eligibility(ctx context.Context, project, wallet string) (bool, string, error)
}

// AirdropDeps 空投 Agent 的依赖。
type AirdropDeps struct {
	Source  AirdropSource
	Market  model.MarketPort
	Signer  model.SwapSigner
	Risk    model.RiskPort
	ChainID string
	Logger  *zap.Logger
}

// NewAirdropTools 注册空投场景的内置工具。
func NewAirdropTools(deps AirdropDeps) *ToolRegistry {
	reg := NewToolRegistry()

	reg.MustRegister(Tool{
		Schema: ToolSchema{
			Name:        "get_airdrop_tasks",
			Description: "获取指定项目的空投任务列表与快照时间",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"project":{"type":"string"}},"required":["project"]}`),
		},
		Fn: func(ctx context.Context, args json.RawMessage) (string, error) {
			var in struct {
				Project string `json:"project"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return "", err
			}
			if deps.Source == nil {
				return "", fmt.Errorf("未配置空投数据源")
			}
			tasks, err := deps.Source.Tasks(ctx, in.Project)
			if err != nil {
				return "", err
			}
			out, _ := json.Marshal(tasks)
			return string(out), nil
		},
	})

	reg.MustRegister(Tool{
		Schema: ToolSchema{
			Name:        "check_wallet_eligibility",
			Description: "检查指定钱包在某空投项目中的资格",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"project":{"type":"string"},"wallet":{"type":"string"}},"required":["project","wallet"]}`),
		},
		Fn: func(ctx context.Context, args json.RawMessage) (string, error) {
			var in struct {
				Project string `json:"project"`
				Wallet  string `json:"wallet"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return "", err
			}
			if deps.Source == nil {
				return "", fmt.Errorf("未配置空投数据源")
			}
			ok, reason, err := deps.Source.Eligibility(ctx, in.Project, in.Wallet)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf(`{"eligible":%t,"reason":%q}`, ok, reason), nil
		},
	})

	reg.MustRegister(Tool{
		Schema: ToolSchema{
			Name:        "estimate_gas_and_path",
			Description: "估算某链上某代币交换的成本与最优路径（只读，不产生交易）",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"chain":{"type":"string"},"token_in":{"type":"string"},"token_out":{"type":"string"},"amount":{"type":"string"}},"required":["token_in","token_out","amount"]}`),
		},
		Fn: func(ctx context.Context, args json.RawMessage) (string, error) {
			var in struct {
				Chain    string `json:"chain"`
				TokenIn  string `json:"token_in"`
				TokenOut string `json:"token_out"`
				Amount   string `json:"amount"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return "", err
			}
			chainID := in.Chain
			if chainID == "" {
				chainID = deps.ChainID
			}
			if deps.Market == nil {
				return "", fmt.Errorf("未配置行情源")
			}
			price, err := deps.Market.TokenPrice(ctx, chainID, in.TokenIn)
			if err != nil {
				return "", err
			}
			liq, err := deps.Market.Liquidity(ctx, chainID, in.TokenIn)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf(`{"chain":%q,"price_usd":%g,"liquidity_usd":%g,"amount":%q,"note":"路径与滑点在执行前由聚合器实时计算"}`,
				chainID, price.USD, liq.LiquidityUSD, in.Amount), nil
		},
	})

	reg.MustRegister(Tool{
		Schema: ToolSchema{
			Name:        "request_human_confirm",
			Description: "请求人工确认（高风险操作必须调用，返回待确认状态）",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"action":{"type":"string"},"reason":{"type":"string"}},"required":["action"]}`),
		},
		Fn: func(_ context.Context, args json.RawMessage) (string, error) {
			var in struct {
				Action string `json:"action"`
				Reason string `json:"reason"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return "", err
			}
			return fmt.Sprintf(`{"status":"pending_human_confirm","action":%q,"reason":%q,"hint":"请在 Telegram / Dashboard 中确认后继续"}`,
				in.Action, in.Reason), nil
		},
	})

	reg.MustRegister(Tool{
		Schema: ToolSchema{
			Name:        "submit_private_tx",
			Description: "提交一笔链上动作（执行层会强制走私有通道并经过风控与金额上限校验）",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"chain":{"type":"string"},"token_in":{"type":"string"},"token_out":{"type":"string"},"amount_usd":{"type":"number"},"reason":{"type":"string"}},"required":["token_in","token_out","amount_usd"]}`),
		},
		Fn: func(ctx context.Context, args json.RawMessage) (string, error) {
			var in struct {
				Chain     string  `json:"chain"`
				TokenIn   string  `json:"token_in"`
				TokenOut  string  `json:"token_out"`
				AmountUSD float64 `json:"amount_usd"`
				Reason    string  `json:"reason"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return "", err
			}
			if deps.Risk == nil {
				return "", fmt.Errorf("未配置风控引擎，拒绝执行")
			}
			chainID := in.Chain
			if chainID == "" {
				chainID = deps.ChainID
			}
			decision, err := deps.Risk.CheckEntry(model.EntryRequest{
				Chain:       chainID,
				Token:       in.TokenOut,
				Source:      model.SourceAgent,
				AmountUSD:   in.AmountUSD,
				RequestedAt: time.Now().UTC(),
			})
			if err != nil {
				return "", err
			}
			if decision.Verdict == model.VerdictReject {
				return "", fmt.Errorf("风控拒绝：%s", decision.Reason)
			}
			// 真正的签名与广播由执行层完成（避免 Agent 直接触碰私钥/交易构造）
			return fmt.Sprintf(`{"status":"approved_pending_execution","verdict":%q,"allowed_usd":%g,"slippage_bps":%d,"split_entries":%d}`,
				decision.Verdict, decision.AllowedUSD, decision.SlippageBps, decision.SplitEntries), nil
		},
	})

	return reg
}

// AirdropSystemPrompt 空投 Agent 的系统提示词。
const AirdropSystemPrompt = `你是一个专业的空投监控与交互优化 Agent。

职责：
1. 监控多个空投项目的快照时间与任务进度
2. 计算最优交互路径（考虑 gas、依赖顺序、多账号）
3. 生成可执行计划，但最终执行必须经过风控与人工确认
4. 所有链上操作优先使用私有交易通道，避免 MEV

约束：
- 只输出结构化 JSON（计划）或明确的结论
- 不得建议任何超出用户授权范围的操作
- 涉及真实资金的动作，必须先调用 request_human_confirm

输出必须结构化，便于系统解析。`

// NewAirdropAgent 构建空投监控 Agent。
func NewAirdropAgent(cfg config.AgentConfig, deps AirdropDeps) (*Agent, error) {
	llm, err := NewOpenAIClient(cfg)
	if err != nil {
		return nil, err
	}
	log := deps.Logger
	if log == nil {
		log = zap.NewNop()
	}
	tools := NewAirdropTools(deps)
	a := New("airdrop_monitor", AirdropSystemPrompt, llm, tools, NewMemory(), cfg.MaxSteps, log)

	// 风控钩子：工具白名单 + 金额上限（Agent 沙箱）
	allow := map[string]bool{}
	for _, name := range tools.Names() {
		allow[name] = true
	}
	a.PreActHook = ToolGuard(cfg.MaxActionUSD, allow)
	return a, nil
}

// ValidatePlan 校验 Agent 生成的计划（工具存在性 + 必填参数）。
func ValidatePlan(plan *Plan, reg *ToolRegistry) error {
	if plan == nil {
		return fmt.Errorf("agent: nil plan")
	}
	if len(plan.Actions) == 0 {
		return fmt.Errorf("agent: 计划为空")
	}
	known := map[string]bool{}
	for _, n := range reg.Names() {
		known[n] = true
	}
	for i, act := range plan.Actions {
		if act.Name == "" {
			return fmt.Errorf("agent: 第 %d 个动作缺少工具名", i+1)
		}
		if !known[act.Name] {
			return fmt.Errorf("agent: 第 %d 个动作使用了未知工具 %s", i+1, act.Name)
		}
		if len(act.Arguments) > 0 && !json.Valid(act.Arguments) {
			return fmt.Errorf("agent: 第 %d 个动作的参数不是合法 JSON", i+1)
		}
	}
	return nil
}

// PlanRequiresHumanConfirm 判断计划是否包含高风险动作。
func PlanRequiresHumanConfirm(plan *Plan) bool {
	if plan == nil {
		return true
	}
	for _, act := range plan.Actions {
		if strings.EqualFold(act.Name, "submit_private_tx") {
			return true
		}
	}
	return false
}
