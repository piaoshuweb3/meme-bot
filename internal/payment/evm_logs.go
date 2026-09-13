package payment

import (
	"context"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"

	"meme-bot/internal/chain"
)

// transferTopic0 = keccak256("Transfer(address,address,uint256)")
var transferTopic0 = crypto.Keccak256Hash([]byte("Transfer(address,address,uint256)"))

// EVMTransferSource 通过 eth_getLogs 读取标准 ERC-20 Transfer 日志（只读，无需私钥）。
type EVMTransferSource struct {
	rpc *chain.RPCPool
}

// NewEVMTransferSource 构建日志源（多 RPC 自动故障切换由 RPCPool 提供）。
func NewEVMTransferSource(name string, rpcURLs []string) *EVMTransferSource {
	return &EVMTransferSource{rpc: chain.NewRPCPool(name, rpcURLs)}
}

// BlockNumber 实现 LogSource。
func (s *EVMTransferSource) BlockNumber(ctx context.Context) (uint64, error) {
	var raw string
	if err := s.rpc.Call(ctx, "eth_blockNumber", []any{}, &raw); err != nil {
		return 0, err
	}
	return parseHexUint64(raw)
}

// RecentTransfers 实现 LogSource：按 topics 过滤 Transfer 且 to == 收款地址。
func (s *EVMTransferSource) RecentTransfers(ctx context.Context, asset, to string, fromBlock uint64) ([]TransferLog, error) {
	if !common.IsHexAddress(asset) {
		return nil, fmt.Errorf("payment: 非法资产地址 %s", asset)
	}
	if !common.IsHexAddress(to) {
		return nil, fmt.Errorf("payment: 非法收款地址 %s", to)
	}

	query := map[string]any{
		"fromBlock": hexUint64(fromBlock),
		"toBlock":   "latest",
		"address":   common.HexToAddress(asset).Hex(),
		"topics":    []any{transferTopic0.Hex(), nil, padTopic(to)},
	}
	var raw []struct {
		TxHash      string   `json:"transactionHash"`
		Data        string   `json:"data"`
		BlockNumber string   `json:"blockNumber"`
		LogIndex    string   `json:"logIndex"`
		Topics      []string `json:"topics"`
	}
	if err := s.rpc.Call(ctx, "eth_getLogs", []any{query}, &raw); err != nil {
		return nil, fmt.Errorf("payment: eth_getLogs 失败: %w", err)
	}

	head, err := s.BlockNumber(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]TransferLog, 0, len(raw))
	for _, lg := range raw {
		value, err := parseHexBig(lg.Data)
		if err != nil {
			continue
		}
		block, _ := parseHexUint64(lg.BlockNumber)
		idx, _ := parseHexUint64(lg.LogIndex)
		confs := uint64(1)
		if head >= block {
			confs = head - block + 1
		}
		from := ""
		if len(lg.Topics) >= 2 {
			from = topicToAddress(lg.Topics[1])
		}
		out = append(out, TransferLog{
			TxHash:        lg.TxHash,
			From:          from,
			To:            common.HexToAddress(to).Hex(),
			Value:         value,
			BlockNumber:   block,
			LogIndex:      idx,
			Confirmations: confs,
			At:            time.Now().UTC(),
		})
	}
	return out, nil
}

func hexUint64(v uint64) string { return "0x" + strconv.FormatUint(v, 16) }

func parseHexUint64(s string) (uint64, error) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "0x")
	if s == "" {
		return 0, fmt.Errorf("payment: 空十六进制值")
	}
	return strconv.ParseUint(s, 16, 64)
}

func parseHexBig(s string) (*big.Int, error) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "0x")
	if s == "" {
		return big.NewInt(0), nil
	}
	v, ok := new(big.Int).SetString(s, 16)
	if !ok {
		return nil, fmt.Errorf("payment: 非法十六进制数值 %q", s)
	}
	return v, nil
}

// padTopic 把地址编码为 32 字节 topic。
func padTopic(addr string) string {
	return common.BytesToHash(common.HexToAddress(addr).Bytes()).Hex()
}

// topicToAddress 从 32 字节 topic 还原地址。
func topicToAddress(topic string) string {
	raw, err := hexDecodeSafe(topic)
	if err != nil || len(raw) < 32 {
		return ""
	}
	return common.BytesToAddress(raw[12:32]).Hex()
}

func hexDecodeSafe(s string) ([]byte, error) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "0x")
	if len(s)%2 != 0 {
		s = "0" + s
	}
	out := make([]byte, len(s)/2)
	for i := 0; i < len(out); i++ {
		b, err := strconv.ParseUint(s[i*2:i*2+2], 16, 8)
		if err != nil {
			return nil, err
		}
		out[i] = byte(b)
	}
	return out, nil
}
