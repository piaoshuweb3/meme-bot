package base

import (
	"bytes"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

func TestAddressArgAndCallData(t *testing.T) {
	addr := common.HexToAddress("0x10687368eF1be3f178de0fCCf5EdfF49e1C258B1")

	arg := addressArg(addr)
	if len(arg) != 32 {
		t.Fatalf("ABI 参数应恰好 32 字节：%d", len(arg))
	}
	for i := 0; i < 12; i++ {
		if arg[i] != 0 {
			t.Fatalf("前 12 字节应为左填充零：%x", arg[:12])
		}
	}
	if common.BytesToAddress(arg[12:]) != addr {
		t.Fatal("地址编码偏移错误")
	}

	data := balanceOfData(addr)
	if len(data) != 36 {
		t.Fatalf("balanceOf 调用数据应为 4+32 字节：%d", len(data))
	}
	if !bytes.Equal(data[:4], selectorBalanceOf) {
		t.Fatalf("选择器错误：%x", data[:4])
	}
	if common.BytesToAddress(data[16:36]) != addr {
		t.Fatal("参数应紧跟选择器之后（4..36）")
	}

	if got := noArgData(selectorDecimals); len(got) != 4 || !bytes.Equal(got, selectorDecimals) {
		t.Fatalf("无参调用应只含选择器：%x", got)
	}
	// 不得共享底层数组：调用方修改不能污染包级选择器常量
	probe := noArgData(selectorDecimals)
	probe[0] = 0xff
	if selectorDecimals[0] == 0xff {
		t.Fatal("noArgData 返回了共享底层数组的切片（会污染常量）")
	}
}

func TestDecodeUintBounds(t *testing.T) {
	if _, err := decodeUint(make([]byte, 31)); err == nil {
		t.Fatal("短返回应报错（避免静默读到错误金额）")
	}
	word := make([]byte, 32)
	word[31] = 42
	if v, err := decodeUint(word); err != nil || v.Int64() != 42 {
		t.Fatalf("解析失败：%v %v", v, err)
	}

	// 超过 uint64 的大数应完整保留为 big.Int（不得截断或溢出）
	huge := make([]byte, 32)
	huge[0] = 1 // 2^248
	v, err := decodeUint(huge)
	if err != nil {
		t.Fatalf("大数应可解析：%v", err)
	}
	if v.IsUint64() {
		t.Fatalf("2^248 超出 uint64，却仍被视为 uint64（说明发生了截断）：%v", v)
	}
	if v.BitLen() != 249 {
		t.Fatalf("精度丢失：期望 249 位，实际 %d 位（%v）", v.BitLen(), v)
	}
}

func TestDecodeUint8RejectsOutOfRange(t *testing.T) {
	if _, err := decodeUint8(make([]byte, 31)); err == nil {
		t.Fatal("短返回应报错")
	}
	ten := make([]byte, 32)
	ten[31] = 18
	if v, err := decodeUint8(ten); err != nil || v != 18 {
		t.Fatalf("decimals 解析失败：%v %v", v, err)
	}
	tooBig := make([]byte, 32)
	tooBig[30] = 1 // 256
	if _, err := decodeUint8(tooBig); err == nil {
		t.Fatal("超出 uint8 应报错（否则精度会被静默截断）")
	}
}

func TestDecodeAddressIgnoresHighBytes(t *testing.T) {
	if _, err := decodeAddress(make([]byte, 31)); err == nil {
		t.Fatal("短返回应报错")
	}
	addr := common.HexToAddress("0x10687368eF1be3f178de0fCCf5EdfF49e1C258B1")
	word := make([]byte, 32)
	copy(word[12:], addr.Bytes())
	if got, err := decodeAddress(word); err != nil || got != addr {
		t.Fatalf("地址解析失败：%v %v", got, err)
	}

	dirty := make([]byte, 32)
	dirty[0] = 0xff // 高 12 字节非零
	copy(dirty[12:], addr.Bytes())
	if got, _ := decodeAddress(dirty); got != addr {
		t.Fatalf("EVM ABI 语义应忽略高 12 字节：%v", got)
	}
}

func TestEventTopicsMatchKnownSignatures(t *testing.T) {
	cases := []struct {
		name  string
		got   common.Hash
		sig   string
		known string
	}{
		{"V2 Swap", v2SwapTopic(), "Swap(address,uint256,uint256,uint256,uint256,address)",
			"0xd78ad95fa46c994b6551d0da85fc275fe613ce37657fb8d5e3d130840159d822"},
		{"V2 PairCreated", v2PairCreatedTopic(), "PairCreated(address,address,address,uint256)",
			"0x0d3648bd0f6ba80134a33ba9275ac585d9d315f0ad8355cddefde31afa28d0e9"},
		{"V3 Swap", v3SwapTopic(), "Swap(address,address,int256,int256,uint160,uint128,int24)",
			"0xc42079f94a6350d7e6235f29174924f928cc2ac818eb64fed8004e115fbcca67"},
	}
	for _, c := range cases {
		if want := crypto.Keccak256Hash([]byte(c.sig)); c.got != want {
			t.Fatalf("%s：topic 与签名串不一致（got %s want %s）", c.name, c.got.Hex(), want.Hex())
		}
		if c.got.Hex() != c.known {
			t.Fatalf("%s：topic 与公开已知值不符（got %s）——签名串可能拼错，会导致漏事件", c.name, c.got.Hex())
		}
	}
}

func TestHexToUint64(t *testing.T) {
	if v, err := hexToUint64(""); err != nil || v != 0 {
		t.Fatalf("空串应返回 0：%v %v", v, err)
	}
	if v, err := hexToUint64("0x2540be400"); err != nil || v != 10000000000 {
		t.Fatalf("解析失败：%v %v", v, err)
	}
	if _, err := hexToUint64("not-hex"); err == nil {
		t.Fatal("非法 hex 应报错")
	}
}

func TestReceiptSucceeded(t *testing.T) {
	if !receiptSucceeded(types.ReceiptStatusSuccessful) {
		t.Fatalf("状态 %d 应视为成功", types.ReceiptStatusSuccessful)
	}
	if receiptSucceeded(0) {
		t.Fatal("状态 0 应视为失败（否则会把失败交易记为成交）")
	}
}
