package model

import "context"

// SwapSigner 交易签名器（EVM）。
//
// 为什么单独抽象：执行层只依赖契约，不应直接持有私钥或依赖具体签名实现。
// 实现位于 internal/chain/signer；dry_run 模式下由执行层跳过签名步骤。
type SwapSigner interface {
	// Address 返回该链上的签名地址。
	Address(chain string) (string, error)
	// SignSwap 补齐 nonce / gas / chainID 并签名，返回可直接广播的原始交易（hex，带 0x）。
	SignSwap(ctx context.Context, chain string, unsigned *UnsignedTx, params SwapParams) (rawTxHex string, err error)
}
