// Package notify sends Telegram alerts. Delivery is asynchronous, message
// bodies are HTML-escaped and truncated, and repeated failures are rate-limited
// in the log so a broken chat cannot flood journald.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/Matsumiko/auvyn-apies-go/internal/config"
	"github.com/Matsumiko/auvyn-apies-go/internal/ipcenter"
)

const logCooldown = 5 * time.Minute

type Notifier struct {
	cfg  *config.Config
	log  *slog.Logger
	http *http.Client

	mu      sync.Mutex
	lastLog map[string]time.Time
}

func New(cfg *config.Config, log *slog.Logger) *Notifier {
	return &Notifier{
		cfg:     cfg,
		log:     log,
		http:    &http.Client{Timeout: 10 * time.Second},
		lastLog: map[string]time.Time{},
	}
}

func (n *Notifier) NotifyTrx(category, refID, product, dest string, qty *int64, statusCode int,
	raw string, balance *ipcenter.Balance, meta map[string]any) {
	go n.notifyTrx(category, refID, product, dest, qty, statusCode, raw, balance, meta)
}

func (n *Notifier) notifyTrx(category, refID, product, dest string, qty *int64, statusCode int,
	raw string, balance *ipcenter.Balance, meta map[string]any) {
	tg := n.cfg.Telegram
	var token, chatID, prefix string
	var threadID int64
	switch category {
	case "success":
		token, chatID, threadID, prefix = tg.SuccessToken, tg.SuccessChatID, tg.SuccessThreadID, "✅ TRX SUCCESS"
	case "pending":
		token, chatID, threadID, prefix = tg.PendingToken, tg.PendingChatID, tg.PendingThreadID, "🕒 TRX PENDING"
	case "failed":
		token, chatID, threadID, prefix = tg.FailedToken, tg.FailedChatID, tg.FailedThreadID, "❌ TRX FAILED"
	default:
		token, chatID, threadID, prefix = tg.SystemToken, tg.SystemChatID, tg.SystemThreadID, "ℹ️ TRX UNKNOWN"
	}
	if token == "" || chatID == "" {
		return
	}
	qtyText := "-"
	if qty != nil {
		qtyText = strconv.FormatInt(*qty, 10)
	}
	lines := []string{
		html.EscapeString(prefix),
		"",
		"<b>RefID</b>: " + html.EscapeString(orDash(refID)),
		"<b>Produk</b>: " + html.EscapeString(orDash(product)),
		"<b>Tujuan</b>: " + html.EscapeString(orDash(dest)),
		"<b>Qty</b>: " + html.EscapeString(qtyText),
		"<b>Status HTTP</b>: " + strconv.Itoa(statusCode),
		"<b>Source</b>: auvyn-apies",
		"",
		"<b>Raw</b>:",
		"<pre>" + html.EscapeString(truncate(ipcenter.RedactRaw(raw), 2500)) + "</pre>",
	}
	if balance != nil {
		lines = append(lines, "", fmt.Sprintf("<b>Saldo Tersisa</b>: Rp %s",
			strconv.FormatInt(balance.Remaining, 10)))
	}
	if len(meta) > 0 {
		lines = append(lines, "", "<b>Meta</b>:",
			"<pre>"+html.EscapeString(truncate(jsonBlock(meta), 800))+"</pre>")
	}
	n.send("trx:"+category, token, chatID, threadID, join(lines))
}

func (n *Notifier) NotifyLowBalance(balance *ipcenter.Balance) {
	go n.notifyLowBalance(balance)
}

func (n *Notifier) notifyLowBalance(balance *ipcenter.Balance) {
	if balance == nil {
		return
	}
	tg := n.cfg.Telegram
	if tg.SystemToken == "" || tg.SystemChatID == "" {
		return
	}
	threadID := tg.BalanceThreadID
	if threadID == 0 {
		threadID = tg.SystemThreadID
	}
	text := "⚠️ <b>Saldo Rendah</b>\n\n<b>Saldo Tersisa</b>: Rp " +
		strconv.FormatInt(balance.Remaining, 10)
	n.send("low-balance", tg.SystemToken, tg.SystemChatID, threadID, text)
}

func (n *Notifier) NotifyReport(refID, method, remoteIP string, message string) {
	go n.notifyReport(refID, method, remoteIP, message)
}

func (n *Notifier) notifyReport(refID, method, remoteIP string, message string) {
	tg := n.cfg.Telegram
	if tg.SystemToken == "" || tg.SystemChatID == "" {
		return
	}
	text := "📨 <b>IP Center Report</b>\n\n" +
		"<b>RefID</b>: " + html.EscapeString(orDash(refID)) + "\n" +
		"<b>Method</b>: " + html.EscapeString(method) + "\n" +
		"<b>IP</b>: " + html.EscapeString(orDash(remoteIP)) + "\n" +
		"<b>Pesan</b>:\n<pre>" +
		html.EscapeString(truncate(ipcenter.RedactRaw(message), 2500)) + "</pre>"
	n.send("report", tg.SystemToken, tg.SystemChatID, tg.SystemThreadID, text)
}

func (n *Notifier) NotifyDeadLetter(target, url, reason string) {
	go n.notifyDeadLetter(target, url, reason)
}

func (n *Notifier) notifyDeadLetter(target, url, reason string) {
	tg := n.cfg.Telegram
	if tg.SystemToken == "" || tg.SystemChatID == "" {
		return
	}
	text := "🚨 <b>Webhook Dead Letter</b>\n\n" +
		"<b>Target</b>: " + html.EscapeString(target) + "\n" +
		"<b>URL</b>: " + html.EscapeString(url) + "\n" +
		"<b>Error</b>: <pre>" + html.EscapeString(truncate(reason, 1000)) + "</pre>"
	n.send("dead-letter", tg.SystemToken, tg.SystemChatID, tg.SystemThreadID, text)
}

// SelfCheck verifies configured bot/chat pairs at startup. Misconfigured chats
// otherwise fail silently on every alert.
func (n *Notifier) SelfCheck(ctx context.Context) {
	tg := n.cfg.Telegram
	targets := []struct {
		category, token, chat string
	}{
		{"SUCCESS", tg.SuccessToken, tg.SuccessChatID},
		{"PENDING", tg.PendingToken, tg.PendingChatID},
		{"FAILED", tg.FailedToken, tg.FailedChatID},
		{"SYSTEM", tg.SystemToken, tg.SystemChatID},
	}
	for _, t := range targets {
		if t.token == "" || t.chat == "" {
			continue
		}
		n.checkChat(ctx, t.category, t.token, t.chat)
	}
}

func (n *Notifier) checkChat(ctx context.Context, category, token, chatID string) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	endpoint := "https://api.telegram.org/bot" + token + "/getChat?chat_id=" + url.QueryEscape(chatID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		n.failure("selfcheck:"+category, "telegram selfcheck request build failed", "category", category, "error", safeErr(err))
		return
	}
	resp, err := n.http.Do(req)
	if err != nil {
		n.failure("selfcheck:"+category, "telegram selfcheck failed", "category", category, "error", safeErr(err))
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	var payload telegramResponse
	_ = json.Unmarshal(body, &payload)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || !payload.OK {
		n.failure("selfcheck:"+category, "telegram chat is not reachable",
			"category", category, "status", resp.StatusCode, "description", payload.Description)
		return
	}
	n.log.Info("telegram chat ok", "category", category)
}

// send performs the HTTP call. Text builders already bound each variable
// component, keeping the final message below Telegram's 4096-character limit
// without cutting HTML tags.
func (n *Notifier) send(key, token, chatID string, threadID int64, text string) {
	if token == "" || chatID == "" {
		return
	}
	payload := map[string]any{
		"chat_id":                  chatID,
		"text":                     text,
		"parse_mode":               "HTML",
		"disable_web_page_preview": true,
	}
	if threadID > 0 {
		payload["message_thread_id"] = threadID
	}
	body, _ := json.Marshal(payload)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.telegram.org/bot"+token+"/sendMessage", bytes.NewReader(body))
	if err != nil {
		n.failure(key, "telegram request build failed", "error", safeErr(err))
		return
	}
	req.Header.Set("content-type", "application/json")
	resp, err := n.http.Do(req)
	if err != nil {
		n.failure(key, "telegram send failed", "error", safeErr(err))
		return
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	var result telegramResponse
	_ = json.Unmarshal(respBody, &result)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || !result.OK {
		n.failure(key, "telegram send failed", "status", resp.StatusCode, "description", result.Description)
	}
}

type telegramResponse struct {
	OK          bool   `json:"ok"`
	Description string `json:"description"`
	ErrorCode   int    `json:"error_code"`
}

// failure logs at most once per key per cooldown, so a broken chat does not
// create thousands of identical lines.
func (n *Notifier) failure(key, msg string, attrs ...any) {
	n.mu.Lock()
	now := time.Now()
	if last, ok := n.lastLog[key]; ok && now.Sub(last) < logCooldown {
		n.mu.Unlock()
		return
	}
	n.lastLog[key] = now
	n.mu.Unlock()
	n.log.Warn(msg, append([]any{"key", key}, attrs...)...)
}

// safeErr strips *url.Error.URL because bot tokens live in the URL path.
func safeErr(err error) string {
	if err == nil {
		return ""
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err.Error()
	}
	return err.Error()
}

func orDash(v string) string {
	if v == "" {
		return "-"
	}
	return v
}

func join(lines []string) string {
	var b bytes.Buffer
	for i, l := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(l)
	}
	return b.String()
}

func jsonBlock(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return ""
	}
	return string(b)
}

func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}
