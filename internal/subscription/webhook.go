package subscription

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"meme-bot/internal/metrics"
)

// maxWebhookBody 回调请求体上限，防止超大 payload 打爆内存。
const maxWebhookBody = 1 << 20 // 1 MiB

// 回调验签用到的请求头。
const (
	headerSignature = "X-Webhook-Signature"
	headerTimestamp = "X-Webhook-Timestamp"
)

// WebhookHandler 支付回调处理器。
//
// 安全设计：
//   - HMAC-SHA256 验签，使用 hmac.Equal 做常量时间比较（防时序侧信道）；
//   - 可选时间戳 + 容忍窗口，抵御重放攻击；
//   - 未配置验签密钥时直接拒绝所有请求（503），绝不"无签名放行"；
//   - 业务幂等由 Service.HandlePaymentSucceeded 保证（order_id 状态迁移唯一）。
type WebhookHandler struct {
	svc       *Service
	secret    string
	tolerance time.Duration
	metrics   *metrics.Registry
}

// NewWebhookHandler 构建回调处理器；tolerance <= 0 表示不校验时间戳。
func NewWebhookHandler(svc *Service, secret string, tolerance time.Duration, reg *metrics.Registry) *WebhookHandler {
	return &WebhookHandler{
		svc:       svc,
		secret:    strings.TrimSpace(secret),
		tolerance: tolerance,
		metrics:   reg,
	}
}

// Configured 是否已配置验签密钥。
func (h *WebhookHandler) Configured() bool {
	return h != nil && h.secret != ""
}

// paymentEvent 回调载荷（兼容嵌套与扁平两种写法）。
type paymentEvent struct {
	Type    string `json:"type"`
	OrderID string `json:"order_id"`
	Data    *struct {
		OrderID string `json:"order_id"`
	} `json:"data"`
}

// resolveOrderID 提取订单号。
func (e *paymentEvent) resolveOrderID() string {
	if e == nil {
		return ""
	}
	if strings.TrimSpace(e.OrderID) != "" {
		return strings.TrimSpace(e.OrderID)
	}
	if e.Data != nil {
		return strings.TrimSpace(e.Data.OrderID)
	}
	return ""
}

// succeeded 判断是否为"支付成功"事件；空 type 视为成功（简化渠道）。
func (e *paymentEvent) succeeded() bool {
	if e == nil || strings.TrimSpace(e.Type) == "" {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(e.Type)) {
	case "payment.succeeded", "payment_succeeded", "payment.success",
		"checkout.session.completed", "charge.succeeded":
		return true
	default:
		return false
	}
}

// SignPayload 计算签名：带时间戳时为 HMAC(secret, timestamp + "." + body)，否则 HMAC(secret, body)。
//
// 与 Stripe 的 "t=...,v1=..." 风格一致，接入真实渠道时可直接复用。
func SignPayload(secret, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	if strings.TrimSpace(timestamp) != "" {
		mac.Write([]byte(timestamp))
		mac.Write([]byte("."))
	}
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifySignature 校验回调签名（常量时间比较）。
func VerifySignature(secret, timestamp string, body []byte, signature string) bool {
	secret = strings.TrimSpace(secret)
	signature = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(signature), "sha256="))
	if secret == "" || signature == "" {
		return false
	}
	expected := SignPayload(secret, timestamp, body)
	return hmac.Equal([]byte(expected), []byte(signature))
}

// verifyTimestamp 校验时间戳是否落在容忍窗口内（tolerance <= 0 时跳过）。
func (h *WebhookHandler) verifyTimestamp(raw string) error {
	if h.tolerance <= 0 {
		return nil
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return errors.New("缺少 " + headerTimestamp + " 请求头（防重放校验必需）")
	}
	ts, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return errors.New("时间戳格式非法（应为 Unix 秒）")
	}
	drift := time.Since(time.Unix(ts, 0))
	if drift < 0 {
		drift = -drift
	}
	if drift > h.tolerance {
		return errors.New("时间戳超出容忍窗口（疑似重放）")
	}
	return nil
}

// Handle 处理回调：POST /api/v1/payments/webhook
//
// 流程：验签 → 时间戳窗口 → 解析事件 → 幂等激活订阅 → 触发返佣。
func (h *WebhookHandler) Handle(c *gin.Context) {
	if h == nil || !h.Configured() {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": "支付回调未启用：请配置 MEMEBOT_PAYMENT_WEBHOOK_SECRET",
		})
		return
	}
	if h.svc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "订阅服务不可用（数据库未就绪）"})
		return
	}

	body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxWebhookBody))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "读取请求体失败"})
		return
	}

	timestamp := c.GetHeader(headerTimestamp)
	signature := c.GetHeader(headerSignature)
	if signature == "" {
		signature = c.GetHeader("X-Signature")
	}

	if !VerifySignature(h.secret, timestamp, body, signature) {
		h.reject(c, "验签失败：签名缺失或不匹配")
		return
	}
	if err := h.verifyTimestamp(timestamp); err != nil {
		h.reject(c, err.Error())
		return
	}

	var event paymentEvent
	if err := jsonUnmarshal(body, &event); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "回调载荷不是合法 JSON"})
		return
	}
	if !event.succeeded() {
		c.JSON(http.StatusOK, gin.H{"status": "ignored", "type": event.Type})
		return
	}

	orderID := event.resolveOrderID()
	if orderID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少 order_id"})
		return
	}

	result, err := h.svc.HandlePaymentSucceeded(c.Request.Context(), orderID)
	if err != nil {
		status := http.StatusInternalServerError
		if strings.Contains(err.Error(), "订单不存在") {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"error": err.Error(), "order_id": orderID})
		return
	}

	if h.metrics != nil {
		h.metrics.Inc("memebot_payments_total", 1)
		if result.Deduped {
			h.metrics.Inc("memebot_payment_webhook_deduped_total", 1)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"status":       "ok",
		"order_id":     result.OrderID,
		"user_id":      result.UserID,
		"plan_id":      result.PlanID,
		"deduped":      result.Deduped,
		"period_end":   result.PeriodEnd,
		"commissioned": result.Commissioned,
	})
}

// reject 拒绝非法回调（计入指标 + 返回 401）。
func (h *WebhookHandler) reject(c *gin.Context, reason string) {
	if h.metrics != nil {
		h.metrics.Inc("memebot_payment_webhook_rejected_total", 1)
	}
	c.JSON(http.StatusUnauthorized, gin.H{"error": reason})
}
