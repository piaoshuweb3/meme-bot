package base

import (
	"encoding/hex"
	"math"
	"math/big"
	"strings"
	"testing"
)

func TestDecodeHexTx(t *testing.T) {
	raw, err := decodeHexTx("0xdeadbeef")
	if err != nil || hex.EncodeToString(raw) != "deadbeef" {
		t.Fatalf("解析失败：%v %x", err, raw)
	}
	if raw, err := decodeHexTx(" 0XDEAD "); err != nil || len(raw) != 2 {
		t.Fatalf("应支持 0X 前缀与首尾空白：%v %x", err, raw)
	}
	if _, err := decodeHexTx("0x"); err == nil {
		t.Fatal("空交易应报错")
	}
	if _, err := decodeHexTx("0xzz"); err == nil {
		t.Fatal("非法 hex 应报错（不能静默发送空交易）")
	}
}

func TestMaskURLHidesCredentials(t *testing.T) {
	cases := []struct {
		in          string
		mustNotHave string
	}{
		{"https://base-sepolia.g.alchemy.com/v2/abcdefghijklmnopqrstuvwxyz012345", "abcdefghijklmnopqrstuvwxyz012345"},
		{"https://rpc.example/key?api_key=supersecret", "supersecret"},
		{"https://user:password@rpc.example", "password"},
	}
	for _, c := range cases {
		got := maskURL(c.in)
		if strings.Contains(got, c.mustNotHave) {
			t.Fatalf("脱敏失败，仍含敏感片段 %q：%q", c.mustNotHave, got)
		}
	}
	if got := maskURL(""); got != "" {
		t.Fatalf("空串应返回空串：%q", got)
	}
	if got := maskURL("https://mainnet.base.org"); got != "https://mainnet.base.org" {
		t.Fatalf("无敏感信息时不应改动：%q", got)
	}
}

func TestToFloatConvertsByDecimals(t *testing.T) {
	cases := []struct {
		amount   *big.Int
		decimals uint8
		want     float64
	}{
		{big.NewInt(1000000), 6, 1},
		{big.NewInt(1500000000), 9, 1.5},
		{big.NewInt(42), 0, 42},
		{big.NewInt(0), 18, 0},
		{nil, 6, 0},
	}
	for _, c := range cases {
		if got := toFloat(c.amount, c.decimals); got != c.want {
			t.Fatalf("toFloat(%v, %d) = %v，期望 %v", c.amount, c.decimals, got, c.want)
		}
	}

	huge, _ := new(big.Int).SetString("123456789012345678901234567890", 10)
	got := toFloat(huge, 6)
	if got <= 0 || math.IsInf(got, 0) {
		t.Fatalf("大额换算异常（不应溢出为 Inf）：%v", got)
	}
}

func TestMaxBig(t *testing.T) {
	if got := maxBig(nil, big.NewInt(5)); got.Int64() != 5 {
		t.Fatal("a 为 nil 应返回 b")
	}
	if got := maxBig(big.NewInt(5), nil); got.Int64() != 5 {
		t.Fatal("b 为 nil 应返回 a")
	}
	if got := maxBig(big.NewInt(3), big.NewInt(9)); got.Int64() != 9 {
		t.Fatal("应返回较大者")
	}
	if got := maxBig(big.NewInt(9), big.NewInt(3)); got.Int64() != 9 {
		t.Fatal("应返回较大者")
	}
	if got := maxBig(big.NewInt(7), big.NewInt(7)); got.Int64() != 7 {
		t.Fatal("相等时应返回 a")
	}
}

func TestNormalizeTokenMapsNativePlaceholder(t *testing.T) {
	native := "0xEeeeeEeeeEeEeeEeEeEeeEEEeeeeEeeeeeeeEEeE"
	if got := normalizeToken("", native); got != nativeSentinel {
		t.Fatalf("空地址应归一为原生占位符：%q", got)
	}
	if got := normalizeToken("   ", native); got != nativeSentinel {
		t.Fatalf("空白应归一为原生占位符：%q", got)
	}
	if got := normalizeToken("0xeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", native); got != nativeSentinel {
		t.Fatalf("应与原生代币大小写不敏感匹配：%q", got)
	}
	if got := normalizeToken("0xToken", native); got != "0xToken" {
		t.Fatalf("普通代币应原样返回：%q", got)
	}
}
