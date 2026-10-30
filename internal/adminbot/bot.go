// Package adminbot runs a private Telegram bot for operational admin tasks.
//
// It uses long polling and talks to the SQLite store directly, so it does not
// need the admin HTTP secret. Only private chats listed in TG_ADMIN_CHAT_IDS
// are accepted; every mutating command requires an explicit "confirm" suffix.
package adminbot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Matsumiko/auvyn-apies-go/internal/config"
	"github.com/Matsumiko/auvyn-apies-go/internal/ipcenter"
	"github.com/Matsumiko/auvyn-apies-go/internal/store"
)

const (
	apiResultLimit = 1 << 20
	messageLimit   = 3500
)

type Bot struct {
	cfg  *config.Config
	st   *store.Store
	ip   *ipcenter.Client
	log  *slog.Logger
	http *http.Client

	token     string
	apiBase   string
	allowed   map[int64]struct{}
	offset    int64
	offsetAt  string
	startedAt time.Time

	stateMu sync.Mutex
	pending map[int64]pendingState
}

type update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *message       `json:"message"`
	CallbackQuery *callbackQuery `json:"callback_query"`
}

type message struct {
	MessageID int64  `json:"message_id"`
	From      *user  `json:"from"`
	Chat      chat   `json:"chat"`
	Text      string `json:"text"`
}

type user struct {
	ID    int64 `json:"id"`
	IsBot bool  `json:"is_bot"`
}

type chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

type callbackQuery struct {
	ID      string   `json:"id"`
	From    *user    `json:"from"`
	Message *message `json:"message"`
	Data    string   `json:"data"`
}

type telegramResponse struct {
	OK          bool            `json:"ok"`
	Description string          `json:"description"`
	Result      json.RawMessage `json:"result"`
}

func New(cfg *config.Config, st *store.Store, ip *ipcenter.Client, log *slog.Logger) *Bot {
	base := strings.TrimRight(strings.TrimSpace(cfg.Telegram.AdminAPIBase), "/")
	if base == "" {
		base = "https://api.telegram.org"
	}
	allowed := make(map[int64]struct{}, len(cfg.Telegram.AdminChatIDs))
	for _, id := range cfg.Telegram.AdminChatIDs {
		allowed[id] = struct{}{}
	}
	offsetAt := ""
	if cfg.DataDir != "" {
		offsetAt = filepath.Join(cfg.DataDir, "telegram-admin.offset")
	}
	return &Bot{
		cfg: cfg, st: st, ip: ip, log: log,
		http:      &http.Client{Timeout: 45 * time.Second},
		token:     strings.TrimSpace(cfg.Telegram.AdminToken),
		apiBase:   base,
		allowed:   allowed,
		offsetAt:  offsetAt,
		startedAt: time.Now(),
		pending:   map[int64]pendingState{},
	}
}

func (b *Bot) Enabled() bool { return b.token != "" }

// Run polls Telegram until ctx is cancelled. A getUpdates error is retried
// with exponential backoff; individual command failures never stop the loop.
func (b *Bot) Run(ctx context.Context) {
	if !b.Enabled() {
		return
	}
	b.loadOffset()
	b.log.Info("telegram admin bot started",
		"chats", len(b.allowed),
		"api", b.apiBase,
	)
	if err := b.deleteWebhook(ctx); err != nil {
		b.log.Warn("telegram admin bot deleteWebhook failed", "error", safeTelegramError(err))
	}
	b.configureMenu(ctx)

	backoff := time.Second
	for {
		updates, err := b.getUpdates(ctx, 30)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			b.log.Warn("telegram admin bot getUpdates failed", "error", safeTelegramError(err))
			timer := time.NewTimer(backoff)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			if backoff < 30*time.Second {
				backoff *= 2
				if backoff > 30*time.Second {
					backoff = 30 * time.Second
				}
			}
			continue
		}
		backoff = time.Second
		for _, u := range updates {
			b.handle(ctx, u)
			if u.UpdateID >= b.offset {
				b.offset = u.UpdateID + 1
				b.saveOffset()
			}
		}
	}
}

func (b *Bot) handle(ctx context.Context, u update) {
	if u.CallbackQuery != nil {
		b.handleCallback(ctx, u.CallbackQuery)
		return
	}
	m := u.Message
	if m == nil || m.From == nil || m.From.IsBot || strings.TrimSpace(m.Text) == "" {
		return
	}
	if m.Chat.Type != "private" {
		b.log.Warn("telegram admin rejected non-private chat",
			"chat_id", m.Chat.ID, "chat_type", m.Chat.Type, "user_id", m.From.ID)
		return
	}
	if _, ok := b.allowed[m.Chat.ID]; !ok {
		b.log.Warn("telegram admin rejected unknown chat",
			"chat_id", m.Chat.ID, "user_id", m.From.ID)
		return
	}
	text := strings.TrimSpace(m.Text)
	if strings.HasPrefix(text, "/") {
		cmd, rest, ok := parseCommand(text)
		if !ok {
			return
		}
		b.log.Info("telegram admin command",
			"chat_id", m.Chat.ID, "user_id", m.From.ID, "command", cmd)
		if cmd == "batal" || cmd == "cancel" {
			p, _ := b.getPending(m.Chat.ID)
			b.clearPending(m.Chat.ID)
			b.sendBackMenu(ctx, m.Chat.ID, p.back)
			return
		}
		b.clearPending(m.Chat.ID)
		b.handleCommand(ctx, m.Chat.ID, m.MessageID, cmd, rest)
		return
	}
	if p, ok := b.getPending(m.Chat.ID); ok {
		b.handlePending(ctx, m.Chat.ID, p, text)
	}
}

func (b *Bot) answerCallback(ctx context.Context, id, text string) {
	payload := map[string]any{"callback_query_id": id}
	if text != "" {
		payload["text"] = text
	}
	if _, err := b.call(ctx, "answerCallbackQuery", payload); err != nil {
		b.log.Warn("telegram admin answerCallbackQuery failed",
			"error", safeTelegramError(err))
	}
}

func parseCommand(text string) (cmd, rest string, ok bool) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "/") {
		return "", "", false
	}
	parts := strings.SplitN(text, " ", 2)
	cmd = strings.ToLower(strings.TrimSpace(parts[0]))
	if i := strings.IndexByte(cmd, '@'); i >= 0 {
		cmd = cmd[:i]
	}
	cmd = strings.TrimPrefix(cmd, "/")
	if cmd == "" {
		return "", "", false
	}
	if len(parts) == 2 {
		rest = strings.TrimSpace(parts[1])
	}
	return cmd, rest, true
}

func (b *Bot) sendLong(ctx context.Context, chatID, replyTo int64, text string) {
	b.sendLongKeyboard(ctx, chatID, replyTo, text, nil)
}

func (b *Bot) sendLongKeyboard(ctx context.Context, chatID, replyTo int64, text string, keyboard any) {
	chunks := splitMessage(text)
	for i, chunk := range chunks {
		payload := map[string]any{
			"chat_id":                  chatID,
			"text":                     chunk,
			"disable_web_page_preview": true,
		}
		if i == 0 && replyTo != 0 {
			payload["reply_to_message_id"] = replyTo
		}
		if keyboard != nil && i == len(chunks)-1 {
			payload["reply_markup"] = keyboard
		}
		if _, err := b.call(ctx, "sendMessage", payload); err != nil {
			b.log.Warn("telegram admin sendMessage failed",
				"chat_id", chatID, "error", safeTelegramError(err))
			return
		}
	}
}

func splitMessage(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	runes := []rune(text)
	var out []string
	for len(runes) > messageLimit {
		cut := messageLimit
		for i := messageLimit; i > messageLimit/2; i-- {
			if runes[i] == '\n' {
				cut = i
				break
			}
		}
		out = append(out, strings.TrimRight(string(runes[:cut]), "\n"))
		runes = []rune(strings.TrimLeft(string(runes[cut:]), "\n"))
	}
	if len(runes) > 0 {
		out = append(out, string(runes))
	}
	return out
}

func (b *Bot) getUpdates(ctx context.Context, timeout int) ([]update, error) {
	pollCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout+10)*time.Second)
	defer cancel()
	raw, err := b.call(pollCtx, "getUpdates", map[string]any{
		"offset":          b.offset,
		"timeout":         timeout,
		"allowed_updates": []string{"message", "callback_query"},
	})
	if err != nil {
		return nil, err
	}
	var updates []update
	if err := json.Unmarshal(raw, &updates); err != nil {
		return nil, fmt.Errorf("decode getUpdates: %w", err)
	}
	return updates, nil
}

func (b *Bot) deleteWebhook(ctx context.Context) error {
	_, err := b.call(ctx, "deleteWebhook", map[string]any{"drop_pending_updates": false})
	return err
}

func (b *Bot) call(ctx context.Context, method string, payload any) (json.RawMessage, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		b.apiBase+"/bot"+b.token+"/"+method, bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("invalid telegram request")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.http.Do(req)
	if err != nil {
		return nil, errors.New(safeTelegramError(err))
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, apiResultLimit))
	if err != nil {
		return nil, errors.New("read telegram response")
	}
	var tr telegramResponse
	if err := json.Unmarshal(data, &tr); err != nil {
		return nil, fmt.Errorf("telegram HTTP %d", resp.StatusCode)
	}
	if !tr.OK {
		msg := strings.TrimSpace(tr.Description)
		if msg == "" {
			msg = fmt.Sprintf("telegram HTTP %d", resp.StatusCode)
		}
		return nil, errors.New(msg)
	}
	return tr.Result, nil
}

func (b *Bot) loadOffset() {
	if b.offsetAt == "" {
		return
	}
	data, err := os.ReadFile(b.offsetAt)
	if err != nil {
		return
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err == nil && n > 0 {
		b.offset = n
	}
}

func (b *Bot) saveOffset() {
	if b.offsetAt == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(b.offsetAt), 0o750); err != nil {
		b.log.Warn("telegram admin offset mkdir failed", "error", err)
		return
	}
	tmp := b.offsetAt + ".tmp"
	if err := os.WriteFile(tmp, []byte(strconv.FormatInt(b.offset, 10)), 0o600); err != nil {
		b.log.Warn("telegram admin offset write failed", "error", err)
		return
	}
	if err := os.Rename(tmp, b.offsetAt); err != nil {
		b.log.Warn("telegram admin offset rename failed", "error", err)
	}
}

// safeTelegramError removes the request URL: the bot token lives in the path.
func safeTelegramError(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		if ue.Err != nil {
			return ue.Op + ": " + ue.Err.Error()
		}
		return ue.Op + ": telegram request failed"
	}
	return err.Error()
}
