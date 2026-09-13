package base

import (
	"context"
	"fmt"
	"math/big"
	"sync"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

// EVM 侧的最小合约调用编码：只实现必要的 view 方法，避免引入完整 ABI 依赖。
//
// 需要的函数选择器（前 4 字节）：
//
//	balanceOf(address)  -> 0x70a08231
//	decimals()          -> 0x313ce567
//	token0()            -> 0x0dfe1681
//	token1()            -> 0xd21220a7
var (
	selectorBalanceOf = []byte{0x70, 0xa0, 0x82, 0x31}
	selectorDecimals  = []byte{0x31, 0x3c, 0xe5, 0x67}
	selectorToken0    = []byte{0x0d, 0xfe, 0x16, 0x81}
	selectorToken1    = []byte{0xd2, 0x12, 0x20, 0xa7}
)

// addressArg 把地址编码为 32 字节左填充参数。
func addressArg(addr common.Address) []byte {
	out := make([]byte, 32)
	copy(out[12:], addr.Bytes())
	return out
}

func balanceOfData(owner common.Address) []byte {
	return append(append([]byte{}, selectorBalanceOf...), addressArg(owner)...)
}

func noArgData(selector []byte) []byte {
	return append([]byte{}, selector...)
}

// decodeUint 解析 32 字节大端无符号整数。
func decodeUint(out []byte) (*big.Int, error) {
	if len(out) < 32 {
		return nil, fmt.Errorf("evm: short return (%d bytes)", len(out))
	}
	return new(big.Int).SetBytes(out[:32]), nil
}

// decodeUint8 解析单字节返回值（decimals）。
func decodeUint8(out []byte) (uint8, error) {
	if len(out) < 32 {
		return 0, fmt.Errorf("evm: short return (%d bytes)", len(out))
	}
	v := new(big.Int).SetBytes(out[:32])
	if !v.IsUint64() || v.Uint64() > 255 {
		return 0, fmt.Errorf("evm: value out of uint8 range")
	}
	return uint8(v.Uint64()), nil
}

// decodeAddress 解析 32 字节返回的地址。
func decodeAddress(out []byte) (common.Address, error) {
	if len(out) < 32 {
		return common.Address{}, fmt.Errorf("evm: short return (%d bytes)", len(out))
	}
	return common.BytesToAddress(out[12:32]), nil
}

// v2SwapTopic 计算 Uniswap V2 风格 Swap 事件的 topic0。
func v2SwapTopic() common.Hash {
	return crypto.Keccak256Hash([]byte("Swap(address,uint256,uint256,uint256,uint256,address)"))
}

// v2PairCreatedTopic 计算 PairCreated 事件 topic0（用于新池发现）。
func v2PairCreatedTopic() common.Hash {
	return crypto.Keccak256Hash([]byte("PairCreated(address,address,address,uint256)"))
}

// v3SwapTopic 计算 Uniswap V3 Swap 事件 topic0。
func v3SwapTopic() common.Hash {
	return crypto.Keccak256Hash([]byte("Swap(address,address,int256,int256,uint160,uint128,int24)"))
}

// evmRPC 封装单个 ethclient，提供类型安全的只读调用与元数据缓存。
type evmRPC struct {
	client *ethclient.Client

	mu       sync.RWMutex
	decimals map[string]uint8
	pairs    map[string][2]string // pool -> [token0, token1]
}

func newEVMRPC(client *ethclient.Client) *evmRPC {
	return &evmRPC{
		client:   client,
		decimals: make(map[string]uint8),
		pairs:    make(map[string][2]string),
	}
}

// TokenDecimals 读取 ERC20 decimals（带缓存）。
func (e *evmRPC) TokenDecimals(ctx context.Context, token common.Address) (uint8, error) {
	key := token.Hex()
	e.mu.RLock()
	if d, ok := e.decimals[key]; ok {
		e.mu.RUnlock()
		return d, nil
	}
	e.mu.RUnlock()

	to := token
	out, err := e.client.CallContract(ctx, callMsg(to, noArgData(selectorDecimals)), nil)
	if err != nil {
		return 0, fmt.Errorf("decimals(%s): %w", key, err)
	}
	d, err := decodeUint8(out)
	if err != nil {
		return 0, err
	}
	e.mu.Lock()
	e.decimals[key] = d
	e.mu.Unlock()
	return d, nil
}

// ERC20Balance 读取 ERC20 余额。
func (e *evmRPC) ERC20Balance(ctx context.Context, token, owner common.Address) (*big.Int, error) {
	to := token
	out, err := e.client.CallContract(ctx, callMsg(to, balanceOfData(owner)), nil)
	if err != nil {
		return nil, fmt.Errorf("balanceOf(%s): %w", token.Hex(), err)
	}
	return decodeUint(out)
}

// PoolTokens 读取 V2 池子的 token0 / token1（带缓存）。
func (e *evmRPC) PoolTokens(ctx context.Context, pool common.Address) (string, string, error) {
	key := pool.Hex()
	e.mu.RLock()
	if p, ok := e.pairs[key]; ok {
		e.mu.RUnlock()
		return p[0], p[1], nil
	}
	e.mu.RUnlock()

	to := pool
	out0, err := e.client.CallContract(ctx, callMsg(to, noArgData(selectorToken0)), nil)
	if err != nil {
		return "", "", fmt.Errorf("token0(%s): %w", key, err)
	}
	t0, err := decodeAddress(out0)
	if err != nil {
		return "", "", err
	}
	out1, err := e.client.CallContract(ctx, callMsg(to, noArgData(selectorToken1)), nil)
	if err != nil {
		return "", "", fmt.Errorf("token1(%s): %w", key, err)
	}
	t1, err := decodeAddress(out1)
	if err != nil {
		return "", "", err
	}

	e.mu.Lock()
	e.pairs[key] = [2]string{t0.Hex(), t1.Hex()}
	e.mu.Unlock()
	return t0.Hex(), t1.Hex(), nil
}

// HasCode 判断地址是否为合约（用于过滤 EOA / 校验池子地址）。
func (e *evmRPC) HasCode(ctx context.Context, addr common.Address) (bool, error) {
	code, err := e.client.CodeAt(ctx, addr, nil)
	if err != nil {
		return false, err
	}
	return len(code) > 0, nil
}

func callMsg(to common.Address, data []byte) ethereumCallMsg {
	return ethereumCallMsg{To: &to, Data: data}
}

// hexToUint64 解析 hex 数量（如 gasPrice）。
func hexToUint64(s string) (uint64, error) {
	if s == "" {
		return 0, nil
	}
	v, err := hexutil.DecodeUint64(s)
	if err != nil {
		return 0, err
	}
	return v, nil
}

// receiptSucceeded 判断交易是否成功。
func receiptSucceeded(status uint64) bool { return status == types.ReceiptStatusSuccessful }
