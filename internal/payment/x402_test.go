package payment

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// 测试专用私钥（非真实资产）。
const testPrivKey = "4c0883a69102937d6231471b5dbb6204fe5129617082792ae468d01a3f362318"

const (
	baseUSDC = "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913"
	deadAddr = "0x000000000000000000000000000000000000dEaD"
)

func requirement402(body string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if payment := r.Header.Get("X-PAYMENT"); payment != "" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = w.Write([]byte(body))
	}))
}

func TestEIP3009SignatureRecoversSigner(t *testing.T) {
	payer, err := NewEVMExactPayer(testPrivKey)
	if err != nil {
		t.Fatalf("构建 payer 失败：%v", err)
	}

	req := Requirement{
		Scheme:            "exact",
		Network:           "base",
		Asset:             baseUSDC,
		PayTo:             deadAddr,
		MaxAmountRequired: "10000",
		MaxTimeoutSeconds: 60,
	}
	if !payer.Supports(req.Network, req.Asset) {
		t.Fatal("应支持 base 上的 USDC")
	}

	payload, err := payer.CreatePayload(context.Background(), req)
	if err != nil {
		t.Fatalf("生成凭证失败：%v", err)
	}

	var decoded struct {
		Signature     string `json:"signature"`
		Authorization struct {
			From        string `json:"from"`
			To          string `json:"to"`
			Value       string `json:"value"`
			ValidAfter  string `json:"validAfter"`
			ValidBefore string `json:"validBefore"`
			Nonce       string `json:"nonce"`
		} `json:"authorization"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("凭证不是预期结构：%v", err)
	}

	if !strings.EqualFold(decoded.Authorization.From, payer.Address()) {
		t.Fatalf("from 应为付款地址：%s", decoded.Authorization.From)
	}
	if !strings.EqualFold(decoded.Authorization.To, req.PayTo) {
		t.Fatalf("to 应为 payTo：%s", decoded.Authorization.To)
	}
	if decoded.Authorization.Value != req.MaxAmountRequired {
		t.Fatalf("金额应等于 maxAmountRequired：%s", decoded.Authorization.Value)
	}

	info := DefaultAssets()[assetKey("base", req.Asset)]
	sig, err := hex.DecodeString(strings.TrimPrefix(decoded.Signature, "0x"))
	if err != nil || len(sig) != 65 {
		t.Fatalf("签名应为 65 字节：len=%d err=%v", len(sig), err)
	}
	if sig[64] != 27 && sig[64] != 28 {
		t.Fatalf("EIP-3009 的 v 应为 27/28，实际 %d", sig[64])
	}

	value, _ := parseAmount(decoded.Authorization.Value)
	validAfter, _ := parseAmount(decoded.Authorization.ValidAfter)
	validBefore, _ := parseAmount(decoded.Authorization.ValidBefore)
	nonce, _ := hex.DecodeString(strings.TrimPrefix(decoded.Authorization.Nonce, "0x"))

	digest := EIP3009Digest(info, 8453,
		common.HexToAddress(decoded.Authorization.From),
		common.HexToAddress(decoded.Authorization.To),
		value, validAfter, validBefore, nonce)

	// EIP-3009 的 v 为 27/28（链上 ecrecover 要求），故用 Ecrecover 对齐验证
	recovered, err := recoverAddress(digest, sig)
	if err != nil {
		t.Fatalf("恢复公钥失败：%v", err)
	}
	if !strings.EqualFold(recovered, payer.Address()) {
		t.Fatalf("签名恢复地址不一致：%s != %s", recovered, payer.Address())
	}

	// 有效期语义：validAfter < 现在 < validBefore
	nowUnix := big.NewInt(time.Now().Unix())
	if validAfter.Cmp(nowUnix) >= 0 {
		t.Fatal("validAfter 应早于当前时间")
	}
	if validBefore.Cmp(nowUnix) <= 0 {
		t.Fatal("validBefore 应晚于当前时间")
	}

	// 篡改金额后，同一签名不应再恢复出付款地址（证明摘要覆盖金额）
	tampered := EIP3009Digest(info, 8453,
		common.HexToAddress(decoded.Authorization.From),
		common.HexToAddress(decoded.Authorization.To),
		big.NewInt(99999), validAfter, validBefore, nonce)
	if addr, err := recoverAddress(tampered, sig); err == nil {
		if strings.EqualFold(addr, payer.Address()) {
			t.Fatal("篡改金额后不应恢复出同一地址")
		}
	}
}

func TestClientPaysAndRetriesOn402(t *testing.T) {
	payer, err := NewEVMExactPayer(testPrivKey)
	if err != nil {
		t.Fatalf("构建 payer 失败：%v", err)
	}

	body := `{"x402Version":1,"accepts":[{"scheme":"exact","network":"base","asset":"` + baseUSDC +
		`","payTo":"` + deadAddr + `","maxAmountRequired":"1000","maxTimeoutSeconds":60}]}`
	srv := requirement402(body)
	defer srv.Close()

	var seen string
	inner := srv.Config.Handler
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h := r.Header.Get("X-PAYMENT"); h != "" {
			seen = h
		}
		inner.ServeHTTP(w, r)
	})

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/paid", nil)
	resp, err := NewClient(payer, 5*time.Second).Do(context.Background(), req)
	if err != nil {
		t.Fatalf("请求失败：%v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("支付后应返回 200，实际 %d", resp.StatusCode)
	}
	if seen == "" {
		t.Fatal("服务端应收到 X-PAYMENT 头")
	}

	envelope, err := DecodePaymentHeader(seen)
	if err != nil {
		t.Fatalf("凭证无法解码：%v", err)
	}
	if envelope.Scheme != "exact" || envelope.Network != "base" || envelope.X402Version != 1 {
		t.Fatalf("凭证信封字段异常：%+v", envelope)
	}
	if len(envelope.Payload) == 0 {
		t.Fatal("payload 不应为空")
	}
}

func TestClientRejectsOverLimit(t *testing.T) {
	payer, _ := NewEVMExactPayer(testPrivKey)
	body := `{"accepts":[{"scheme":"exact","network":"base","asset":"` + baseUSDC +
		`","payTo":"` + deadAddr + `","maxAmountRequired":"1000000"}]}`
	srv := requirement402(body)
	defer srv.Close()

	client := NewClient(payer, 5*time.Second)
	client.MaxPayAmount = "5000"

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/paid", nil)
	if _, err := client.Do(context.Background(), req); err == nil {
		t.Fatal("超过单次上限必须拒绝支付")
	} else {
		var unpayable *UnpayableError
		if !errors.As(err, &unpayable) {
			t.Fatalf("应为 UnpayableError，实际 %v", err)
		}
		if !strings.Contains(unpayable.Reason, "超过单次上限") {
			t.Fatalf("拒绝原因不明确：%s", unpayable.Reason)
		}
	}
}

func TestClientRejectsUnsupportedAsset(t *testing.T) {
	payer, _ := NewEVMExactPayer(testPrivKey)
	body := `{"accepts":[{"scheme":"exact","network":"base","asset":"0x0000000000000000000000000000000000000001","payTo":"` + deadAddr + `","maxAmountRequired":"1"}]}`
	srv := requirement402(body)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/paid", nil)
	if _, err := NewClient(payer, 5*time.Second).Do(context.Background(), req); err == nil {
		t.Fatal("不支持的资产必须拒绝")
	}
}

func TestParseRequirementsVariants(t *testing.T) {
	list, err := ParseRequirements([]byte(`{"x402Version":1,"accepts":[{"payTo":"0xabc","network":"base"}]}`))
	if err != nil || len(list) != 1 || list[0].Network != "base" {
		t.Fatalf("accepts 数组解析失败：%v %+v", err, list)
	}

	single, err := ParseRequirements([]byte(`{"scheme":"exact","payTo":"0xabc","network":"polygon"}`))
	if err != nil || len(single) != 1 || single[0].Network != "polygon" {
		t.Fatalf("单对象解析失败：%v %+v", err, single)
	}

	if _, err := ParseRequirements([]byte(`{}`)); err == nil {
		t.Fatal("缺少 payTo 应报错")
	}
	if _, err := ParseRequirements(nil); err == nil {
		t.Fatal("空响应体应报错")
	}
}

func TestPaymentHeaderRoundTrip(t *testing.T) {
	header, err := encodePaymentHeader(Requirement{Scheme: "", Network: "base"}, json.RawMessage(`{"signature":"0x00"}`))
	if err != nil {
		t.Fatalf("编码失败：%v", err)
	}
	envelope, err := DecodePaymentHeader(header)
	if err != nil {
		t.Fatalf("解码失败：%v", err)
	}
	if envelope.Scheme != "exact" {
		t.Fatalf("空 scheme 应回退为 exact，实际 %s", envelope.Scheme)
	}
	if envelope.Network != "base" || envelope.X402Version != 1 {
		t.Fatalf("信封字段异常：%+v", envelope)
	}

	if _, err := DecodePaymentHeader("not-base64!!"); err == nil {
		t.Fatal("非法 base64 应报错")
	}
}

// recoverAddress 恢复签名者地址。
//
// 说明：产物按链上 ecrecover 规范使用 v = 27/28，而 go-ethereum 的本地恢复函数
// 要求 compact 形式（v = 0/1），故此处做一次转换（也顺带验证了两者的一致性）。
func recoverAddress(digest, sig []byte) (string, error) {
	compact := make([]byte, len(sig))
	copy(compact, sig)
	if compact[64] >= 27 {
		compact[64] -= 27
	}
	pubBytes, err := crypto.Ecrecover(digest, compact)
	if err != nil {
		return "", err
	}
	pub, err := crypto.UnmarshalPubkey(pubBytes)
	if err != nil {
		return "", err
	}
	return crypto.PubkeyToAddress(*pub).Hex(), nil
}
