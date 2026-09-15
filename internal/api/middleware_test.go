package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"meme-bot/internal/user"
)

const testSecret = "test-secret-0123456789abcdef"

func init() { gin.SetMode(gin.TestMode) }

func newTestRouter(mw ...gin.HandlerFunc) *gin.Engine {
	r := gin.New()
	for _, m := range mw {
		r.Use(m)
	}
	r.GET("/probe", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"user_id": currentUserID(c), "role": currentRole(c)})
	})
	return r
}

func do(r *gin.Engine, method, path string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func bearer(t *testing.T, secret string, claims user.Claims, ttl time.Duration) string {
	t.Helper()
	tok, err := user.SignToken(secret, claims, ttl)
	if err != nil {
		t.Fatalf("签发令牌失败：%v", err)
	}
	return "Bearer " + tok
}

func TestJWTAuthRejectsMissingOrMalformedHeader(t *testing.T) {
	r := newTestRouter(JWTAuth(testSecret))

	if w := do(r, "GET", "/probe", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("无 Authorization 应 401，实际 %d", w.Code)
	}
	if w := do(r, "GET", "/probe", map[string]string{"Authorization": "Token abc"}); w.Code != http.StatusUnauthorized {
		t.Fatalf("非 Bearer 前缀应 401，实际 %d", w.Code)
	}
	if w := do(r, "GET", "/probe", map[string]string{"Authorization": "Bearer "}); w.Code != http.StatusUnauthorized {
		t.Fatalf("空令牌应 401，实际 %d", w.Code)
	}
}

func TestJWTAuthRejectsInvalidAndExpiredToken(t *testing.T) {
	r := newTestRouter(JWTAuth(testSecret))

	if w := do(r, "GET", "/probe", map[string]string{"Authorization": "Bearer not-a-jwt"}); w.Code != http.StatusUnauthorized {
		t.Fatalf("乱码令牌应 401，实际 %d", w.Code)
	}

	// 用另一把密钥签发 → 签名校验必须失败（防止伪造）
	wrong := bearer(t, "another-secret-0123456789abcdef", user.Claims{UserID: 1, Role: "admin"}, time.Hour)
	if w := do(r, "GET", "/probe", map[string]string{"Authorization": wrong}); w.Code != http.StatusUnauthorized {
		t.Fatalf("异密钥签发的令牌应被拒绝，实际 %d", w.Code)
	}

	// 过期令牌：显式指定过去的 Exp（SignToken 对非正 TTL 会 fail-fast）
	expiredTok, err := user.SignToken(testSecret,
		user.Claims{UserID: 1, Role: "admin", Exp: time.Now().Add(-time.Hour).Unix()}, time.Hour)
	if err != nil {
		t.Fatalf("签发过期令牌失败：%v", err)
	}
	if w := do(r, "GET", "/probe", map[string]string{"Authorization": "Bearer " + expiredTok}); w.Code != http.StatusUnauthorized {
		t.Fatalf("过期令牌应被拒绝，实际 %d", w.Code)
	}
}

func TestJWTAuthInjectsClaims(t *testing.T) {
	r := newTestRouter(JWTAuth(testSecret))

	w := do(r, "GET", "/probe", map[string]string{
		"Authorization": bearer(t, testSecret, user.Claims{UserID: 42, Role: "admin"}, time.Hour),
	})
	if w.Code != http.StatusOK {
		t.Fatalf("合法令牌应放行：%d %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `"user_id":42`) || !strings.Contains(body, `"role":"admin"`) {
		t.Fatalf("claim 未注入上下文：%s", body)
	}
}

func TestRequireRoleDeniesAndAllows(t *testing.T) {
	admin := bearer(t, testSecret, user.Claims{UserID: 1, Role: "admin"}, time.Hour)
	member := bearer(t, testSecret, user.Claims{UserID: 2, Role: "user"}, time.Hour)

	r := newTestRouter(JWTAuth(testSecret), RequireRole("admin"))
	if w := do(r, "GET", "/probe", map[string]string{"Authorization": member}); w.Code != http.StatusForbidden {
		t.Fatalf("普通用户访问管理路由应 403，实际 %d", w.Code)
	}
	if w := do(r, "GET", "/probe", map[string]string{"Authorization": admin}); w.Code != http.StatusOK {
		t.Fatalf("管理员应放行，实际 %d", w.Code)
	}

	// 未经 JWTAuth（上下文中无角色）必须拒绝，而不是默认放行
	bare := newTestRouter(RequireRole("admin"))
	if w := do(bare, "GET", "/probe", nil); w.Code != http.StatusForbidden {
		t.Fatalf("缺失角色上下文应 403（不得默认放行），实际 %d", w.Code)
	}
}

func TestAPIKeyAuthRejectsMissingKey(t *testing.T) {
	// 未提供 X-API-Key 时应在查库之前短路（因此可传 nil service）
	r := gin.New()
	r.Use(APIKeyAuth(nil, nil))
	r.GET("/x", func(c *gin.Context) { c.Status(http.StatusOK) })

	if w := do(r, "GET", "/x", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("缺少 X-API-Key 应 401，实际 %d", w.Code)
	}
	if w := do(r, "GET", "/x", map[string]string{"X-API-Key": "   "}); w.Code != http.StatusUnauthorized {
		t.Fatalf("空白 X-API-Key 应 401，实际 %d", w.Code)
	}
}

func TestCORSWhitelistAndPreflight(t *testing.T) {
	r := gin.New()
	r.Use(CORS([]string{"http://localhost:3000"}))
	r.GET("/x", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{}) })

	w := do(r, "GET", "/x", map[string]string{"Origin": "http://localhost:3000"})
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:3000" {
		t.Fatalf("白名单来源应回显：%q", got)
	}

	w = do(r, "GET", "/x", map[string]string{"Origin": "http://evil.example"})
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("非白名单来源不得回显（否则等于放开跨源）：%q", got)
	}

	w = do(r, "OPTIONS", "/x", map[string]string{
		"Origin":                        "http://localhost:3000",
		"Access-Control-Request-Method": "POST",
	})
	if w.Code != http.StatusNoContent {
		t.Fatalf("预检应返回 204，实际 %d", w.Code)
	}
	if w.Header().Get("Access-Control-Allow-Methods") == "" {
		t.Fatal("预检应声明允许的方法")
	}
}

func TestCORSAllowsAnyOriginOnlyWhenWildcardConfigured(t *testing.T) {
	r := gin.New()
	r.Use(CORS([]string{"*"}))
	r.GET("/x", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{}) })

	// 实现选择回显具体 Origin（配合 Vary: Origin），比直接回 "*" 更严格
	w := do(r, "GET", "/x", map[string]string{"Origin": "http://any.example"})
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "http://any.example" {
		t.Fatalf("配置 * 时应放行并回显该来源：%q", got)
	}
	if got := w.Header().Get("Vary"); !strings.Contains(got, "Origin") {
		t.Fatalf("应声明 Vary: Origin 以避免缓存串源：%q", got)
	}
}

func TestCurrentIdentityDefaultsToZeroValues(t *testing.T) {
	r := gin.New()
	var uid int64 = -1
	var role = "sentinel"
	r.GET("/x", func(c *gin.Context) {
		uid = currentUserID(c)
		role = currentRole(c)
		c.Status(http.StatusOK)
	})
	do(r, "GET", "/x", nil)

	if uid != 0 || role != "" {
		t.Fatalf("未注入身份时应返回零值：%d %q", uid, role)
	}
}

func TestItoa(t *testing.T) {
	cases := map[int]string{0: "0", 7: "7", -3: "-3", 12345: "12345", -987654: "-987654"}
	for in, want := range cases {
		if got := itoa(in); got != want {
			t.Fatalf("itoa(%d) = %q，期望 %q", in, got, want)
		}
	}
}

func TestFirstOr(t *testing.T) {
	if got := firstOr([]string{"a", "b"}, "z"); got != "a" {
		t.Fatalf("应取首个元素：%q", got)
	}
	if got := firstOr(nil, "z"); got != "z" {
		t.Fatalf("空列表应返回默认值：%q", got)
	}
}

func TestSignTokenRejectsNonPositiveTTL(t *testing.T) {
	for _, ttl := range []time.Duration{0, -time.Minute} {
		if _, err := user.SignToken(testSecret, user.Claims{UserID: 1, Role: "user"}, ttl); err == nil {
			t.Fatalf("ttl=%s 应报错（否则令牌生命周期会静默失控）", ttl)
		}
	}
	// 显式给 Exp 时不受 TTL 限制（用于构造过期令牌）
	if _, err := user.SignToken(testSecret, user.Claims{UserID: 1, Exp: time.Now().Add(-time.Hour).Unix()}, time.Hour); err != nil {
		t.Fatalf("显式 Exp 应可签发：%v", err)
	}
}
