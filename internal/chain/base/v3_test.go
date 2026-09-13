package base

import (
	"math/big"
	"testing"
)

// int256Bytes 把 int64 编码为 32 字节二补码。
func int256Bytes(v int64) []byte {
	b := make([]byte, 32)
	x := new(big.Int).SetInt64(v)
	if v < 0 {
		x.Add(x, new(big.Int).Lsh(big.NewInt(1), 256))
	}
	bs := x.Bytes()
	copy(b[32-len(bs):], bs)
	return b
}

func TestDecodeInt256(t *testing.T) {
	cases := []struct {
		name string
		raw  []byte
		want int64
	}{
		{"零", int256Bytes(0), 0},
		{"正数", int256Bytes(123456), 123456},
		{"负数", int256Bytes(-123456), -123456},
		{"负一", int256Bytes(-1), -1},
		{"最大值", int256Bytes(9223372036854775807), 9223372036854775807},
		{"最小值", int256Bytes(-9223372036854775808), -9223372036854775808},
	}
	for _, c := range cases {
		if got := decodeInt256(c.raw); got.Int64() != c.want {
			t.Fatalf("%s: decodeInt256 = %s，期望 %d", c.name, got.String(), c.want)
		}
	}
	// 截断输入应安全返回 0（不 panic）
	if got := decodeInt256([]byte{0x01, 0x02}); got.Sign() != 0 {
		t.Fatalf("短输入应返回 0，实际 %s", got.String())
	}
}

func TestParseV3Amounts(t *testing.T) {
	// data = amount0(+1000) | amount1(-2000) | sqrtPriceX96 | liquidity | tick
	data := make([]byte, 5*32)
	copy(data[0:32], int256Bytes(1000))
	copy(data[32:64], int256Bytes(-2000))

	a0, a1, ok := parseV3Amounts(data)
	if !ok {
		t.Fatal("合法 data 应解析成功")
	}
	if a0.Int64() != 1000 || a1.Int64() != -2000 {
		t.Fatalf("金额解析错误：%s / %s", a0.String(), a1.String())
	}

	if _, _, ok := parseV3Amounts(make([]byte, 4*32)); ok {
		t.Fatal("data 长度不足应返回 false")
	}
}

func TestV3Direction(t *testing.T) {
	const t0, t1 = "0xToken0", "0xToken1"

	// amount0 > 0 且 amount1 < 0 → 卖出 token0 买入 token1
	in, out, amtIn, amtOut, ok := v3Direction(big.NewInt(1000), big.NewInt(-2000), t0, t1)
	if !ok || in != t0 || out != t1 || amtIn.Int64() != 1000 || amtOut.Int64() != 2000 {
		t.Fatalf("卖出 token0 场景解析错误：%s→%s %s/%s ok=%v", in, out, amtIn, amtOut, ok)
	}

	// amount0 < 0 且 amount1 > 0 → 买入 token0 卖出 token1
	in, out, amtIn, amtOut, ok = v3Direction(big.NewInt(-500), big.NewInt(700), t0, t1)
	if !ok || in != t1 || out != t0 || amtIn.Int64() != 700 || amtOut.Int64() != 500 {
		t.Fatalf("买入 token0 场景解析错误：%s→%s %s/%s ok=%v", in, out, amtIn, amtOut, ok)
	}

	// 同号（加/撤流动性等）不构成单边交换
	if _, _, _, _, ok := v3Direction(big.NewInt(1), big.NewInt(1), t0, t1); ok {
		t.Fatal("同号金额不应判定为交换")
	}
	// 零金额
	if _, _, _, _, ok := v3Direction(big.NewInt(0), big.NewInt(0), t0, t1); ok {
		t.Fatal("零金额不应判定为交换")
	}
	// nil 安全
	if _, _, _, _, ok := v3Direction(nil, big.NewInt(1), t0, t1); ok {
		t.Fatal("nil 输入应返回 false")
	}
}
