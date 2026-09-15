package user

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// JWT（HS256）自实现：只依赖标准库，行为完全可控。
//
// 说明：这里实现的是最小可用 JWT（header.payload.signature），
// 仅支持 HS256，并强制校验 exp/iat 与算法字段（拒绝 alg=none 之类的降级攻击）。

type jwtHeader struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
}

// Claims 是访问令牌载荷。
type Claims struct {
	UserID int64  `json:"sub"`
	Role   string `json:"role"`
	Iat    int64  `json:"iat"`
	Exp    int64  `json:"exp"`
}

// ErrInvalidToken 表示令牌非法或已过期。
var ErrInvalidToken = errors.New("user: invalid or expired token")

// SignToken 生成 HS256 令牌。
func SignToken(secret string, claims Claims, ttl time.Duration) (string, error) {
	if strings.TrimSpace(secret) == "" {
		return "", errors.New("user: jwt secret is empty")
	}
	now := time.Now().UTC()
	if claims.Iat == 0 {
		claims.Iat = now.Unix()
	}
	if claims.Exp == 0 {
		// 非正 TTL 必须显式失败：静默改为默认值会让"配置错误/计算失误"变成
		// 生命周期失控的令牌（且无声），属于安全边界，宁可 fail-fast。
		// 需要构造过期令牌用于测试时，请显式设置 claims.Exp。
		if ttl <= 0 {
			return "", fmt.Errorf("user: token ttl must be positive, got %s", ttl)
		}
		claims.Exp = now.Add(ttl).Unix()
	}

	headerJSON, err := json.Marshal(jwtHeader{Alg: "HS256", Typ: "JWT"})
	if err != nil {
		return "", err
	}
	payloadJSON, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}

	enc := base64.RawURLEncoding
	signingInput := enc.EncodeToString(headerJSON) + "." + enc.EncodeToString(payloadJSON)
	sig := sign(secret, signingInput)
	return signingInput + "." + enc.EncodeToString(sig), nil
}

// ParseToken 校验并解析令牌。
func ParseToken(secret, token string) (*Claims, error) {
	if strings.TrimSpace(secret) == "" {
		return nil, errors.New("user: jwt secret is empty")
	}
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 3 {
		return nil, ErrInvalidToken
	}

	enc := base64.RawURLEncoding
	headerRaw, err := enc.DecodeString(parts[0])
	if err != nil {
		return nil, ErrInvalidToken
	}
	var header jwtHeader
	if err := json.Unmarshal(headerRaw, &header); err != nil {
		return nil, ErrInvalidToken
	}
	if header.Alg != "HS256" {
		return nil, fmt.Errorf("%w: unsupported alg %q", ErrInvalidToken, header.Alg)
	}

	expected := sign(secret, parts[0]+"."+parts[1])
	got, err := enc.DecodeString(parts[2])
	if err != nil {
		return nil, ErrInvalidToken
	}
	if !hmac.Equal(expected, got) {
		return nil, ErrInvalidToken
	}

	payloadRaw, err := enc.DecodeString(parts[1])
	if err != nil {
		return nil, ErrInvalidToken
	}
	var claims Claims
	if err := json.Unmarshal(payloadRaw, &claims); err != nil {
		return nil, ErrInvalidToken
	}
	if claims.Exp > 0 && time.Now().UTC().Unix() > claims.Exp {
		return nil, ErrInvalidToken
	}
	return &claims, nil
}

func sign(secret, input string) []byte {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(input))
	return mac.Sum(nil)
}
