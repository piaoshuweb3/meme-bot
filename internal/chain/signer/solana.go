package signer

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"go.uber.org/zap"

	"meme-bot/internal/config"
	"meme-bot/internal/model"
)

// SolanaSigner 本地 Ed25519 签名器：为 Jupiter 等聚合器返回的未签名交易签名。
//
// 安全要点（重要）：
//   - 签名前**强制校验 fee payer（accountKeys[0]）等于本私钥对应公钥**，
//     防止在"替他人付手续费/他人为签名者"的交易上盲签（盲签等于交出资产控制权）；
//   - 校验签名数量与 message 结构，任何越界/畸形数据直接拒绝，不猜测；
//   - 私钥只从环境变量读取（生产建议改用 KMS/远程签名服务）。
type SolanaSigner struct {
	priv ed25519.PrivateKey
	pub  ed25519.PublicKey
}

// NewSolanaFromBase58 从 base58 私钥构造。
//
// 兼容两种常见格式：
//   - 64 字节完整私钥（Solana CLI keypair 的 secretKey 字段）；
//   - 32 字节 seed（由 ed25519.NewKeyFromSeed 派生）。
func NewSolanaFromBase58(encoded string) (*SolanaSigner, error) {
	raw := strings.TrimSpace(encoded)
	if raw == "" {
		return nil, errors.New("signer: 空的 Solana 私钥")
	}
	raw = strings.TrimPrefix(raw, "base58:")
	// 兼容 JSON 数组格式的 keypair（[1,2,3,...]）
	if strings.HasPrefix(raw, "[") {
		seed, err := parseJSONKeypair(raw)
		if err != nil {
			return nil, err
		}
		return newSolanaSigner(seed)
	}
	decoded, err := base58Decode(raw)
	if err != nil {
		return nil, fmt.Errorf("signer: Solana 私钥 base58 解码失败: %w", err)
	}
	return newSolanaSigner(decoded)
}

// NewSolanaFromEnv 从环境变量读取私钥并构造签名器。
func NewSolanaFromEnv(envVar string) (*SolanaSigner, error) {
	raw := config.ResolveSecret(envVar)
	if raw == "" {
		return nil, fmt.Errorf("signer: 环境变量 %s 未设置", envVar)
	}
	return NewSolanaFromBase58(raw)
}

func newSolanaSigner(key []byte) (*SolanaSigner, error) {
	switch len(key) {
	case ed25519.SeedSize: // 32
		priv := ed25519.NewKeyFromSeed(key)
		return &SolanaSigner{priv: priv, pub: priv.Public().(ed25519.PublicKey)}, nil
	case ed25519.PrivateKeySize: // 64
		priv := ed25519.PrivateKey(append([]byte(nil), key...))
		pub, ok := priv.Public().(ed25519.PublicKey)
		if !ok {
			return nil, errors.New("signer: 私钥推导公钥失败")
		}
		return &SolanaSigner{priv: priv, pub: pub}, nil
	default:
		return nil, fmt.Errorf("signer: Solana 私钥长度非法（%d 字节，期望 32 或 64）", len(key))
	}
}

// parseJSONKeypair 解析 Solana CLI 风格的 JSON 数组私钥。
func parseJSONKeypair(raw string) ([]byte, error) {
	trimmed := strings.Trim(strings.TrimSpace(raw), "[]")
	if strings.TrimSpace(trimmed) == "" {
		return nil, errors.New("signer: JSON keypair 为空")
	}
	parts := strings.Split(trimmed, ",")
	out := make([]byte, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		v, ok := new(big.Int).SetString(p, 10)
		if !ok || !v.IsUint64() || v.Uint64() > 255 {
			return nil, fmt.Errorf("signer: JSON keypair 含非法字节 %q", p)
		}
		out = append(out, byte(v.Uint64()))
	}
	return out, nil
}

// Address 返回签名者地址（base58 编码的公钥）。
func (s *SolanaSigner) Address(_ string) (string, error) {
	if s == nil || len(s.pub) == 0 {
		return "", errors.New("signer: 未配置 Solana 私钥")
	}
	return base58Encode(s.pub), nil
}

// SignSolanaTx 实现 model.SolanaTxSigner。
func (s *SolanaSigner) SignSolanaTx(_ context.Context, _ string, tx []byte) (string, error) {
	signed, err := s.SignTransaction(tx)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(signed), nil
}

// SignTransactionBase64 对 base64 编码的未签名交易签名，返回 base64 编码的已签名交易。
func (s *SolanaSigner) SignTransactionBase64(txB64 string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(txB64))
	if err != nil {
		return "", fmt.Errorf("signer: 交易 base64 解码失败: %w", err)
	}
	signed, err := s.SignTransaction(raw)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(signed), nil
}

// ---- base58（与 Solana 地址/签名编码一致；此处独立实现以避免跨包依赖） ----

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

func base58Encode(input []byte) string {
	if len(input) == 0 {
		return ""
	}
	x := new(big.Int).SetBytes(input)
	base := big.NewInt(58)
	mod := new(big.Int)
	var out []byte
	for x.Sign() > 0 {
		x.DivMod(x, base, mod)
		out = append(out, b58Alphabet[mod.Int64()])
	}
	for _, b := range input {
		if b != 0 {
			break
		}
		out = append(out, b58Alphabet[0])
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return string(out)
}

func base58Decode(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, errors.New("空的 base58 字符串")
	}
	x := new(big.Int)
	base := big.NewInt(58)
	for i := 0; i < len(s); i++ {
		v := b58Index[s[i]]
		if v < 0 {
			return nil, fmt.Errorf("非法 base58 字符 %q", s[i])
		}
		x.Mul(x, base)
		x.Add(x, big.NewInt(int64(v)))
	}
	out := x.Bytes()
	var zeros int
	for i := 0; i < len(s) && s[i] == b58Alphabet[0]; i++ {
		zeros++
	}
	if zeros > 0 {
		out = append(make([]byte, zeros), out...)
	}
	return out, nil
}

// SolanaSignerOrNil 在私钥缺失时返回 nil（dry_run 下无需配置私钥）。
func SolanaSignerOrNil(envVar string, log *zap.Logger) model.SolanaTxSigner {
	s, err := NewSolanaFromEnv(envVar)
	if err != nil {
		if log != nil {
			log.Info("solana signer disabled", zap.Error(err))
		}
		return nil
	}
	if log != nil {
		addr, _ := s.Address("solana")
		log.Info("solana signer ready", zap.String("address", addr))
	}
	return s
}

var _ model.SolanaTxSigner = (*SolanaSigner)(nil)

// ---- Solana 交易（wire format）解析与签名 ----

// SignTransaction 对原始交易字节签名，返回已签名交易字节。
//
// Solana 交易结构（legacy 与 v0 前缀一致）：
//
//	[签名数量(shortvec)] [签名 64B × N] [message]
//	message = [header(3B)] [accountKeys(shortvec + 32B × N)] [recentBlockhash(32B)] [instructions…] [v0: lookups]
//
// 本实现只做必要且安全的三步：定位 message → 校验 fee payer → Ed25519 签名并写回第 0 个签名槽。
func (s *SolanaSigner) SignTransaction(tx []byte) ([]byte, error) {
	if s == nil || len(s.priv) == 0 {
		return nil, errors.New("signer: 未配置 Solana 私钥（dry_run 模式禁止签名）")
	}
	sigCount, msgStart, err := parseTxLayout(tx)
	if err != nil {
		return nil, err
	}
	if sigCount == 0 {
		return nil, errors.New("signer: 交易声明的签名数量为 0")
	}

	payer, err := feePayer(tx, msgStart)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(payer, s.pub) {
		return nil, fmt.Errorf(
			"signer: 拒绝签名——fee payer(%s) 与本私钥地址(%s) 不一致（防止盲签他人交易）",
			base58Encode(payer), base58Encode(s.pub))
	}

	message := tx[msgStart:]
	signature := ed25519.Sign(s.priv, message)

	_, vecLen, err := readShortVec(tx)
	if err != nil {
		return nil, err
	}
	sig0 := vecLen
	out := make([]byte, len(tx))
	copy(out, tx)
	if sig0+ed25519.SignatureSize > len(out) {
		return nil, errors.New("signer: 签名槽越界")
	}
	copy(out[sig0:sig0+ed25519.SignatureSize], signature)
	return out, nil
}

// VerifyTransaction 校验第 0 个签名是否由本公钥对 message 正确签名（广播前自检用）。
func (s *SolanaSigner) VerifyTransaction(tx []byte) (bool, error) {
	if s == nil || len(s.pub) == 0 {
		return false, errors.New("signer: 未配置公钥")
	}
	sigCount, msgStart, err := parseTxLayout(tx)
	if err != nil {
		return false, err
	}
	if sigCount == 0 {
		return false, errors.New("signer: 交易无签名槽")
	}
	_, vecLen, err := readShortVec(tx)
	if err != nil {
		return false, err
	}
	sig0 := vecLen
	if sig0+ed25519.SignatureSize > len(tx) {
		return false, errors.New("signer: 签名槽越界")
	}
	sig := tx[sig0 : sig0+ed25519.SignatureSize]
	return ed25519.Verify(s.pub, tx[msgStart:], sig), nil
}

// parseTxLayout 解析签名数量与 message 起始偏移。
func parseTxLayout(tx []byte) (sigCount, msgStart int, err error) {
	if len(tx) == 0 {
		return 0, 0, errors.New("signer: 空交易")
	}
	count, n, err := readShortVec(tx)
	if err != nil {
		return 0, 0, fmt.Errorf("signer: 解析签名数量失败: %w", err)
	}
	msgStart = n + count*ed25519.SignatureSize
	if msgStart > len(tx) {
		return 0, 0, fmt.Errorf("signer: 交易长度不足以容纳 %d 个签名", count)
	}
	return count, msgStart, nil
}

// feePayer 从 message 头部解析 accountKeys[0]（Solana 约定 fee payer 必须是第一个账户）。
func feePayer(tx []byte, msgStart int) ([]byte, error) {
	pos := msgStart + 3 // 跳过 header(numRequiredSignatures, numReadonlySigned, numReadonlyUnsigned)
	if pos > len(tx) {
		return nil, errors.New("signer: message header 越界")
	}
	n, vecLen, err := readShortVec(tx[pos:])
	if err != nil {
		return nil, fmt.Errorf("signer: 解析 accountKeys 数量失败: %w", err)
	}
	if n == 0 {
		return nil, errors.New("signer: accountKeys 为空")
	}
	start := pos + vecLen
	if start+32 > len(tx) {
		return nil, errors.New("signer: accountKeys[0] 越界")
	}
	return tx[start : start+32], nil
}

// readShortVec 解析 Solana compact-u16（shortvec）编码，返回（值, 占用字节数）。
func readShortVec(b []byte) (int, int, error) {
	var value uint
	for i := 0; i < 3; i++ {
		if i >= len(b) {
			return 0, 0, errors.New("shortvec 截断")
		}
		cur := b[i]
		value |= uint(cur&0x7f) << (7 * uint(i))
		if cur&0x80 == 0 {
			return int(value), i + 1, nil
		}
	}
	return int(value), 3, nil
}
