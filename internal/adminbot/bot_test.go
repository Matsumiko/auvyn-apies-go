package adminbot

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Matsumiko/auvyn-apies-go/internal/config"
	"github.com/Matsumiko/auvyn-apies-go/internal/store"
)

type telegramCall struct {
	method  string
	payload map[string]any
}

type fakeTelegram struct {
	mu    sync.Mutex
	calls []telegramCall
}

func (f *fakeTelegram) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var payload map[string]any
	_ = json.NewDecoder(r.Body).Decode(&payload)
	f.mu.Lock()
	f.calls = append(f.calls, telegramCall{
		method:  strings.TrimPrefix(r.URL.Path, "/bottest-token/"),
		payload: payload,
	})
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"ok":true,"result":{}}`)
}

func (f *fakeTelegram) snapshot() []telegramCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]telegramCall, len(f.calls))
	copy(out, f.calls)
	return out
}

func (f *fakeTelegram) lastText() string {
	calls := f.snapshot()
	for i := len(calls) - 1; i >= 0; i-- {
		if calls[i].method == "sendMessage" {
			if s, ok := calls[i].payload["text"].(string); ok {
				return s
			}
		}
	}
	return ""
}

func newBotForTest(t *testing.T) (*Bot, *fakeTelegram) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	fake := &fakeTelegram{}
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)

	cfg := &config.Config{
		DataDir:               t.TempDir(),
		AllowInsecureWebhooks: true,
		AllowPrivateWebhooks:  true,
		Telegram: config.Telegram{
			AdminToken:   "test-token",
			AdminChatIDs: []int64{42},
			AdminAPIBase: srv.URL,
		},
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	b := New(cfg, st, nil, log)
	b.offsetAt = ""
	return b, fake
}

func testUpdate(chatID int64, chatType, text string) update {
	return update{
		UpdateID: 1,
		Message: &message{
			MessageID: 7,
			From:      &user{ID: chatID},
			Chat:      chat{ID: chatID, Type: chatType},
			Text:      text,
		},
	}
}

func TestBotIgnoresUnauthorizedAndGroups(t *testing.T) {
	b, fake := newBotForTest(t)
	b.handle(context.Background(), testUpdate(99, "private", "/help"))
	b.handle(context.Background(), testUpdate(42, "group", "/help"))
	if calls := fake.snapshot(); len(calls) != 0 {
		t.Fatalf("unauthorized requests reached Telegram: %d", len(calls))
	}
}

func TestBotHelpRepliesInPrivateChat(t *testing.T) {
	b, fake := newBotForTest(t)
	b.handle(context.Background(), testUpdate(42, "private", "/help@auvyn_bot"))
	text := fake.lastText()
	if !strings.Contains(text, "/client_add") || !strings.Contains(text, "/balance") {
		t.Fatalf("unexpected help text: %q", text)
	}
}

func TestBotCreatesClientAndWebhook(t *testing.T) {
	b, fake := newBotForTest(t)
	b.handle(context.Background(), testUpdate(42, "private",
		"/client_add Acme | http://127.0.0.1:9999/cb | invoice.paid"))
	text := fake.lastText()
	if !strings.Contains(text, "Secret: avn_sec_") ||
		!strings.Contains(text, "Webhook: avn_wh_") {
		t.Fatalf("credentials missing from reply: %q", text)
	}

	clients, err := b.st.ListClients()
	if err != nil || len(clients) != 1 {
		t.Fatalf("clients = %d, err = %v", len(clients), err)
	}
	hooks, err := b.st.ListWebhooks()
	if err != nil || len(hooks) != 1 {
		t.Fatalf("webhooks = %d, err = %v", len(hooks), err)
	}
	if hooks[0].URL != "http://127.0.0.1:9999/cb" ||
		len(hooks[0].Events) != 1 || hooks[0].Events[0] != "invoice.paid" {
		t.Fatalf("unexpected webhook: %+v", hooks[0])
	}
}

func TestBotDeleteRequiresConfirm(t *testing.T) {
	b, _ := newBotForTest(t)
	created, err := b.st.CreateClientWithWebhook("Acme", "http://127.0.0.1:9999/cb",
		[]string{"invoice.paid"})
	if err != nil {
		t.Fatal(err)
	}
	b.handle(context.Background(), testUpdate(42, "private", "/client_del "+created.Client.ClientID))
	c, err := b.st.GetClient(created.Client.ClientID)
	if err != nil || c.Status != "ACTIVE" {
		t.Fatalf("client deleted without confirm: %+v, %v", c, err)
	}

	b.handle(context.Background(), testUpdate(42, "private",
		"/client_del "+created.Client.ClientID+" confirm"))
	c, err = b.st.GetClient(created.Client.ClientID)
	if err != nil || c.Status != "DELETED" {
		t.Fatalf("client was not deleted: %+v, %v", c, err)
	}
}

func TestBotWebhookSetKeepsURLWithDash(t *testing.T) {
	b, _ := newBotForTest(t)
	created, err := b.st.CreateClientWithWebhook("Acme", "http://127.0.0.1:9999/cb",
		[]string{"invoice.paid"})
	if err != nil {
		t.Fatal(err)
	}
	b.handle(context.Background(), testUpdate(42, "private",
		"/webhook_set "+created.Webhook.WebhookID+" - invoice.failed"))
	hook, err := b.st.GetWebhook(created.Webhook.WebhookID)
	if err != nil {
		t.Fatal(err)
	}
	if hook.URL != "http://127.0.0.1:9999/cb" {
		t.Fatalf("URL changed to %q", hook.URL)
	}
	if len(hook.Events) != 1 || hook.Events[0] != "invoice.failed" {
		t.Fatalf("events = %v", hook.Events)
	}
}

func TestBotOffsetPersists(t *testing.T) {
	b, _ := newBotForTest(t)
	b.offsetAt = filepath.Join(t.TempDir(), "offset")
	b.offset = 1234
	b.saveOffset()

	next := New(b.cfg, b.st, nil, b.log)
	next.offsetAt = b.offsetAt
	next.loadOffset()
	if next.offset != 1234 {
		t.Fatalf("offset = %d, want 1234", next.offset)
	}
}

func TestBotRunPollsAndAdvancesOffset(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "run.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	var mu sync.Mutex
	polls := 0
	offsets := []any{}
	sent := make(chan struct{}, 1)
	secondPoll := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := strings.TrimPrefix(r.URL.Path, "/bottest-token/")
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		switch method {
		case "getUpdates":
			mu.Lock()
			offsets = append(offsets, payload["offset"])
			polls++
			n := polls
			mu.Unlock()
			if n > 1 {
				select {
				case secondPoll <- struct{}{}:
				default:
				}
				<-r.Context().Done()
				return
			}
			_, _ = io.WriteString(w, `{"ok":true,"result":[{"update_id":10,"message":{"message_id":1,"from":{"id":42},"chat":{"id":42,"type":"private"},"text":"/help"}}]}`)
		case "sendMessage":
			select {
			case sent <- struct{}{}:
			default:
			}
			_, _ = io.WriteString(w, `{"ok":true,"result":{}}`)
		default:
			_, _ = io.WriteString(w, `{"ok":true,"result":{}}`)
		}
	}))
	t.Cleanup(srv.Close)

	cfg := &config.Config{
		DataDir: t.TempDir(),
		Telegram: config.Telegram{
			AdminToken:   "test-token",
			AdminChatIDs: []int64{42},
			AdminAPIBase: srv.URL,
		},
	}
	b := New(cfg, st, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	b.offsetAt = ""
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		b.Run(ctx)
		close(done)
	}()

	select {
	case <-sent:
	case <-time.After(3 * time.Second):
		cancel()
		<-done
		t.Fatal("bot did not send a reply")
	}
	select {
	case <-secondPoll:
	case <-time.After(3 * time.Second):
		cancel()
		<-done
		t.Fatal("bot did not poll again")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("bot did not stop after cancellation")
	}
	if b.offset != 11 {
		t.Fatalf("offset = %d, want 11", b.offset)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(offsets) < 2 || offsets[1] != float64(11) {
		t.Fatalf("poll offsets = %v", offsets)
	}
}

func TestBotStartShowsInlineMenu(t *testing.T) {
	b, fake := newBotForTest(t)
	b.handle(context.Background(), testUpdate(42, "private", "/start"))
	calls := fake.snapshot()
	if len(calls) == 0 || calls[len(calls)-1].method != "sendMessage" {
		t.Fatalf("expected sendMessage, got %+v", calls)
	}
	markup, ok := calls[len(calls)-1].payload["reply_markup"].(map[string]any)
	if !ok {
		t.Fatalf("missing reply_markup: %+v", calls[len(calls)-1].payload)
	}
	if _, ok := markup["inline_keyboard"]; !ok {
		t.Fatalf("missing inline_keyboard: %+v", markup)
	}
}

func TestBotCallbackSendsMenu(t *testing.T) {
	b, fake := newBotForTest(t)
	b.handle(context.Background(), update{
		UpdateID: 2,
		CallbackQuery: &callbackQuery{
			ID:   "cb-1",
			From: &user{ID: 42},
			Message: &message{
				MessageID: 8,
				Chat:      chat{ID: 42, Type: "private"},
			},
			Data: "status",
		},
	})
	calls := fake.snapshot()
	var answered, sent bool
	for _, c := range calls {
		switch c.method {
		case "answerCallbackQuery":
			answered = true
		case "sendMessage":
			sent = true
			if _, ok := c.payload["reply_markup"]; !ok {
				t.Fatalf("callback response has no menu: %+v", c.payload)
			}
		}
	}
	if !answered || !sent {
		t.Fatalf("answered=%v sent=%v calls=%+v", answered, sent, calls)
	}
	if text := fake.lastText(); !strings.Contains(text, "Status Auvyn Apies") || strings.Contains(text, "Pilih aksi") {
		t.Fatalf("status should render directly: %q", text)
	}
}

func TestConfigureMenuRegistersCommands(t *testing.T) {
	b, fake := newBotForTest(t)
	b.configureMenu(context.Background())
	var commands, button bool
	for _, c := range fake.snapshot() {
		switch c.method {
		case "setMyCommands":
			commands = true
			scope, _ := c.payload["scope"].(map[string]any)
			if scope["chat_id"] != float64(42) {
				t.Fatalf("scope = %+v", scope)
			}
		case "setChatMenuButton":
			button = true
		}
	}
	if !commands || !button {
		t.Fatalf("commands=%v button=%v calls=%+v", commands, button, fake.snapshot())
	}
}

func testCallback(chatID int64, data string) update {
	return update{
		UpdateID: 3,
		CallbackQuery: &callbackQuery{
			ID:   "cb-" + data,
			From: &user{ID: chatID},
			Message: &message{
				MessageID: 9,
				Chat:      chat{ID: chatID, Type: "private"},
			},
			Data: data,
		},
	}
}

func TestClientsSubmenuPagination(t *testing.T) {
	b, fake := newBotForTest(t)
	for i := 0; i < 12; i++ {
		if _, err := b.st.CreateClient(fmt.Sprintf("Client %02d", i)); err != nil {
			t.Fatal(err)
		}
	}
	b.handle(context.Background(), testCallback(42, "clients"))
	if text := fake.lastText(); !strings.Contains(text, "Pilih aksi") {
		t.Fatalf("clients submenu missing: %q", text)
	}
	b.handle(context.Background(), testCallback(42, "clp:0"))
	text := fake.lastText()
	if !strings.Contains(text, "Total: 12") || strings.Contains(text, "11. ") {
		t.Fatalf("page 1 text = %q", text)
	}
	if !strings.Contains(fmt.Sprint(fake.snapshot()), "clp:1") {
		t.Fatalf("next page button missing: %+v", fake.snapshot())
	}
	b.handle(context.Background(), testCallback(42, "clp:1"))
	text = fake.lastText()
	if !strings.Contains(text, "11. ") || !strings.Contains(text, "12. ") {
		t.Fatalf("page 2 text = %q", text)
	}
}

func TestClientCheckPromptAndInput(t *testing.T) {
	b, fake := newBotForTest(t)
	created, err := b.st.CreateClient("Acme")
	if err != nil {
		t.Fatal(err)
	}
	b.handle(context.Background(), testCallback(42, "cl_check"))
	if text := fake.lastText(); !strings.Contains(text, "Kirim client_id") {
		t.Fatalf("prompt missing: %q", text)
	}
	b.handle(context.Background(), testUpdate(42, "private", created.Client.ClientID))
	text := fake.lastText()
	if !strings.Contains(text, "Client: "+created.Client.ClientID) ||
		!strings.Contains(text, "Nama: Acme") {
		t.Fatalf("client detail = %q", text)
	}
}

func TestClientDeleteViaMenuConfirm(t *testing.T) {
	b, fake := newBotForTest(t)
	created, err := b.st.CreateClient("Acme")
	if err != nil {
		t.Fatal(err)
	}
	b.handle(context.Background(), testCallback(42, "cl_del"))
	b.handle(context.Background(), testUpdate(42, "private", created.Client.ClientID))
	if text := fake.lastText(); !strings.Contains(text, "Nonaktifkan client") {
		t.Fatalf("confirmation missing: %q", text)
	}
	c, err := b.st.GetClient(created.Client.ClientID)
	if err != nil || c.Status != "ACTIVE" {
		t.Fatalf("deleted before confirmation: %+v %v", c, err)
	}
	b.handle(context.Background(), testCallback(42, "cl_del_yes"))
	c, err = b.st.GetClient(created.Client.ClientID)
	if err != nil || c.Status != "DELETED" {
		t.Fatalf("not deleted: %+v %v", c, err)
	}
}

func TestBotClientAddAllEvents(t *testing.T) {
	b, fake := newBotForTest(t)
	b.handle(context.Background(), testUpdate(42, "private",
		"/client_add Fadzpay | https://example.com/cb | all"))
	if text := fake.lastText(); !strings.Contains(text, "Client dibuat") {
		t.Fatalf("reply = %q", text)
	}
	hooks, err := b.st.ListWebhooks()
	if err != nil || len(hooks) != 1 {
		t.Fatalf("webhooks = %d, err = %v", len(hooks), err)
	}
	if len(hooks[0].Events) != 1 || hooks[0].Events[0] != "invoice.*" {
		t.Fatalf("events = %v", hooks[0].Events)
	}
}
