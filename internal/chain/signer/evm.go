// Package signer 提供交易签名能力（EVM）。
//
// 安全基线：
//   - 私钥只从环境变量读取，绝不写入代码或配置文件；
//   - dry_run 模式下不加载私钥，且任何签名请求都会失败（fail-closed）；
//   - 生产建议改用 KMS / Vault，把本包替换为远程签名实现。
package signer

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"go.uber.org/zap"

	"meme-bot/internal/config"
	"meme-bot/internal/model"
)

// EVMSigner 本地私钥签名器（适用于 Base / BSC / Ethereum 等 EVM 链）。
type EVMSigner struct {
	key     *ecdsa.PrivateKey
	address common.Address
	log     *zap.Logger

	mu      sync.RWMutex
	clients map[string]*ethclient.Client // chainID -> client
}

// NewEVMFromEnv 从环境变量读取私钥并构建签名器。
//
// rpcByChain：链 ID -> RPC URL 列表（用于补齐 nonce/gas/chainID）。
func NewEVMFromEnv(envVar string, rpcByChain map[string][]string, log *zap.Logger) (*EVMSigner, error) {
	if log == nil {
		log = zap.NewNop()
	}
	raw := strings.TrimSpace(config.ResolveSecret(envVar))
	if raw == "" {
		return nil, fmt.Errorf("signer: 环境变量 %s 未设置（dry_run 模式下无需设置）", envVar)
	}
	raw = strings.TrimPrefix(raw, "0x")

	key, err := crypto.HexToECDSA(raw)
	if err != nil {
		return nil, fmt.Errorf("signer: 私钥解析失败: %w", err)
	}
	addr := crypto.PubkeyToAddress(key.PublicKey)

	s := &EVMSigner{key: key, address: addr, log: log, clients: make(map[string]*ethclient.Client)}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for chainID, urls := range rpcByChain {
		for _, u := range urls {
			c, err := ethclient.DialContext(ctx, u)
			if err != nil {
				continue
			}
			s.clients[strings.ToLower(chainID)] = c
			break
		}
	}
	log.Info("evm signer ready", zap.String("address", addr.Hex()), zap.Int("chains", len(s.clients)))
	return s, nil
}

// Address 实现 model.SwapSigner。
func (s *EVMSigner) Address(_ string) (string, error) {
	if s == nil || s.key == nil {
		return "", errors.New("signer: 未配置私钥")
	}
	return s.address.Hex(), nil
}

// SignSwap 实现 model.SwapSigner：补齐 nonce/gas/chainID 并签名。
func (s *EVMSigner) SignSwap(ctx context.Context, chain string, unsigned *model.UnsignedTx, _ model.SwapParams) (string, error) {
	if s == nil || s.key == nil {
		return "", errors.New("signer: 未配置私钥（dry_run 模式禁止签名）")
	}
	if unsigned == nil {
		return "", errors.New("signer: nil unsigned tx")
	}

	s.mu.RLock()
	client, ok := s.clients[strings.ToLower(chain)]
	s.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("signer: 链 %s 未配置 RPC，无法补齐 nonce/gas", chain)
	}

	chainID, err := client.ChainID(ctx)
	if err != nil {
		return "", fmt.Errorf("signer: chainID: %w", err)
	}
	nonce, err := client.PendingNonceAt(ctx, s.address)
	if err != nil {
		return "", fmt.Errorf("signer: nonce: %w", err)
	}

	to := common.HexToAddress(unsigned.To)
	value := unsigned.Value
	if value == nil {
		value = big.NewInt(0)
	}

	gasLimit := unsigned.GasLimit
	if gasLimit == 0 {
		est, err := client.EstimateGas(ctx, ethereumCallMsg(to, value, unsigned.Data))
		if err != nil {
			return "", fmt.Errorf("signer: estimateGas: %w", err)
		}
		gasLimit = est + est/5 // +20% 余量
	}

	tip, err := client.SuggestGasTipCap(ctx)
	if err != nil || tip == nil {
		tip = big.NewInt(1_000_000)
	}
	head, err := client.HeaderByNumber(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("signer: header: %w", err)
	}
	feeCap := new(big.Int).Mul(tip, big.NewInt(2))
	if head.BaseFee != nil {
		feeCap = new(big.Int).Add(new(big.Int).Mul(head.BaseFee, big.NewInt(2)), tip)
	}

	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   chainID,
		Nonce:     nonce,
		GasTipCap: tip,
		GasFeeCap: feeCap,
		Gas:       gasLimit,
		To:        &to,
		Value:     value,
		Data:      unsigned.Data,
	})

	signed, err := types.SignTx(tx, types.LatestSignerForChainID(chainID), s.key)
	if err != nil {
		return "", fmt.Errorf("signer: sign: %w", err)
	}
	raw, err := signed.MarshalBinary()
	if err != nil {
		return "", fmt.Errorf("signer: marshal: %w", err)
	}
	return "0x" + common.Bytes2Hex(raw), nil
}

// SignerOrNil 在私钥缺失时返回 nil（调用方据此进入 dry_run 或拒绝执行）。
func SignerOrNil(envVar string, rpcByChain map[string][]string, log *zap.Logger) model.SwapSigner {
	s, err := NewEVMFromEnv(envVar, rpcByChain, log)
	if err != nil {
		if log != nil {
			log.Info("evm signer disabled", zap.Error(err))
		}
		return nil
	}
	return s
}

var _ model.SwapSigner = (*EVMSigner)(nil)
