package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCacheTTLAndMisses(t *testing.T) {
	c := newCache(40 * time.Millisecond)
	if _, ok := c.get("missing"); ok {
		t.Fatal("未写入的 key 不应命中")
	}

	c.put("k", "v")
	if v, ok := c.get("k"); !ok || v != "v" {
		t.Fatalf("应命中缓存：%v %v", v, ok)
	}

	time.Sleep(60 * time.Millisecond)
	if _, ok := c.get("k"); ok {
		t.Fatal("TTL 过期后不应命中（避免返回陈旧数据）")
	}
}

func TestClipBoundsLogOutput(t *testing.T) {
	if got := clip("  short  ", 20); got != "short" {
		t.Fatalf("应去除首尾空白：%q", got)
	}
	if got := clip("", 10); got != "" {
		t.Fatalf("空串应返回空串：%q", got)
	}

	long := strings.Repeat("a", 300)
	got := clip(long, 160)
	if len(got) != 163 || !strings.HasSuffix(got, "...") {
		t.Fatalf("超长应截断为 160 字符加省略号：len=%d", len(got))
	}
	if got := clip(strings.Repeat("b", 10), 10); got != "bbbbbbbbbb" {
		t.Fatalf("恰好等长不应截断：%q", got)
	}
}

func TestChainIDMappings(t *testing.T) {
	api := map[string]string{
		"base": "base", "BASE": "base", "solana": "solana", "bsc": "bsc",
		"ethereum": "ethereum", "eth": "ethereum", "arbitrum": "arbitrum",
		"unknown-chain": "unknown-chain",
	}
	for in, want := range api {
		if got := chainIDForAPI(in); got != want {
			t.Fatalf("chainIDForAPI(%q) = %q，期望 %q", in, got, want)
		}
	}

	// GoPlus 对 EVM 用数字链 ID，Solana 用字符串
	goplus := map[string]string{
		"base": "8453", "Base": "8453", "bsc": "56", "ethereum": "1",
		"eth": "1", "arbitrum": "42161", "solana": "solana",
	}
	for in, want := range goplus {
		if got := goplusChainID(in); got != want {
			t.Fatalf("goplusChainID(%q) = %q，期望 %q", in, got, want)
		}
	}
}

func TestHTTPGetJSONSuccessAndHeaders(t *testing.T) {
	var gotAccept, gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAccept = r.Header.Get("Accept")
		gotKey = r.Header.Get("X-Api-Key")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"name":"ok","value":3}`))
	}))
	defer srv.Close()

	var out struct {
		Name  string `json:"name"`
		Value int    `json:"value"`
	}
	err := httpGetJSON(context.Background(), srv.Client(), srv.URL, map[string]string{"X-Api-Key": "k1"}, &out)
	if err != nil {
		t.Fatalf("正常响应应解析成功：%v", err)
	}
	if out.Name != "ok" || out.Value != 3 {
		t.Fatalf("解析结果错误：%+v", out)
	}
	if gotAccept != "application/json" {
		t.Fatalf("应声明 Accept: application/json，实际 %q", gotAccept)
	}
	if gotKey != "k1" {
		t.Fatalf("自定义头未传递：%q", gotKey)
	}
}

func TestHTTPGetJSONSkipsEmptyHeaderValues(t *testing.T) {
	var gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("X-Api-Key")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	var out map[string]any
	if err := httpGetJSON(context.Background(), srv.Client(), srv.URL, map[string]string{"X-Api-Key": ""}, &out); err != nil {
		t.Fatalf("空 header 值不应导致失败：%v", err)
	}
	if gotKey != "" {
		t.Fatalf("空 header 值不应被设置：%q", gotKey)
	}
}

func TestHTTPGetJSONDecodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{not json`))
	}))
	defer srv.Close()

	var out map[string]any
	err := httpGetJSON(context.Background(), srv.Client(), srv.URL, nil, &out)
	if err == nil || !strings.Contains(err.Error(), "decode json") {
		t.Fatalf("非法 JSON 应报解码错误：%v", err)
	}
}

func TestHTTPGetJSONNon200TruncatesBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(strings.Repeat("e", 500)))
	}))
	defer srv.Close()

	var out map[string]any
	err := httpGetJSON(context.Background(), srv.Client(), srv.URL, nil, &out)
	if err == nil || !strings.Contains(err.Error(), "http 429") {
		t.Fatalf("非 200 应报状态码：%v", err)
	}
	if len(err.Error()) > 200 {
		t.Fatalf("错误信息应被截断以防日志爆炸：len=%d", len(err.Error()))
	}
}

func TestHTTPGetJSONHonorsCancelledContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out map[string]any
	if err := httpGetJSON(ctx, srv.Client(), srv.URL, nil, &out); err == nil {
		t.Fatal("已取消的 ctx 应返回错误（避免上游超时后仍等待）")
	}
}
