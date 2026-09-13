package alert

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"

	"meme-bot/internal/model"
)

// TelegramChannel 通过标准库直连 Telegram Bot API（不引入第三方 SDK）。
//
// 支持：
//   - sendMessage + inline keyboard（一键暂停 / 平仓 / 忽略）
//   - 长轮询 getUpdates 处理 callback_query 与命令
//
// 安全：token 只从配置（环境变量）读取，日志中从不打印完整 token。
type TelegramChannel struct {
	bot    *botAPI
	chatID int64
	log    *zap.Logger
}

// botAPI 是 Telegram Bot API 的最小客户端。
type botAPI struct {
	token  string
	base   string
	client *http.Client
}

func newBotAPI(token string) *botAPI {
	return &botAPI{
		token:  strings.TrimSpace(token),
		base:   "https://api.telegram.org",
		client: &http.Client{Timeout: 35 * time.Second},
	}
}

func (b *botAPI) endpoint(method string) string {
	return fmt.Sprintf("%s/bot%s/%s", b.base, b.token, method)
}

func (b *botAPI) call(ctx context.Context, method string, payload any, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.endpoint(method), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := b.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	var envelope struct {
		OK          bool            `json:"ok"`
		Description string          `json:"description"`
		Result      json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("telegram: decode response: %w", err)
	}
	if !envelope.OK {
		return fmt.Errorf("telegram: api error: %s", envelope.Description)
	}
	if out != nil && len(envelope.Result) > 0 {
		if err := json.Unmarshal(envelope.Result, out); err != nil {
			return fmt.Errorf("telegram: decode result: %w", err)
		}
	}
	return nil
}

// NewTelegramChannel 构建 Telegram 通道。
func NewTelegramChannel(token string, chatID int64, log *zap.Logger) (*TelegramChannel, error) {
	if strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("telegram: empty bot token")
	}
	if chatID == 0 {
		return nil, fmt.Errorf("telegram: chat id is required")
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &TelegramChannel{bot: newBotAPI(token), chatID: chatID, log: log}, nil
}

// Name 实现 model.Channel。
func (t *TelegramChannel) Name() string { return "telegram" }

// Ping 实现健康检查（getMe）。
func (t *TelegramChannel) Ping(ctx context.Context) error {
	var out struct {
		Username string `json:"username"`
	}
	if err := t.bot.call(ctx, "getMe", map[string]any{}, &out); err != nil {
		return err
	}
	t.log.Debug("telegram bot ok", zap.String("username", out.Username))
	return nil
}

// Send 实现 model.Channel：HTML 解析 + 内联键盘。
func (t *TelegramChannel) Send(ctx context.Context, a *model.Alert) error {
	if a == nil {
		return fmt.Errorf("telegram: nil alert")
	}

	payload := map[string]any{
		"chat_id":                  t.chatID,
		"text":                     formatMessage(a),
		"parse_mode":               "HTML",
		"disable_web_page_preview": true,
	}
	if kb := inlineKeyboard(a.Buttons); kb != nil {
		payload["reply_markup"] = kb
	}
	return t.bot.call(ctx, "sendMessage", payload, nil)
}

// inlineKeyboard 把按钮转换为 Telegram 内联键盘。
func inlineKeyboard(buttons []model.Button) map[string]any {
	if len(buttons) == 0 {
		return nil
	}
	row := make([]map[string]string, 0, len(buttons))
	for _, b := range buttons {
		if strings.TrimSpace(b.Text) == "" || strings.TrimSpace(b.Data) == "" {
			continue
		}
		row = append(row, map[string]string{"text": b.Text, "callback_data": b.Data})
	}
	if len(row) == 0 {
		return nil
	}
	return map[string]any{"inline_keyboard": [][]map[string]string{row}}
}

// formatMessage 组装告警文本（含必要上下文：链、代币、原因、链接、动作）。
func formatMessage(a *model.Alert) string {
	icon := map[model.Level]string{
		model.LevelCritical: "🚨",
		model.LevelWarning:  "⚠️",
		model.LevelInfo:     "ℹ️",
	}[a.Level]

	var b strings.Builder
	fmt.Fprintf(&b, "%s <b>%s</b>  [%s]\n\n", icon, escapeHTML(a.Title), strings.ToUpper(string(a.Level)))
	if a.Message != "" {
		fmt.Fprintf(&b, "%s\n\n", escapeHTML(a.Message))
	}
	if a.Chain != "" {
		fmt.Fprintf(&b, "链：<code>%s</code>\n", escapeHTML(a.Chain))
	}
	if a.Token != "" {
		fmt.Fprintf(&b, "代币：<code>%s</code>\n", escapeHTML(a.Token))
	}
	if a.Address != "" {
		fmt.Fprintf(&b, "地址：<code>%s</code>\n", escapeHTML(a.Address))
	}
	if a.ActionURL != "" {
		fmt.Fprintf(&b, "\n<a href=\"%s\">查看链上详情</a>\n", escapeURL(a.ActionURL))
	}
	fmt.Fprintf(&b, "\n<i>%s</i>", a.Timestamp.Format("2006-01-02 15:04:05 MST"))
	return b.String()
}

// escapeHTML 转义 Telegram HTML 解析模式下的特殊字符。
func escapeHTML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

func escapeURL(raw string) string {
	if u, err := url.Parse(raw); err == nil {
		return u.String()
	}
	return ""
}

// ---------------------------------------------------------------------------
// 交互：命令与按钮回调
// ---------------------------------------------------------------------------

// ActionHandler 处理按钮回调与命令（返回给用户的提示文本）。
type ActionHandler func(ctx context.Context, userID int64, action, payload string) string

// StartPolling 启动长轮询，处理 callback_query 与常用命令。
//
// 阻塞直到 ctx 取消；应在独立 goroutine 中调用。
func (t *TelegramChannel) StartPolling(ctx context.Context, handler ActionHandler) {
	var offset int64
	client := &http.Client{Timeout: 40 * time.Second}

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		updates, err := t.getUpdates(ctx, client, offset)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			t.log.Warn("telegram getUpdates failed", zap.Error(err))
			time.Sleep(3 * time.Second)
			continue
		}
		for _, u := range updates {
			offset = u.UpdateID + 1
			t.dispatch(ctx, handler, u)
		}
	}
}

type tgUpdate struct {
	UpdateID int64 `json:"update_id"`
	Message  *struct {
		MessageID int64  `json:"message_id"`
		Text      string `json:"text"`
		From      *struct {
			ID int64 `json:"id"`
		} `json:"from"`
		Chat *struct {
			ID int64 `json:"id"`
		} `json:"chat"`
	} `json:"message"`
	CallbackQuery *struct {
		ID   string `json:"id"`
		Data string `json:"data"`
		From *struct {
			ID int64 `json:"id"`
		} `json:"from"`
		Message *struct {
			Chat *struct {
				ID int64 `json:"id"`
			} `json:"chat"`
			MessageID int64 `json:"message_id"`
		} `json:"message"`
	} `json:"callback_query"`
}

func (t *TelegramChannel) getUpdates(ctx context.Context, client *http.Client, offset int64) ([]tgUpdate, error) {
	q := url.Values{}
	q.Set("timeout", "25")
	q.Set("allowed_updates", `["message","callback_query"]`)
	if offset > 0 {
		q.Set("offset", strconv.FormatInt(offset, 10))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.bot.endpoint("getUpdates")+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var envelope struct {
		OK          bool       `json:"ok"`
		Description string     `json:"description"`
		Result      []tgUpdate `json:"result"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, err
	}
	if !envelope.OK {
		return nil, fmt.Errorf("telegram: %s", envelope.Description)
	}
	return envelope.Result, nil
}

func (t *TelegramChannel) dispatch(ctx context.Context, handler ActionHandler, u tgUpdate) {
	// 按钮回调
	if u.CallbackQuery != nil {
		var userID int64
		if u.CallbackQuery.From != nil {
			userID = u.CallbackQuery.From.ID
		}
		action, payload := splitAction(u.CallbackQuery.Data)
		text := "已处理"
		if handler != nil {
			text = handler(ctx, userID, action, payload)
		}
		_ = t.answerCallback(ctx, u.CallbackQuery.ID, text)
		return
	}

	// 文本命令
	if u.Message != nil && u.Message.Text != "" {
		var userID int64
		if u.Message.From != nil {
			userID = u.Message.From.ID
		}
		cmd, arg := splitAction(strings.TrimPrefix(u.Message.Text, "/"))
		text := t.defaultCommandReply(ctx, handler, userID, cmd, arg)
		_ = t.Send(ctx, &model.Alert{
			Level:   model.LevelInfo,
			Title:   "命令响应",
			Message: text,
		})
	}
}

func (t *TelegramChannel) defaultCommandReply(ctx context.Context, handler ActionHandler, userID int64, cmd, arg string) string {
	switch cmd {
	case "start":
		return "Smart Money Bot 已就绪。信号与告警将自动推送；可用命令：/status、/pause、/resume。"
	case "status":
		if handler != nil {
			return handler(ctx, userID, "status", arg)
		}
		return "系统运行正常"
	case "pause":
		if handler != nil {
			return handler(ctx, userID, "pause", arg)
		}
		return "交易已暂停"
	case "resume":
		if handler != nil {
			return handler(ctx, userID, "resume", arg)
		}
		return "交易已恢复"
	default:
		if handler != nil {
			return handler(ctx, userID, cmd, arg)
		}
		return "未知命令"
	}
}

func (t *TelegramChannel) answerCallback(ctx context.Context, callbackID, text string) error {
	if callbackID == "" {
		return nil
	}
	return t.bot.call(ctx, "answerCallbackQuery", map[string]any{
		"callback_query_id": callbackID,
		"text":              text,
		"show_alert":        false,
	}, nil)
}

// splitAction 解析 "action:payload" 形式的数据。
//
// 兼容两种来源：
//   - 内联按钮 callback_data（如 "close:0xabc"）
//   - 文本命令（如 "/status"、"pause 60"），前导斜杠会被剥离
func splitAction(data string) (action, payload string) {
	data = strings.TrimSpace(data)
	data = strings.TrimPrefix(data, "/")
	parts := strings.SplitN(data, ":", 2)
	if len(parts) == 1 {
		return parts[0], ""
	}
	return parts[0], parts[1]
}

var _ model.Channel = (*TelegramChannel)(nil)
