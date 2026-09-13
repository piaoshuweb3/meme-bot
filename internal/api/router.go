// Package api 提供 HTTP 接口层（Gin）：
// 用户端（JWT）、管理端（角色）、外部 API（API Key + 配额）、健康检查与指标。
package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"meme-bot/internal/affiliate"
	"meme-bot/internal/model"
	"meme-bot/internal/subscription"
	"meme-bot/internal/user"
)

// Deps 是 API 层的依赖（只依赖契约接口，便于测试与替换）。
type Deps struct {
	Users      *user.Service
	Subs       *subscription.Service
	Affiliates *affiliate.Service

	// 运行时数据源
	Signals     func() []*model.Signal
	Positions   model.PositionStore
	Addresses   model.ProfileStore
	Risk        model.RiskPort
	AlertHealth func() map[string]string
	HealthExtra func() map[string]any

	MetricsHandler http.Handler
	MetricsPath    string

	JWTSecret string
	Mode      string
	GinMode   string
	Chains    []string
}

// SetupRouter 装配路由。
//
// gin 模式由配置驱动（configs/config.yaml 的 server.gin_mode）：
// release 用于生产（关闭 debug 日志），debug 便于本地排查。
func SetupRouter(d Deps) *gin.Engine {
	if d.GinMode != "" {
		gin.SetMode(d.GinMode)
	}

	r := gin.New()
	r.Use(gin.Recovery())
	if d.Mode != "live" {
		r.Use(gin.Logger())
	}

	// 健康检查与指标（不需要鉴权）
	r.GET("/healthz", func(c *gin.Context) {
		body := gin.H{
			"status":    "ok",
			"mode":      d.Mode,
			"chains":    d.Chains,
			"timestamp": time.Now().UTC(),
		}
		if d.HealthExtra != nil {
			for k, v := range d.HealthExtra() {
				body[k] = v
			}
		}
		if d.AlertHealth != nil {
			body["alerts"] = d.AlertHealth()
		}
		c.JSON(http.StatusOK, body)
	})
	if d.MetricsHandler != nil {
		path := d.MetricsPath
		if path == "" {
			path = "/metrics"
		}
		r.GET(path, gin.WrapH(d.MetricsHandler))
	}

	// 公开
	r.POST("/api/v1/auth/register", registerHandler(d))
	r.POST("/api/v1/auth/login", loginHandler(d))
	r.GET("/api/v1/plans", plansHandler(d))

	// 用户端（JWT）
	auth := r.Group("/api/v1")
	auth.Use(JWTAuth(d.JWTSecret))
	{
		auth.GET("/me", meHandler(d))
		auth.POST("/me/api-key/rotate", rotateAPIKeyHandler(d))
		auth.GET("/signals", listSignalsHandler(d))
		auth.GET("/positions", listPositionsHandler(d))
		auth.GET("/subscription", subscriptionHandler(d))
		auth.POST("/subscription/checkout", checkoutHandler(d))
		auth.POST("/affiliate/bind", bindReferralHandler(d))
		auth.GET("/affiliate/stats", affiliateStatsHandler(d))
		auth.POST("/system/pause", pauseHandler(d))
		auth.POST("/system/resume", resumeHandler(d))
	}

	// 管理端
	admin := auth.Group("/admin")
	admin.Use(RequireRole(string(user.RoleSuperAdmin), string(user.RoleAdmin)))
	{
		admin.GET("/users", listUsersHandler(d))
		admin.POST("/users/:id/status", setUserStatusHandler(d))
		admin.POST("/users/:id/role", setUserRoleHandler(d))
		admin.GET("/addresses/top", topAddressesHandler(d))
	}

	// 外部 API（API Key + 配额）
	if d.Users != nil && d.Subs != nil {
		ext := r.Group("/api/external/v1")
		ext.Use(APIKeyAuth(d.Users, d.Subs))
		{
			ext.GET("/signals/latest", listSignalsHandler(d))
			ext.GET("/address/score/:address", externalScoreHandler(d))
		}
	}

	r.NoRoute(func(c *gin.Context) {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
	})
	return r
}

// ---------------------------------------------------------------------------
// handlers
// ---------------------------------------------------------------------------

func registerHandler(d Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var in struct {
			Email    string `json:"email"`
			Password string `json:"password"`
			Referral string `json:"referral_code"`
		}
		if err := c.ShouldBindJSON(&in); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求体格式错误"})
			return
		}
		if d.Users == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "用户服务不可用"})
			return
		}
		u, err := d.Users.Create(c.Request.Context(), in.Email, in.Password, in.Referral)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusCreated, gin.H{
			"user":    u,
			"api_key": u.APIKey,
			"warning": "API Key 仅在此处返回一次，请立即保存；请勿提交到代码仓库",
		})
	}
}

func loginHandler(d Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var in struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		if err := c.ShouldBindJSON(&in); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求体格式错误"})
			return
		}
		if d.Users == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "用户服务不可用"})
			return
		}
		token, u, err := d.Users.Login(c.Request.Context(), in.Email, in.Password)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"token": token, "user": u})
	}
}

func meHandler(d Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		if d.Users == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "用户服务不可用"})
			return
		}
		u, err := d.Users.GetByID(c.Request.Context(), currentUserID(c))
		if err != nil || u == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "用户不存在"})
			return
		}
		u.APIKey = "" // 不在常规接口回显 API Key
		c.JSON(http.StatusOK, gin.H{"user": u})
	}
}

func rotateAPIKeyHandler(d Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		if d.Users == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "用户服务不可用"})
			return
		}
		key, err := d.Users.RotateAPIKey(c.Request.Context(), currentUserID(c))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"api_key": key, "warning": "旧 Key 已失效"})
	}
}

func listSignalsHandler(d Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		if d.Signals == nil {
			c.JSON(http.StatusOK, gin.H{"signals": []any{}})
			return
		}
		signals := d.Signals()
		limit := 50
		if v := c.Query("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
				limit = n
			}
		}
		if len(signals) > limit {
			signals = signals[:limit]
		}
		c.JSON(http.StatusOK, gin.H{"count": len(signals), "signals": signals})
	}
}

func listPositionsHandler(d Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		if d.Positions == nil {
			c.JSON(http.StatusOK, gin.H{"positions": []any{}})
			return
		}
		chain := c.Query("chain")
		list, err := d.Positions.ListOpen(chain)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"count": len(list), "positions": list})
	}
}

func topAddressesHandler(d Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		if d.Addresses == nil {
			c.JSON(http.StatusOK, gin.H{"addresses": []any{}})
			return
		}
		limit := 50
		if v := c.Query("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
				limit = n
			}
		}
		list, err := d.Addresses.ListTop(c.Request.Context(), c.Query("chain"), limit)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"count": len(list), "addresses": list})
	}
}

func externalScoreHandler(d Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		addr := c.Param("address")
		chain := c.DefaultQuery("chain", firstOr(d.Chains, "base"))
		if d.Addresses == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "画像服务不可用"})
			return
		}
		p, err := d.Addresses.Get(c.Request.Context(), chain, addr)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if p == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "地址无画像数据", "address": addr, "chain": chain})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"address": p.Address, "chain": p.Chain, "score": p.RecentScore,
			"win_rate": p.WinRate, "profit_factor": p.ProfitFactor, "max_drawdown": p.MaxDrawdown,
			"total_trades": p.TotalTrades, "tags": p.Tags, "blacklisted": p.IsBlacklisted,
			"updated_at": p.UpdatedAt,
		})
	}
}

func plansHandler(d Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		if d.Subs == nil {
			c.JSON(http.StatusOK, gin.H{"plans": []any{}})
			return
		}
		plans, err := d.Subs.Plans(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"plans": plans})
	}
}

func subscriptionHandler(d Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		if d.Subs == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "订阅服务不可用"})
			return
		}
		sub, err := d.Subs.Status(c.Request.Context(), currentUserID(c))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"subscription": sub})
	}
}

func checkoutHandler(d Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		if d.Subs == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "订阅服务不可用"})
			return
		}
		var in struct {
			PlanID   int    `json:"plan_id"`
			Currency string `json:"currency"`
		}
		if err := c.ShouldBindJSON(&in); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求体格式错误"})
			return
		}
		order, err := d.Subs.CreatePayment(c.Request.Context(), currentUserID(c), in.PlanID, in.Currency)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusCreated, gin.H{
			"order": order,
			"next":  "请通过支付渠道完成付款；支付成功回调 /api/v1/payments/webhook 将自动激活订阅",
		})
	}
}

func bindReferralHandler(d Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		if d.Affiliates == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "返佣服务不可用"})
			return
		}
		var in struct {
			Code string `json:"code"`
		}
		if err := c.ShouldBindJSON(&in); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求体格式错误"})
			return
		}
		if err := d.Affiliates.BindReferral(c.Request.Context(), currentUserID(c), in.Code); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "bound"})
	}
}

func affiliateStatsHandler(d Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		if d.Affiliates == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "返佣服务不可用"})
			return
		}
		stats, err := d.Affiliates.Stats(c.Request.Context(), currentUserID(c))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"stats": stats, "rate": d.Affiliates.Rate()})
	}
}

func pauseHandler(d Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		if d.Risk == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "风控不可用"})
			return
		}
		var in struct {
			Reason      string `json:"reason"`
			DurationMin int    `json:"duration_minutes"`
		}
		_ = c.ShouldBindJSON(&in)
		if strings.TrimSpace(in.Reason) == "" {
			in.Reason = "人工熔断"
		}
		if in.DurationMin <= 0 {
			in.DurationMin = 60
		}
		if err := d.Risk.Pause(in.Reason, time.Now().Add(time.Duration(in.DurationMin)*time.Minute)); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "paused", "reason": in.Reason, "duration_minutes": in.DurationMin})
	}
}

func resumeHandler(d Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		type resumer interface{ Resume() error }
		r, ok := d.Risk.(resumer)
		if !ok || d.Risk == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "风控不可用或不支持恢复"})
			return
		}
		if err := r.Resume(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "resumed"})
	}
}

func listUsersHandler(d Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		if d.Users == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "用户服务不可用"})
			return
		}
		limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
		offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
		users, err := d.Users.List(c.Request.Context(), limit, offset)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"count": len(users), "users": users})
	}
}

func setUserStatusHandler(d Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		if d.Users == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "用户服务不可用"})
			return
		}
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "用户 ID 非法"})
			return
		}
		var in struct {
			Status string `json:"status"`
		}
		if err := c.ShouldBindJSON(&in); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求体格式错误"})
			return
		}
		if err := d.Users.SetStatus(c.Request.Context(), id, in.Status); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "updated"})
	}
}

func setUserRoleHandler(d Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		if d.Users == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "用户服务不可用"})
			return
		}
		if currentRole(c) != string(user.RoleSuperAdmin) {
			c.JSON(http.StatusForbidden, gin.H{"error": "仅超级管理员可调整角色"})
			return
		}
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "用户 ID 非法"})
			return
		}
		var in struct {
			Role string `json:"role"`
		}
		if err := c.ShouldBindJSON(&in); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求体格式错误"})
			return
		}
		if err := d.Users.SetRole(c.Request.Context(), id, user.Role(in.Role)); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "updated"})
	}
}

func firstOr(list []string, def string) string {
	if len(list) > 0 {
		return list[0]
	}
	return def
}
