package signer

import (
	"math/big"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
)

// ethereumCallMsg 构造 eth_estimateGas 的调用消息。
func ethereumCallMsg(to common.Address, value *big.Int, data []byte) ethereum.CallMsg {
	if value == nil {
		value = big.NewInt(0)
	}
	return ethereum.CallMsg{To: &to, Value: value, Data: data}
}
