package payment

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// AssetInfo 描述 EIP-712 域（EIP-3009 资产，如 USDC）。
type AssetInfo struct {
	Address  string
	Name     string
	Version  string
	Decimals int
}

// EVMExactPayer 使用 EIP-3009 transferWithAuthorization 为稳定币签名授权。
//
// 为什么用 EIP-3009：它允许"离线授权、链上结算"，付款方无需先转币，
// 收到 402 后即可给出可验证的授权签名，由结算方提交上链（x402 的 exact 方案）。
type EVMExactPayer struct {
	priv     *ecdsa.PrivateKey
	addr     common.Address
	chainIDs map[string]int64
	assets   map[string]AssetInfo
	now      func() time.Time
}

// DefaultNetworks 常见网络的 chainId。
func DefaultNetworks() map[string]int64 {
	return map[string]int64{
		"base":         8453,
		"base-sepolia": 84532,
		"polygon":      137,
		"arbitrum":     42161,
		"ethereum":     1,
	}
}

// DefaultAssets 预置的稳定币（key 为 "network|asset" 小写）。
func DefaultAssets() map[string]AssetInfo {
	usdc := func(addr string) AssetInfo {
		return AssetInfo{Address: addr, Name: "USD Coin", Version: "2", Decimals: 6}
	}
	return map[string]AssetInfo{
		"base|0x833589fcd6edb6e08f4c7c32d4f71b54bda02913":         usdc("0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913"),
		"base-sepolia|0x036cbd53842c5426634e7929541ec2318f3dcf7e": usdc("0x036CbD53842c5426634e7929541eC2318f3dCF7e"),
		"polygon|0x3c499c542cef5e3811e1192ce70d8cc03d5c3359":      usdc("0x3c499c542cEF5E3811e1192ce70d8cC03d5c3359"),
		"arbitrum|0xaf88d065e77c8cc2239327c5edb3a432268e5831":     usdc("0xaf88d065e77c8cC2239327C5EDb3A432268e5831"),
		"ethereum|0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48":     usdc("0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48"),
	}
}

// NewEVMExactPayer 从 hex 私钥构建（0x 前缀可选）。
func NewEVMExactPayer(hexKey string) (*EVMExactPayer, error) {
	raw := strings.TrimPrefix(strings.TrimSpace(hexKey), "0x")
	if raw == "" {
		return nil, errors.New("payment: 空的 EVM 私钥")
	}
	key, err := crypto.HexToECDSA(raw)
	if err != nil {
		return nil, fmt.Errorf("payment: 私钥解析失败: %w", err)
	}
	return &EVMExactPayer{
		priv:     key,
		addr:     crypto.PubkeyToAddress(key.PublicKey),
		chainIDs: DefaultNetworks(),
		assets:   DefaultAssets(),
		now:      func() time.Time { return time.Now().UTC() },
	}, nil
}

// Address 实现 Payer。
func (p *EVMExactPayer) Address() string { return p.addr.Hex() }

// Supports 实现 Payer：网络与资产都需已知。
func (p *EVMExactPayer) Supports(network, asset string) bool {
	if _, ok := p.chainIDs[strings.ToLower(strings.TrimSpace(network))]; !ok {
		return false
	}
	_, ok := p.assets[assetKey(network, asset)]
	return ok
}

// CreatePayload 实现 Payer：生成 EIP-3009 授权与签名。
func (p *EVMExactPayer) CreatePayload(_ context.Context, req Requirement) (json.RawMessage, error) {
	chainID, ok := p.chainIDs[strings.ToLower(strings.TrimSpace(req.Network))]
	if !ok {
		return nil, fmt.Errorf("payment: 不支持的网络 %s", req.Network)
	}
	info, ok := p.assets[assetKey(req.Network, req.Asset)]
	if !ok {
		return nil, fmt.Errorf("payment: 不支持的资产 %s", req.Asset)
	}
	if !common.IsHexAddress(req.PayTo) {
		return nil, fmt.Errorf("payment: payTo 非法: %s", req.PayTo)
	}
	value, ok := parseAmount(req.MaxAmountRequired)
	if !ok {
		return nil, fmt.Errorf("payment: 金额非法: %s", req.MaxAmountRequired)
	}

	timeout := req.MaxTimeoutSeconds
	if timeout <= 0 {
		timeout = 300
	}
	now := p.now()
	validAfter := big.NewInt(now.Add(-time.Minute).Unix()) // 留 1 分钟时钟偏差
	validBefore := big.NewInt(now.Add(time.Duration(timeout) * time.Second).Unix())

	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("payment: 生成 nonce 失败: %w", err)
	}

	to := common.HexToAddress(req.PayTo)
	digest := EIP3009Digest(info, chainID, p.addr, to, value, validAfter, validBefore, nonce)

	signature, err := crypto.Sign(digest, p.priv)
	if err != nil {
		return nil, fmt.Errorf("payment: 签名失败: %w", err)
	}
	// EIP-3009 使用 27/28 的 v 值
	if len(signature) == 65 && signature[64] < 27 {
		signature[64] += 27
	}

	auth := map[string]string{
		"from":        p.addr.Hex(),
		"to":          to.Hex(),
		"value":       value.String(),
		"validAfter":  validAfter.String(),
		"validBefore": validBefore.String(),
		"nonce":       "0x" + hex.EncodeToString(nonce),
	}
	return json.Marshal(map[string]any{
		"signature":     "0x" + hex.EncodeToString(signature),
		"authorization": auth,
	})
}

// assetKey 资产索引键。
func assetKey(network, asset string) string {
	return strings.ToLower(strings.TrimSpace(network)) + "|" + strings.ToLower(strings.TrimSpace(asset))
}

// EIP-712 类型哈希（EIP-3009 / USDC 规范）。
var (
	eip712DomainTypeHash = crypto.Keccak256([]byte("EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)"))
	transferAuthTypeHash = crypto.Keccak256([]byte("TransferWithAuthorization(address from,address to,uint256 value,uint256 validAfter,uint256 validBefore,bytes32 nonce)"))
)

// EIP3009Digest 计算 EIP-712 签名摘要（供签名与测试校验使用）。
func EIP3009Digest(info AssetInfo, chainID int64, from, to common.Address, value, validAfter, validBefore *big.Int, nonce []byte) []byte {
	domain := crypto.Keccak256(
		eip712DomainTypeHash,
		crypto.Keccak256([]byte(info.Name)),
		crypto.Keccak256([]byte(info.Version)),
		leftPad32(big.NewInt(chainID).Bytes()),
		leftPad32(common.HexToAddress(info.Address).Bytes()),
	)
	message := crypto.Keccak256(
		transferAuthTypeHash,
		leftPad32(from.Bytes()),
		leftPad32(to.Bytes()),
		leftPad32(value.Bytes()),
		leftPad32(validAfter.Bytes()),
		leftPad32(validBefore.Bytes()),
		leftPad32(nonce),
	)
	return crypto.Keccak256([]byte{0x19, 0x01}, domain, message)
}

// leftPad32 左填充到 32 字节（ABI 编码）。
func leftPad32(b []byte) []byte {
	out := make([]byte, 32)
	if len(b) > 32 {
		b = b[len(b)-32:]
	}
	copy(out[32-len(b):], b)
	return out
}

var _ Payer = (*EVMExactPayer)(nil)
