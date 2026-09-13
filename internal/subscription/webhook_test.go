package subscription

import (
	"strconv"
	"testing"
	"time"
)

func TestSignAndVerify(t *testing.T) {
	secret := "whsec_test_123"
	body := []byte(`{"type":"payment.succeeded","data":{"order_id":"ord_1"}}`)
	ts := "1789309553"

	sig := SignPayload(secret, ts, body)
	if !VerifySignature(secret, ts, body, sig) {
		t.Fatal("正确签名应通过验签")
	}
	if VerifySignature(secret, ts, []byte(`{"x":1}`), sig) {
		t.Fatal("篡改 body 后必须验签失败")
	}
	if VerifySignature("wrong-secret", ts, body, sig) {
		t.Fatal("错误密钥必须验签失败")
	}
	if VerifySignature(secret, "1789309554", body, sig) {
		t.Fatal("篡改时间戳后必须验签失败")
	}
	if VerifySignature(secret, ts, body, "") {
		t.Fatal("空签名必须失败")
	}
	if VerifySignature("", ts, body, sig) {
		t.Fatal("空密钥必须失败")
	}
	if !VerifySignature(secret, ts, body, "sha256="+sig) {
		t.Fatal("应兼容 sha256= 前缀写法")
	}
}

func TestSignPayloadShape(t *testing.T) {
	body := []byte(`{"a":1}`)
	plain := SignPayload("k", "", body)
	withTS := SignPayload("k", "1", body)
	if plain == withTS {
		t.Fatal("带/不带时间戳的签名应不同")
	}
	if len(plain) != 64 {
		t.Fatalf("签名应为 hex(sha256)=64 字符，实际 %d", len(plain))
	}
}

func TestPaymentEventResolveOrderID(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"扁平", `{"order_id":"ord_flat"}`, "ord_flat"},
		{"嵌套", `{"type":"payment.succeeded","data":{"order_id":"ord_nested"}}`, "ord_nested"},
	}
	for _, c := range cases {
		var e paymentEvent
		if err := jsonUnmarshal([]byte(c.body), &e); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got := e.resolveOrderID(); got != c.want {
			t.Fatalf("%s: want %q got %q", c.name, c.want, got)
		}
	}
}

func TestPaymentEventSucceeded(t *testing.T) {
	var empty paymentEvent
	if !empty.succeeded() {
		t.Fatal("空 type 应视为成功事件")
	}
	for _, ok := range []string{"payment.succeeded", "PAYMENT_SUCCEEDED", "checkout.session.completed"} {
		e := paymentEvent{Type: ok}
		if !e.succeeded() {
			t.Fatalf("%s 应判定为成功事件", ok)
		}
	}
	for _, no := range []string{"payment.failed", "refund.created"} {
		e := paymentEvent{Type: no}
		if e.succeeded() {
			t.Fatalf("%s 不应判定为成功事件", no)
		}
	}
}

func TestVerifyTimestampWindow(t *testing.T) {
	h := NewWebhookHandler(nil, "s", 5*time.Minute, nil)
	if err := h.verifyTimestamp(strconv.FormatInt(time.Now().Unix(), 10)); err != nil {
		t.Fatalf("当前时间戳应通过：%v", err)
	}
	if err := h.verifyTimestamp(strconv.FormatInt(time.Now().Add(-time.Hour).Unix(), 10)); err == nil {
		t.Fatal("过期时间戳应被拒绝")
	}
	if err := h.verifyTimestamp(""); err == nil {
		t.Fatal("缺失时间戳应被拒绝")
	}
	if err := h.verifyTimestamp("not-a-number"); err == nil {
		t.Fatal("非法时间戳应被拒绝")
	}
	off := NewWebhookHandler(nil, "s", 0, nil)
	if err := off.verifyTimestamp(""); err != nil {
		t.Fatal("tolerance<=0 时应跳过时间戳校验")
	}
}

func TestConfiguredReflectsSecret(t *testing.T) {
	if NewWebhookHandler(nil, "", 0, nil).Configured() {
		t.Fatal("空密钥应视为未配置")
	}
	if !NewWebhookHandler(nil, "  whsec_x  ", 0, nil).Configured() {
		t.Fatal("非空密钥应视为已配置")
	}
}
