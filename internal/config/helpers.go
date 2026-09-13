package config

import "strings"

// AggregatorBaseURL 返回某链聚合器的 API 地址（未配置时返回空串，由聚合器客户端使用默认值）。
func (c *Config) AggregatorBaseURL(chainID string) string {
	if c == nil {
		return ""
	}
	ch, ok := c.Chains[strings.ToLower(strings.TrimSpace(chainID))]
	if !ok {
		return ""
	}
	return ch.AggregatorBaseURL
}

// AggregatorKeyEnv 返回某链聚合器 API Key 所在的环境变量名。
func (c *Config) AggregatorKeyEnv(chainID string) string {
	if c == nil {
		return ""
	}
	ch, ok := c.Chains[strings.ToLower(strings.TrimSpace(chainID))]
	if !ok {
		return ""
	}
	return ch.AggregatorAPIKeyEnv
}

// ChainIDs 返回配置中出现的全部链 ID（排序后）。
func (c *Config) ChainIDs() []string {
	if c == nil {
		return nil
	}
	out := make([]string, 0, len(c.Chains))
	for id := range c.Chains {
		out = append(out, id)
	}
	sortStrings(out)
	return out
}

// SecretFor 返回某链聚合器密钥的实际值（从环境变量读取，绝不落盘）。
func (c *Config) SecretFor(chainID string) string {
	return ResolveSecret(c.AggregatorKeyEnv(chainID))
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
