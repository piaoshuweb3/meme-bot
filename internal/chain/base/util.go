package base

import (
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
)

// decodeHexTx 解析 0x 前缀的原始交易 hex。
func decodeHexTx(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "0x")
	s = strings.TrimPrefix(s, "0X")
	if s == "" {
		return nil, fmt.Errorf("base: empty raw tx")
	}
	raw, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("base: invalid hex raw tx: %w", err)
	}
	return raw, nil
}

// maskURL 脱敏 RPC URL：隐藏 query 与形如 /v2/<API_KEY> 的路径片段，
// 避免日志/告警泄露密钥（安全基线要求）。
func maskURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "<invalid-url>"
	}
	if u.RawQuery != "" {
		u.RawQuery = "***"
	}
	if u.Path != "" {
		segs := strings.Split(u.Path, "/")
		for i, seg := range segs {
			if len(seg) >= 24 {
				segs[i] = "***"
			}
		}
		u.Path = strings.Join(segs, "/")
	}
	if u.User != nil {
		u.User = url.User("***")
	}
	return u.String()
}
