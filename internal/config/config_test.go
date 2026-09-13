package config

import "testing"

func baseCfg() *Config {
	return &Config{Chains: map[string]Chain{
		"base":         {ChainID: "base", RPCURLs: []string{"https://yaml.base"}, Supported: true},
		"base-sepolia": {ChainID: "base-sepolia", RPCURLs: []string{"https://yaml.sepolia"}},
	}}
}

func TestEnvRPCOverrideHyphenatedChain(t *testing.T) {
	t.Setenv("MEMEBOT_CHAINS_BASE_SEPOLIA_RPC_URLS", "https://a.example,https://b.example")
	cfg := baseCfg()
	applyEnvOverrides(cfg)

	ch := cfg.Chains["base-sepolia"]
	if len(ch.RPCURLs) != 2 || ch.RPCURLs[0] != "https://a.example" {
		t.Fatalf("连字符链名（base-sepolia → BASE_SEPOLIA）的 RPC 覆盖未生效: %v", ch.RPCURLs)
	}
	if got := cfg.Chains["base"].RPCURLs; len(got) != 1 || got[0] != "https://yaml.base" {
		t.Fatalf("未被覆盖的链不应改动: %v", got)
	}
	if len(cfg.EnvRPCChains) != 1 || cfg.EnvRPCChains[0] != "base-sepolia" {
		t.Fatalf("EnvRPCChains 记录错误: %v", cfg.EnvRPCChains)
	}
}

func TestEnvRPCSeparatorsAndScalarFields(t *testing.T) {
	t.Setenv("MEMEBOT_CHAINS_BASE_RPC_URLS", "https://a.example   https://b.example;https://c.example")
	t.Setenv("MEMEBOT_CHAINS_BASE_CONFIRMATIONS", "24")
	t.Setenv("MEMEBOT_CHAINS_BASE_MIN_LIQUIDITY_USD", "12345.5")
	t.Setenv("MEMEBOT_CHAINS_BASE_AGGREGATOR_BASE_URL", "https://api.example/v6.0")
	t.Setenv("MEMEBOT_CHAINS_BASE_AGGREGATOR_API_KEY", "secret-value-must-not-be-stored")
	t.Setenv("MEMEBOT_CHAINS_BASE_SUPPORTED", "false")

	cfg := baseCfg()
	applyEnvOverrides(cfg)
	ch := cfg.Chains["base"]

	if len(ch.RPCURLs) != 3 {
		t.Fatalf("空格/分号分隔解析失败: %v", ch.RPCURLs)
	}
	if ch.Confirmations != 24 {
		t.Fatalf("CONFIRMATIONS 覆盖失败: %d", ch.Confirmations)
	}
	if ch.MinLiquidityUSD != 12345.5 {
		t.Fatalf("MIN_LIQUIDITY_USD 覆盖失败: %v", ch.MinLiquidityUSD)
	}
	if ch.AggregatorBaseURL != "https://api.example/v6.0" {
		t.Fatalf("AGGREGATOR_BASE_URL 覆盖失败: %q", ch.AggregatorBaseURL)
	}
	if ch.AggregatorAPIKeyEnv != "MEMEBOT_CHAINS_BASE_AGGREGATOR_API_KEY" {
		t.Fatalf("密钥应只记录环境变量名，实际 %q", ch.AggregatorAPIKeyEnv)
	}
	if ch.Supported {
		t.Fatal("SUPPORTED=false 未生效")
	}
}

func TestEnvCreatesUnknownChainButNotTrading(t *testing.T) {
	t.Setenv("MEMEBOT_CHAINS_POLYGON_RPC_URLS", "https://polygon.example")
	t.Setenv("MEMEBOT_CHAINS_POLYGON_CHAIN_ID", "polygon")
	t.Setenv("MEMEBOT_CHAINS_POLYGON_EVM_CHAIN_ID", "137")

	cfg := baseCfg()
	applyEnvOverrides(cfg)

	ch, ok := cfg.Chain("polygon")
	if !ok {
		t.Fatal("应据环境变量新建链条目")
	}
	if ch.EVMChainID != 137 || len(ch.RPCURLs) != 1 {
		t.Fatalf("新链条目字段错误: %+v", ch)
	}
	if ch.Supported {
		t.Fatal("新建链默认不应参与交易监控（防误下单）")
	}
}

func TestEnvUnknownWithoutChainIDIsIgnored(t *testing.T) {
	t.Setenv("MEMEBOT_CHAINS_MYSTERY_RPC_URLS", "https://x.example")
	cfg := baseCfg()
	applyEnvOverrides(cfg)

	if _, ok := cfg.Chains["mystery"]; ok {
		t.Fatal("缺少 CHAIN_ID 时不应凭空建链（段名无法反推规范链名）")
	}
	if _, ok := cfg.Chains["MYSTERY"]; ok {
		t.Fatal("不应建出异常链名")
	}
}

func TestEnvInvalidValuesKeepOriginals(t *testing.T) {
	t.Setenv("MEMEBOT_CHAINS_BASE_CONFIRMATIONS", "not-a-number")
	t.Setenv("MEMEBOT_CHAINS_BASE_SUPPORTED", "maybe")

	cfg := baseCfg()
	cfg.Chains["base"] = Chain{ChainID: "base", Confirmations: 7, Supported: true}
	applyEnvOverrides(cfg)

	ch := cfg.Chains["base"]
	if ch.Confirmations != 7 || !ch.Supported {
		t.Fatalf("非法值应保留原值: confirmations=%d supported=%v", ch.Confirmations, ch.Supported)
	}
}

func TestEnvChainSegment(t *testing.T) {
	cases := map[string]string{
		"base":         "BASE",
		"base-sepolia": "BASE_SEPOLIA",
		"solana":       "SOLANA",
		"op.mainnet":   "OP_MAINNET",
	}
	for in, want := range cases {
		if got := envChainSegment(in); got != want {
			t.Fatalf("envChainSegment(%q) = %q, 期望 %q", in, got, want)
		}
	}
	if got := envChainKey("BASE_SEPOLIA", "RPC_URLS"); got != "MEMEBOT_CHAINS_BASE_SEPOLIA_RPC_URLS" {
		t.Fatalf("envChainKey 组装错误: %q", got)
	}
}

func TestSplitListAndParseBool(t *testing.T) {
	got := splitList(" a , b ; c   d ")
	if len(got) != 4 || got[0] != "a" || got[3] != "d" {
		t.Fatalf("splitList 解析错误: %v", got)
	}
	if n := len(splitList("   ")); n != 0 {
		t.Fatalf("空白串应返回空列表，实际 %d", n)
	}
	if !parseBool("YES", false) || parseBool("0", true) || !parseBool("maybe", true) {
		t.Fatal("parseBool 解析错误")
	}
}

func TestMaskURLNeverLeaksKey(t *testing.T) {
	cases := map[string]string{
		"https://base-sepolia.g.alchemy.com/v2/SECRETKEY": "https://base-sepolia.g.alchemy.com/***",
		"https://mainnet.base.org":                        "https://mainnet.base.org/***",
		"":                                                "",
	}
	for in, want := range cases {
		if got := MaskURL(in); got != want {
			t.Fatalf("MaskURL(%q) = %q, 期望 %q", in, got, want)
		}
	}
	if got := MaskURL("https://x.example/v2/SECRETKEY"); got == "https://x.example/v2/SECRETKEY" {
		t.Fatalf("MaskURL 泄漏了 path 中的密钥: %q", got)
	}
}

func TestLoadAppliesEnvRPCOverride(t *testing.T) {
	t.Setenv("MEMEBOT_AUTH_JWT_SECRET", "test-secret-0123456789abcdef")
	t.Setenv("MEMEBOT_CHAINS_BASE_SEPOLIA_RPC_URLS", "https://env-only.example")

	cfg, err := Load("../../configs/config.yaml")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	ch, ok := cfg.Chain("base-sepolia")
	if !ok {
		t.Fatal("base-sepolia 应来自 chains.yaml")
	}
	if len(ch.RPCURLs) != 1 || ch.RPCURLs[0] != "https://env-only.example" {
		t.Fatalf("Load 后 RPC 覆盖未生效: %v", ch.RPCURLs)
	}
	if len(cfg.EnvRPCChains) == 0 {
		t.Fatal("EnvRPCChains 应记录覆盖的链")
	}
}
