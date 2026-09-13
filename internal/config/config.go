// Package config 负责加载与校验 meme-bot 的全部配置。
//
// 配置来源优先级（后者覆盖前者）：
//  1. 内置默认值
//  2. configs/config.yaml + configs/chains.yaml
//  3. 环境变量（前缀 MEMEBOT_，点号转下划线，例如 MEMEBOT_SERVER_PORT）
//  4. 运行时注入（CreateChain 后的 env overrides）
//
// 安全基线：本包不落盘任何密钥；私钥/Token 只从环境变量读取。
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/joho/godotenv"
	"github.com/spf13/viper"
)

// 契约冻结说明：本文件是全局配置契约。字段增删需同步更新
// configs/config.yaml、.env.example 与 TECH-PLAN.md 的配置章节。
//
// EnvPrefix 是所有环境变量的统一前缀。
const EnvPrefix = "MEMEBOT"

// Mode 运行模式。
type Mode string

const (
	// ModeDryRun 只做模拟与告警，绝不发送真实链上交易（默认）。
	ModeDryRun Mode = "dry_run"
	// ModeLive 真实执行模式，需要显式配置并二次确认。
	ModeLive Mode = "live"
)

// Config 是应用配置的根对象。
type Config struct {
	Mode        Mode             `mapstructure:"mode"`
	Server      ServerConfig     `mapstructure:"server"`
	Database    DatabaseConfig   `mapstructure:"database"`
	Redis       RedisConfig      `mapstructure:"redis"`
	Telegram    TelegramConfig   `mapstructure:"telegram"`
	Score       ScoreConfig      `mapstructure:"score"`
	Signal      SignalConfig     `mapstructure:"signal"`
	Risk        RiskConfig       `mapstructure:"risk"`
	Alert       AlertConfig      `mapstructure:"alert"`
	Agent       AgentConfig      `mapstructure:"agent"`
	Metrics     MetricsConfig    `mapstructure:"metrics"`
	Auth        AuthConfig       `mapstructure:"auth"`
	EnabledList []string         `mapstructure:"enabled_chains"`
	Chains      map[string]Chain `mapstructure:"chains"`
	Providers   ProviderConfig   `mapstructure:"providers"`
	PrivateTx   PrivateTxConfig  `mapstructure:"private_tx"`
}

// ServerConfig HTTP 服务配置。
type ServerConfig struct {
	Port                   int    `mapstructure:"port"`
	GinMode                string `mapstructure:"gin_mode"`
	ReadTimeoutSeconds     int    `mapstructure:"read_timeout_seconds"`
	WriteTimeoutSeconds    int    `mapstructure:"write_timeout_seconds"`
	ShutdownTimeoutSeconds int    `mapstructure:"shutdown_timeout_seconds"`
}

// DatabaseConfig PostgreSQL 配置。
type DatabaseConfig struct {
	DSN                    string `mapstructure:"dsn"`
	MaxConns               int32  `mapstructure:"max_conns"`
	MinConns               int32  `mapstructure:"min_conns"`
	MaxConnLifetimeMinutes int    `mapstructure:"max_conn_lifetime_minutes"`
	AutoMigrate            bool   `mapstructure:"auto_migrate"`
}

// RedisConfig Redis 配置。
type RedisConfig struct {
	Addr     string `mapstructure:"addr"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`
}

// TelegramConfig Telegram 实时监控端配置。
type TelegramConfig struct {
	Enabled            bool   `mapstructure:"enabled"`
	Token              string `mapstructure:"token"`
	ChatID             int64  `mapstructure:"chat_id"`
	CriticalEscalation bool   `mapstructure:"critical_escalation"`
}

// TokenConfig 代币元数据配置。
type TokenConfig struct {
	Address  string `mapstructure:"address"`
	Symbol   string `mapstructure:"symbol"`
	Decimals uint8  `mapstructure:"decimals"`
}

// Chain 单链参数配置。
type Chain struct {
	ChainID                     string      `mapstructure:"chain_id"`
	EVMChainID                  int64       `mapstructure:"evm_chain_id"`
	NativeToken                 TokenConfig `mapstructure:"native_token"`
	RPCURLs                     []string    `mapstructure:"rpc_urls"`
	Aggregator                  string      `mapstructure:"aggregator"`
	AggregatorBaseURL           string      `mapstructure:"aggregator_base_url"`
	AggregatorAPIKeyEnv         string      `mapstructure:"aggregator_api_key_env"`
	FallbackRouters             []string    `mapstructure:"fallback_routers"`
	PrivateTxRPC                string      `mapstructure:"private_tx_rpc"`
	Confirmations               int         `mapstructure:"confirmations"`
	MinLiquidityUSD             float64     `mapstructure:"min_liquidity_usd"`
	MaxPriorityFeeMicroLamports uint64      `mapstructure:"max_priority_fee_micro_lamports"`
	Supported                   bool        `mapstructure:"supported"`
}

// ScoreConfig 地址评分参数。
type ScoreConfig struct {
	MinWinRate        float64 `mapstructure:"min_win_rate"`
	MinProfitFactor   float64 `mapstructure:"min_profit_factor"`
	MaxDrawdown       float64 `mapstructure:"max_drawdown"`
	MinTrades         int     `mapstructure:"min_trades"`
	ScoreThreshold    float64 `mapstructure:"score_threshold"`
	WindowDays        int     `mapstructure:"window_days"`
	WeightWinRate     float64 `mapstructure:"weight_win_rate"`
	WeightProfitFact  float64 `mapstructure:"weight_profit_factor"`
	WeightDrawdown    float64 `mapstructure:"weight_drawdown"`
	WeightConsistency float64 `mapstructure:"weight_consistency"`
	WeightRecency     float64 `mapstructure:"weight_recency"`
}

// SignalConfig 信号漏斗参数。
type SignalConfig struct {
	VolumeSpikeMultiple        float64 `mapstructure:"volume_spike_multiple"`
	LiquiditySpikeMultiple     float64 `mapstructure:"liquidity_spike_multiple"`
	TTLMinutes                 int     `mapstructure:"ttl_minutes"`
	FollowConfirmWindowMinutes int     `mapstructure:"follow_confirm_window_minutes"`
	LargeBuyMultiple           float64 `mapstructure:"large_buy_multiple"`
	CooldownMinutes            int     `mapstructure:"cooldown_minutes"`
}

// RiskConfig 风控参数。
type RiskConfig struct {
	MaxPositionPct           float64 `mapstructure:"max_position_pct"`
	MaxOpenPositions         int     `mapstructure:"max_open_positions"`
	StopLossPct              float64 `mapstructure:"stop_loss_pct"`
	TrailingStopPct          float64 `mapstructure:"trailing_stop_pct"`
	LiquidityDropTriggerPct  float64 `mapstructure:"liquidity_drop_trigger_pct"`
	DailyLossLimitPct        float64 `mapstructure:"daily_loss_limit_pct"`
	CooldownAfterLossMinutes int     `mapstructure:"cooldown_after_loss_minutes"`
	MaxSlippageBps           int     `mapstructure:"max_slippage_bps"`
	MaxFollowDelaySeconds    int     `mapstructure:"max_follow_delay_seconds"`
	SplitEntries             int     `mapstructure:"split_entries"`
	DryRunDefault            bool    `mapstructure:"dry_run_default"`
}

// AlertConfig 告警参数。
type AlertConfig struct {
	DedupWindowMinutes           int      `mapstructure:"dedup_window_minutes"`
	QuietHours                   []string `mapstructure:"quiet_hours"`
	SuppressAfterStopLossMinutes int      `mapstructure:"suppress_after_stop_loss_minutes"`
}

// AgentConfig LLM Agent 参数。
type AgentConfig struct {
	Enabled             bool    `mapstructure:"enabled"`
	Provider            string  `mapstructure:"provider"`
	Model               string  `mapstructure:"model"`
	BaseURL             string  `mapstructure:"base_url"`
	APIKey              string  `mapstructure:"api_key"`
	MaxSteps            int     `mapstructure:"max_steps"`
	TimeoutSeconds      int     `mapstructure:"timeout_seconds"`
	MaxActionUSD        float64 `mapstructure:"max_action_usd"`
	RequireHumanConfirm bool    `mapstructure:"require_human_confirm"`
}

// MetricsConfig 指标暴露配置。
type MetricsConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Path    string `mapstructure:"path"`
}

// AuthConfig 鉴权配置。
type AuthConfig struct {
	JWTSecret   string `mapstructure:"jwt_secret"`
	JWTTTLHours int    `mapstructure:"jwt_ttl_hours"`
}

// PrivateTxConfig 私有交易通道配置（跨链统一）。
type PrivateTxConfig struct {
	BaseRPC   string `mapstructure:"base_rpc"`
	SolanaRPC string `mapstructure:"solana_rpc"`
}

// ProviderConfig 外部数据源开关与密钥。
type ProviderConfig struct {
	GoPlusAPIKey      string `mapstructure:"goplus_api_key"`
	BirdeyeAPIKey     string `mapstructure:"birdeye_api_key"`
	MoralisAPIKey     string `mapstructure:"moralis_api_key"`
	BitqueryAPIKey    string `mapstructure:"bitquery_api_key"`
	DexScreenerEnable bool   `mapstructure:"dexscreener_enabled"`
}

// Load 读取配置。paths[0] 为 config.yaml 路径（默认 configs/config.yaml）。
func Load(paths ...string) (*Config, error) {
	cfgPath := "configs/config.yaml"
	if len(paths) > 0 && strings.TrimSpace(paths[0]) != "" {
		cfgPath = paths[0]
	}

	// .env 可选，缺失不算错误
	_ = godotenv.Load(".env")

	v := viper.New()
	v.SetConfigType("yaml")
	setDefaults(v)
	if _, err := os.Stat(cfgPath); err == nil {
		v.SetConfigFile(cfgPath)
		if err := v.ReadInConfig(); err != nil {
			return nil, fmt.Errorf("read config %s: %w", cfgPath, err)
		}
	}
	bindEnv(v)

	cfg := &Config{}
	if err := v.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}
	cfg.Mode = Mode(strings.ToLower(strings.TrimSpace(string(cfg.Mode))))
	if cfg.Mode == "" {
		cfg.Mode = ModeDryRun
	}

	// 链参数来自独立文件
	chainsPath := filepath.Join(filepath.Dir(cfgPath), "chains.yaml")
	if err := loadChains(chainsPath, cfg); err != nil {
		return nil, err
	}
	applyEnvOverrides(cfg)

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// LoadChainsFrom 允许测试直接注入链参数。
func LoadChainsFrom(path string, cfg *Config) error { return loadChains(path, cfg) }

func loadChains(path string, cfg *Config) error {
	if _, err := os.Stat(path); err != nil {
		return nil // 未提供链配置时允许以空表启动（降级模式）
	}
	cv := viper.New()
	cv.SetConfigFile(path)
	cv.SetConfigType("yaml")
	if err := cv.ReadInConfig(); err != nil {
		return fmt.Errorf("read chains config %s: %w", path, err)
	}
	if err := cv.Unmarshal(cfg); err != nil {
		return fmt.Errorf("unmarshal chains config: %w", err)
	}
	return nil
}

// Validate 校验配置合法性。
func (c *Config) Validate() error {
	if c.Mode != ModeDryRun && c.Mode != ModeLive {
		return fmt.Errorf("invalid mode %q (want dry_run|live)", c.Mode)
	}
	if c.Server.Port <= 0 || c.Server.Port > 65535 {
		return fmt.Errorf("invalid server.port %d", c.Server.Port)
	}
	if c.Score.MinTrades < 0 {
		return fmt.Errorf("score.min_trades 不能为负")
	}
	if c.Risk.MaxPositionPct <= 0 || c.Risk.MaxPositionPct > 1 {
		return fmt.Errorf("risk.max_position_pct 必须在 (0,1] 区间")
	}
	if c.Risk.MaxOpenPositions <= 0 {
		return fmt.Errorf("risk.max_open_positions 必须为正")
	}
	if c.Mode == ModeLive && c.Risk.DryRunDefault {
		// live 模式必须显式关闭 dry_run_default，防误开
		return fmt.Errorf("mode=live 时 risk.dry_run_default 必须为 false（安全门禁）")
	}
	return nil
}

// IsDryRun 是否处于模拟模式。
func (c *Config) IsDryRun() bool {
	if c.Mode == ModeLive {
		return c.Risk.DryRunDefault
	}
	return true
}

// Chain 按链 ID 取配置。
func (c *Config) Chain(id string) (Chain, bool) {
	ch, ok := c.Chains[strings.ToLower(id)]
	return ch, ok
}

// ResolveSecret 从环境变量取密钥；找不到时返回空串（调用方决定是否必需）。
func ResolveSecret(envName string) string {
	if envName == "" {
		return ""
	}
	return strings.TrimSpace(os.Getenv(envName))
}

func setDefaults(v *viper.Viper) {
	v.SetDefault("mode", string(ModeDryRun))

	v.SetDefault("server.port", 8080)
	v.SetDefault("server.gin_mode", "release")
	v.SetDefault("server.read_timeout_seconds", 15)
	v.SetDefault("server.write_timeout_seconds", 30)
	v.SetDefault("server.shutdown_timeout_seconds", 10)

	v.SetDefault("database.dsn", "postgres://bot:botpass@localhost:5432/smartmoney?sslmode=disable")
	v.SetDefault("database.max_conns", 10)
	v.SetDefault("database.min_conns", 2)
	v.SetDefault("database.max_conn_lifetime_minutes", 30)

	v.SetDefault("redis.addr", "localhost:6379")
	v.SetDefault("redis.db", 0)

	v.SetDefault("telegram.enabled", false)
	v.SetDefault("telegram.chat_id", 0)

	v.SetDefault("score.min_win_rate", 0.55)
	v.SetDefault("score.min_profit_factor", 1.8)
	v.SetDefault("score.max_drawdown", 0.40)
	v.SetDefault("score.min_trades", 20)
	v.SetDefault("score.score_threshold", 0.70)
	v.SetDefault("score.window_days", 90)
	v.SetDefault("score.weight_win_rate", 0.30)
	v.SetDefault("score.weight_profit_factor", 0.25)
	v.SetDefault("score.weight_drawdown", 0.20)
	v.SetDefault("score.weight_consistency", 0.15)
	v.SetDefault("score.weight_recency", 0.10)

	v.SetDefault("signal.volume_spike_multiple", 6.0)
	v.SetDefault("signal.liquidity_spike_multiple", 5.0)
	v.SetDefault("signal.ttl_minutes", 30)
	v.SetDefault("signal.follow_confirm_window_minutes", 30)
	v.SetDefault("signal.large_buy_multiple", 2.0)
	v.SetDefault("signal.cooldown_minutes", 20)

	v.SetDefault("risk.max_position_pct", 0.08)
	v.SetDefault("risk.max_open_positions", 4)
	v.SetDefault("risk.stop_loss_pct", 0.25)
	v.SetDefault("risk.trailing_stop_pct", 0.20)
	v.SetDefault("risk.liquidity_drop_trigger_pct", 0.35)
	v.SetDefault("risk.daily_loss_limit_pct", 0.15)
	v.SetDefault("risk.cooldown_after_loss_minutes", 120)
	v.SetDefault("risk.max_slippage_bps", 150)
	v.SetDefault("risk.max_follow_delay_seconds", 90)
	v.SetDefault("risk.split_entries", 3)
	v.SetDefault("risk.dry_run_default", true)

	v.SetDefault("alert.dedup_window_minutes", 5)
	v.SetDefault("alert.suppress_after_stop_loss_minutes", 60)

	v.SetDefault("agent.enabled", false)
	v.SetDefault("agent.provider", "openai")
	v.SetDefault("agent.model", "gpt-4o-mini")
	v.SetDefault("agent.max_steps", 12)
	v.SetDefault("agent.timeout_seconds", 60)
	v.SetDefault("agent.max_action_usd", 200)
	v.SetDefault("agent.require_human_confirm", true)

	v.SetDefault("metrics.enabled", true)
	v.SetDefault("metrics.path", "/metrics")

	v.SetDefault("auth.jwt_ttl_hours", 168)

	v.SetDefault("private_tx.base_rpc", "")
	v.SetDefault("private_tx.solana_rpc", "")

	v.SetDefault("providers.dexscreener_enabled", true)
}

// bindEnv 显式绑定需要环境变量覆盖的键。
// viper 的 AutomaticEnv 对 Unmarshal 不可靠，因此这里逐项绑定。
func bindEnv(v *viper.Viper) {
	v.SetEnvPrefix(EnvPrefix)
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	keys := []string{
		"mode",
		"server.port", "server.gin_mode",
		"database.dsn",
		"redis.addr", "redis.password", "redis.db",
		"telegram.enabled", "telegram.token", "telegram.chat_id",
		"metrics.enabled", "metrics.path",
		"auth.jwt_secret", "auth.jwt_ttl_hours",
		"agent.enabled", "agent.provider", "agent.model", "agent.base_url", "agent.api_key",
		"agent.max_steps", "agent.max_action_usd", "agent.require_human_confirm",
		"private_tx.base_rpc", "private_tx.solana_rpc",
		"providers.goplus_api_key", "providers.birdeye_api_key",
		"providers.moralis_api_key", "providers.bitquery_api_key",
		"providers.dexscreener_enabled",
	}
	for _, k := range keys {
		_ = v.BindEnv(k)
	}
}

// applyEnvOverrides 处理链级别的环境变量覆盖（RPC 列表、私钥占位）。
func applyEnvOverrides(cfg *Config) {
	for id, ch := range cfg.Chains {
		upper := strings.ToUpper(id)
		if v := ResolveSecret(EnvPrefix + "_CHAINS_" + upper + "_RPC_URLS"); v != "" {
			ch.RPCURLs = splitCSV(v)
		}
		if v := ResolveSecret(EnvPrefix + "_PRIVATE_TX_" + upper + "_RPC"); v != "" {
			ch.PrivateTxRPC = v
		}
		cfg.Chains[id] = ch
	}
	if v := ResolveSecret(EnvPrefix + "_PRIVATE_TX_BASE_RPC"); v != "" {
		cfg.PrivateTx.BaseRPC = v
	}
	if v := ResolveSecret(EnvPrefix + "_PRIVATE_TX_SOLANA_RPC"); v != "" {
		cfg.PrivateTx.SolanaRPC = v
	}
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}
