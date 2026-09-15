package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func goplusServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestSecurityFromGoPlusParsesAndFlagsRisky(t *testing.T) {
	body := `{"code":1,"message":"ok","result":{"0xT":{
	  "is_honeypot":"0","is_mintable":"1","is_blacklisted":"0","is_open_source":"1",
	  "owner_address":"0xdead","buy_tax":"0.05","sell_tax":"0.05"}}}`
	srv := goplusServer(t, http.StatusOK, body)
	s := NewSecurityWithBaseURL("key", nil, srv.URL)

	rep, err := s.Security(context.Background(), "base", "0xT")
	if err != nil {
		t.Fatalf("Security 失败：%v", err)
	}
	if rep.Source != "goplus" {
		t.Fatalf("来源应为 goplus：%q", rep.Source)
	}
	if rep.IsHoneypot {
		t.Fatal("is_honeypot=0 不应判为蜜罐")
	}
	if !rep.HasMint {
		t.Fatal("is_mintable=1 应标记可增发")
	}
	if rep.HasBlacklist {
		t.Fatal("is_blacklisted=0 不应标记黑名单")
	}
	if !rep.IsOpenSource {
		t.Fatal("is_open_source=1 应标记开源")
	}
	if rep.OwnershipRenounced {
		t.Fatal("owner_address 非空不应视为已放弃权限")
	}
	if rep.BuyTax != 0.05 || rep.SellTax != 0.05 {
		t.Fatalf("税率解析错误：buy=%v sell=%v", rep.BuyTax, rep.SellTax)
	}
	if !rep.Risky {
		t.Fatal("存在增发权限（且未放弃权限）应综合判为 Risky")
	}
}

func TestSecurityMarksRenouncedOwnerAsSafeOnThatAxis(t *testing.T) {
	body := `{"code":1,"result":{"0xT":{"is_open_source":"1","is_mintable":"0","is_honeypot":"0","is_blacklisted":"0","owner_address":"","buy_tax":"0","sell_tax":"0"}}}`
	srv := goplusServer(t, http.StatusOK, body)
	s := NewSecurityWithBaseURL("", nil, srv.URL)

	rep, err := s.Security(context.Background(), "base", "0xT")
	if err != nil {
		t.Fatalf("Security 失败：%v", err)
	}
	if !rep.OwnershipRenounced {
		t.Fatal("owner_address 为空应视为已放弃权限")
	}
	if rep.Risky {
		t.Fatalf("无风险特征的合约不应判为 Risky：%+v", rep)
	}
}

func TestSecurityFallsBackWhenGoPlusCodeNotOne(t *testing.T) {
	srv := goplusServer(t, http.StatusOK, `{"code":0,"message":"rate limited","result":{}}`)
	s := NewSecurityWithBaseURL("", nil, srv.URL)

	rep, err := s.Security(context.Background(), "base", "0xT")
	if err != nil {
		t.Fatalf("上游限流应降级而非报错：%v", err)
	}
	if rep.Source != "fallback" {
		t.Fatalf("应走兜底报告：%q", rep.Source)
	}
	if !rep.Risky {
		t.Fatal("兜底报告必须标记 Risky（禁止自动放行未知合约）")
	}
}

func TestSecurityFallsBackOnHTTPError(t *testing.T) {
	srv := goplusServer(t, http.StatusInternalServerError, "boom")
	s := NewSecurityWithBaseURL("", nil, srv.URL)

	rep, err := s.Security(context.Background(), "base", "0xT")
	if err != nil {
		t.Fatalf("HTTP 错误应降级：%v", err)
	}
	if rep.Source != "fallback" || !rep.Risky {
		t.Fatalf("应返回 Risky 兜底报告：%+v", rep)
	}
}

func TestSecurityCachesReportAcrossCalls(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		_, _ = w.Write([]byte(`{"code":1,"result":{"0xT":{"is_open_source":"1","is_mintable":"0","is_honeypot":"0","is_blacklisted":"0","owner_address":"","buy_tax":"0","sell_tax":"0"}}}`))
	}))
	t.Cleanup(srv.Close)
	s := NewSecurityWithBaseURL("", nil, srv.URL)

	for i := 0; i < 3; i++ {
		if _, err := s.Security(context.Background(), "base", "0xT"); err != nil {
			t.Fatalf("第 %d 次调用失败：%v", i+1, err)
		}
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("应命中缓存（仅 1 次上游请求），实际 %d", n)
	}
}

func TestSecurityConcentrationParsesHolders(t *testing.T) {
	body := `{"code":1,"result":{"0xT":{"top_10_holder_rate":"0.42","top_20_holder_rate":"0.61","holder_count":"1234"}}}`
	srv := goplusServer(t, http.StatusOK, body)
	s := NewSecurityWithBaseURL("", nil, srv.URL)

	c, err := s.Concentration(context.Background(), "base", "0xT", 10)
	if err != nil {
		t.Fatalf("Concentration 失败：%v", err)
	}
	if c.Top10Percent != 0.42 || c.Top20Percent != 0.61 || c.HolderCount != 1234 {
		t.Fatalf("持仓集中度解析错误：%+v", c)
	}
}

func TestSecurityConcentrationReportsMissingData(t *testing.T) {
	srv := goplusServer(t, http.StatusOK, `{"code":1,"result":{}}`)
	s := NewSecurityWithBaseURL("", nil, srv.URL)

	_, err := s.Concentration(context.Background(), "base", "0xT", 10)
	if err == nil || !strings.Contains(err.Error(), "no holder data") {
		t.Fatalf("无持仓数据应显式报错：%v", err)
	}
}
