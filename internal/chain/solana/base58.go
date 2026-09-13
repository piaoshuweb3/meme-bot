package solana

import (
	"fmt"
	"math/big"
	"strings"
)

// Base58 编解码（Solana 地址与签名使用 Bitcoin 风格的 base58 字母表）。
//
// 不引入第三方库：实现只有几十行，且完全可控。
const b58Alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

var b58Index = func() [256]int8 {
	var idx [256]int8
	for i := range idx {
		idx[i] = -1
	}
	for i := 0; i < len(b58Alphabet); i++ {
		idx[b58Alphabet[i]] = int8(i)
	}
	return idx
}()

// base58Encode 编码为 base58 字符串。
func base58Encode(input []byte) string {
	if len(input) == 0 {
		return ""
	}
	x := new(big.Int).SetBytes(input)
	base := big.NewInt(58)
	zero := big.NewInt(0)
	mod := new(big.Int)

	var out []byte
	for x.Cmp(zero) > 0 {
		x.DivMod(x, base, mod)
		out = append(out, b58Alphabet[mod.Int64()])
	}
	// 前导零字节 -> '1'
	for _, b := range input {
		if b != 0 {
			break
		}
		out = append(out, b58Alphabet[0])
	}
	// 反转
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return string(out)
}

// base58Decode 解码 base58 字符串。
func base58Decode(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("solana: empty base58 string")
	}
	x := new(big.Int)
	base := big.NewInt(58)
	for i := 0; i < len(s); i++ {
		c := s[i]
		v := b58Index[c]
		if v < 0 {
			return nil, fmt.Errorf("solana: invalid base58 character %q", c)
		}
		x.Mul(x, base)
		x.Add(x, big.NewInt(int64(v)))
	}
	decoded := x.Bytes()
	// 还原前导零
	var zeros int
	for i := 0; i < len(s) && s[i] == b58Alphabet[0]; i++ {
		zeros++
	}
	if zeros > 0 {
		decoded = append(make([]byte, zeros), decoded...)
	}
	return decoded, nil
}
