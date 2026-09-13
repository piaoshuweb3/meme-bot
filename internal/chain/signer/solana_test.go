package signer

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"strings"
	"testing"
)

// buildFakeTx 构造一个最小可解析的 Solana legacy 交易（1 个签名槽 + 简单 message）。
func buildFakeTx(payer []byte) []byte {
	tx := []byte{0x01}                   // 签名数量 = 1（shortvec）
	tx = append(tx, make([]byte, 64)...) // 签名占位（全 0）
	tx = append(tx, 0x01, 0x00, 0x01)    // message header：1 required sig / 0 readonly signed / 1 readonly unsigned
	tx = append(tx, 0x02)                // accountKeys 数量 = 2（shortvec）
	tx = append(tx, payer...)            // accountKeys[0] = fee payer
	tx = append(tx, bytes.Repeat([]byte{0x02}, 32)...)
	tx = append(tx, bytes.Repeat([]byte{0x03}, 32)...) // recentBlockhash
	tx = append(tx, 0x00)                              // instructions 数量 = 0
	return tx
}

func testSigner(t *testing.T) *SolanaSigner {
	t.Helper()
	s, err := newSolanaSigner(bytes.Repeat([]byte{7}, ed25519.SeedSize))
	if err != nil {
		t.Fatalf("构造签名器失败：%v", err)
	}
	return s
}

func TestSolanaSignAndVerify(t *testing.T) {
	s := testSigner(t)
	tx := buildFakeTx(s.pub)

	signed, err := s.SignTransaction(tx)
	if err != nil {
		t.Fatalf("签名失败：%v", err)
	}
	if len(signed) != len(tx) {
		t.Fatalf("签名不应改变交易长度：%d != %d", len(signed), len(tx))
	}
	ok, err := s.VerifyTransaction(signed)
	if err != nil || !ok {
		t.Fatalf("签名校验应通过：ok=%v err=%v", ok, err)
	}

	// 原始交易（签名槽为 0）不应通过校验
	if ok, _ := s.VerifyTransaction(tx); ok {
		t.Fatal("未签名交易不应通过校验")
	}

	// 篡改 message 后必须校验失败
	tampered := append([]byte(nil), signed...)
	tampered[len(tampered)-1] ^= 0xff
	if ok, _ := s.VerifyTransaction(tampered); ok {
		t.Fatal("篡改 message 后必须校验失败")
	}
}

func TestSolanaRejectsForeignFeePayer(t *testing.T) {
	s := testSigner(t)
	foreign := bytes.Repeat([]byte{9}, 32) // 别人的公钥
	if _, err := s.SignTransaction(buildFakeTx(foreign)); err == nil {
		t.Fatal("fee payer 非本私钥地址时必须拒绝签名（防盲签）")
	}
	if _, err := s.SignTransaction(buildFakeTx(s.pub)); err != nil {
		t.Fatalf("自身 fee payer 应可签名：%v", err)
	}
}

func TestSolanaSignTransactionBase64(t *testing.T) {
	s := testSigner(t)
	txB64 := base64.StdEncoding.EncodeToString(buildFakeTx(s.pub))

	outB64, err := s.SignTransactionBase64(txB64)
	if err != nil {
		t.Fatalf("base64 签名失败：%v", err)
	}
	raw, err := base64.StdEncoding.DecodeString(outB64)
	if err != nil {
		t.Fatalf("输出不是合法 base64：%v", err)
	}
	if ok, _ := s.VerifyTransaction(raw); !ok {
		t.Fatal("base64 往返后签名应有效")
	}
	if _, err := s.SignTransactionBase64("not-base64!!"); err == nil {
		t.Fatal("非法 base64 应报错")
	}
}

func TestSolanaKeypairFormats(t *testing.T) {
	s := testSigner(t)
	addr, err := s.Address("solana")
	if err != nil || addr == "" {
		t.Fatalf("地址应可导出：%v", err)
	}

	// 32 字节 seed 的 base58
	seedB58 := base58Encode([]byte(bytes.Repeat([]byte{7}, ed25519.SeedSize)))
	a, err := NewSolanaFromBase58(seedB58)
	if err != nil {
		t.Fatalf("seed base58 解析失败：%v", err)
	}
	if got, _ := a.Address("solana"); got != addr {
		t.Fatalf("同 seed 应导出同地址：%s != %s", got, addr)
	}

	// 64 字节完整私钥的 base58
	fullB58 := base58Encode([]byte(s.priv))
	b, err := NewSolanaFromBase58(fullB58)
	if err != nil {
		t.Fatalf("完整私钥 base58 解析失败：%v", err)
	}
	if got, _ := b.Address("solana"); got != addr {
		t.Fatalf("同私钥应导出同地址：%s != %s", got, addr)
	}

	// JSON 数组格式（Solana CLI keypair）
	parts := make([]string, 0, len(s.priv))
	for _, v := range []byte(s.priv) {
		parts = append(parts, itoa(int(v)))
	}
	jsonKey := "[" + strings.Join(parts, ",") + "]"
	c, err := NewSolanaFromBase58(jsonKey)
	if err != nil {
		t.Fatalf("JSON keypair 解析失败：%v", err)
	}
	if got, _ := c.Address("solana"); got != addr {
		t.Fatalf("JSON keypair 应导出同地址：%s != %s", got, addr)
	}

	if _, err := NewSolanaFromBase58(""); err == nil {
		t.Fatal("空私钥应报错")
	}
	if _, err := NewSolanaFromBase58(base58Encode([]byte{1, 2, 3})); err == nil {
		t.Fatal("长度非法的私钥应报错")
	}
}

func TestReadShortVecAndLayout(t *testing.T) {
	cases := []struct {
		in   []byte
		want int
		n    int
	}{
		{[]byte{0x00}, 0, 1},
		{[]byte{0x7f}, 127, 1},
		{[]byte{0x80, 0x01}, 128, 2},
		{[]byte{0xff, 0x7f}, 16383, 2},
	}
	for _, c := range cases {
		v, n, err := readShortVec(c.in)
		if err != nil || v != c.want || n != c.n {
			t.Fatalf("readShortVec(%v) = %d,%d,%v；期望 %d,%d", c.in, v, n, err, c.want, c.n)
		}
	}
	if _, _, err := readShortVec(nil); err == nil {
		t.Fatal("空输入应报错")
	}

	s := testSigner(t)
	if _, _, err := parseTxLayout(nil); err == nil {
		t.Fatal("空交易应报错")
	}
	if _, _, err := parseTxLayout([]byte{0x02}); err == nil {
		t.Fatal("长度不足以容纳签名数量时应报错")
	}
	count, msgStart, err := parseTxLayout(buildFakeTx(s.pub))
	if err != nil || count != 1 || msgStart != 1+64 {
		t.Fatalf("布局解析错误：count=%d msgStart=%d err=%v", count, msgStart, err)
	}
}

func TestBase58RoundTrip(t *testing.T) {
	for _, in := range [][]byte{
		{},
		{0x00},
		{0x00, 0x01, 0x02},
		bytes.Repeat([]byte{0xff}, 32),
		bytes.Repeat([]byte{0x07}, 64),
	} {
		enc := base58Encode(in)
		if len(in) == 0 {
			continue
		}
		dec, err := base58Decode(enc)
		if err != nil || !bytes.Equal(dec, in) {
			t.Fatalf("base58 往返失败：in=%v enc=%s dec=%v err=%v", in, enc, dec, err)
		}
	}
	if _, err := base58Decode("0OIl"); err == nil {
		t.Fatal("含非法字符的 base58 应报错")
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [8]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
