package adminbot

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Matsumiko/auvyn-apies-go/internal/webhook"
)

const (
	pageSize   = 10
	pendingTTL = 10 * time.Minute
)

type pendingState struct {
	action  string
	value   string
	back    string
	expires time.Time
}

func (b *Bot) setPending(chatID int64, action, value, back string) {
	b.stateMu.Lock()
	defer b.stateMu.Unlock()
	b.pending[chatID] = pendingState{
		action: action, value: value, back: back,
		expires: time.Now().Add(pendingTTL),
	}
}

func (b *Bot) getPending(chatID int64) (pendingState, bool) {
	b.stateMu.Lock()
	defer b.stateMu.Unlock()
	p, ok := b.pending[chatID]
	if !ok {
		return pendingState{}, false
	}
	if time.Now().After(p.expires) {
		delete(b.pending, chatID)
		return pendingState{}, false
	}
	return p, true
}

func (b *Bot) clearPending(chatID int64) {
	b.stateMu.Lock()
	defer b.stateMu.Unlock()
	delete(b.pending, chatID)
}

func btn(text, data string) map[string]any {
	return map[string]any{"text": text, "callback_data": data}
}

func keyboard(rows ...[]map[string]any) map[string]any {
	return map[string]any{"inline_keyboard": rows}
}

func mainMenuKeyboard() map[string]any {
	return keyboard(
		[]map[string]any{btn("📊 Status", "status"), btn("💰 Saldo", "balance")},
		[]map[string]any{btn("👥 Clients", "clients"), btn("🔗 Webhooks", "webhooks")},
		[]map[string]any{btn("🧾 Transaksi", "trx_menu"), btn("❓ Bantuan", "help")},
	)
}

func clientsMenuKeyboard() map[string]any {
	return keyboard(
		[]map[string]any{btn("📋 Daftar Clients", "clp:0"), btn("🔎 Cek Client", "cl_check")},
		[]map[string]any{btn("➕ Tambah Client", "cl_add"), btn("🗑 Hapus Client", "cl_del")},
		[]map[string]any{btn("🔑 Rotate Secret", "cl_rotate")},
		[]map[string]any{btn("🏠 Menu", "menu")},
	)
}

func webhooksMenuKeyboard() map[string]any {
	return keyboard(
		[]map[string]any{btn("📋 Daftar Webhooks", "whp:0"), btn("🔎 Cek Webhook", "wh_check")},
		[]map[string]any{btn("➕ Tambah Webhook", "wh_add"), btn("✏️ Ubah Webhook", "wh_set")},
		[]map[string]any{btn("🗑 Hapus Webhook", "wh_del")},
		[]map[string]any{btn("🏠 Menu", "menu")},
	)
}

func trxMenuKeyboard() map[string]any {
	return keyboard(
		[]map[string]any{btn("🔎 Cek RefID", "trx_check")},
		[]map[string]any{btn("🏠 Menu", "menu")},
	)
}

func resultMenuKeyboard() map[string]any {
	return keyboard([]map[string]any{btn("🏠 Menu", "menu")})
}

func backKeyboard(back string) map[string]any {
	return keyboard([]map[string]any{btn("⬅️ Kembali", back), btn("🏠 Menu", "menu")})
}

func cancelKeyboard(back string) map[string]any {
	return keyboard([]map[string]any{btn("❌ Batal", "cancel"), btn("⬅️ Kembali", back)})
}

func confirmKeyboard(yesAction, back string) map[string]any {
	return keyboard([]map[string]any{btn("✅ Ya, lanjutkan", yesAction), btn("❌ Batal", "cancel")})
}

func pageKeyboard(prefix string, page int, total int64, back string) map[string]any {
	pages := int((total + pageSize - 1) / pageSize)
	if pages < 1 {
		pages = 1
	}
	var nav []map[string]any
	if page > 0 {
		nav = append(nav, btn("⬅️ Sebelumnya", fmt.Sprintf("%s:%d", prefix, page-1)))
	}
	if page+1 < pages {
		nav = append(nav, btn("Berikutnya ➡️", fmt.Sprintf("%s:%d", prefix, page+1)))
	}
	rows := [][]map[string]any{}
	if len(nav) > 0 {
		rows = append(rows, nav)
	}
	rows = append(rows, []map[string]any{btn("⬅️ Kembali", back), btn("🏠 Menu", "menu")})
	return keyboard(rows...)
}

func (b *Bot) handleCommand(ctx context.Context, chatID, replyTo int64, cmd, rest string) {
	switch cmd {
	case "start", "menu":
		b.sendMainMenu(ctx, chatID, replyTo)
	case "help":
		b.sendHelp(ctx, chatID, replyTo)
	case "status", "stats":
		b.sendStatus(ctx, chatID, replyTo)
	case "balance", "saldo":
		b.sendBalance(ctx, chatID, replyTo)
	case "clients", "listclients":
		b.sendClientPage(ctx, chatID, replyTo, 0)
	case "webhooks", "listwebhooks":
		b.sendWebhookPage(ctx, chatID, replyTo, 0)
	case "trx":
		b.sendLongKeyboard(ctx, chatID, replyTo, b.cmdTrx(rest), trxMenuKeyboard())
	default:
		reply := b.execute(ctx, cmd, rest)
		if reply != "" {
			b.sendLongKeyboard(ctx, chatID, replyTo, reply, keyboardForCommand(cmd))
		}
	}
}

func keyboardForCommand(cmd string) map[string]any {
	switch cmd {
	case "client", "getclient", "client_add", "addclient", "client_del", "delclient", "rotate":
		return clientsMenuKeyboard()
	case "webhook_add", "addwebhook", "webhook_set", "setwebhook", "webhook_del", "delwebhook":
		return webhooksMenuKeyboard()
	case "trx":
		return trxMenuKeyboard()
	default:
		return mainMenuKeyboard()
	}
}

func (b *Bot) sendMainMenu(ctx context.Context, chatID, replyTo int64) {
	b.sendLongKeyboard(ctx, chatID, replyTo, "🏠 Menu Admin\n\nPilih kategori:", mainMenuKeyboard())
}

func (b *Bot) sendClientsMenu(ctx context.Context, chatID, replyTo int64) {
	b.sendLongKeyboard(ctx, chatID, replyTo, "👥 Clients\n\nPilih aksi:", clientsMenuKeyboard())
}

func (b *Bot) sendWebhooksMenu(ctx context.Context, chatID, replyTo int64) {
	b.sendLongKeyboard(ctx, chatID, replyTo, "🔗 Webhooks\n\nPilih aksi:", webhooksMenuKeyboard())
}

func (b *Bot) sendTrxMenu(ctx context.Context, chatID, replyTo int64) {
	b.sendLongKeyboard(ctx, chatID, replyTo, "🧾 Transaksi\n\nCek detail transaksi dari ledger:", trxMenuKeyboard())
}

func (b *Bot) sendStatus(ctx context.Context, chatID, replyTo int64) {
	b.sendLongKeyboard(ctx, chatID, replyTo, b.cmdStatus(ctx), resultMenuKeyboard())
}

func (b *Bot) sendBalance(ctx context.Context, chatID, replyTo int64) {
	b.sendLongKeyboard(ctx, chatID, replyTo, b.cmdBalance(ctx), resultMenuKeyboard())
}

func (b *Bot) sendHelp(ctx context.Context, chatID, replyTo int64) {
	b.sendLongKeyboard(ctx, chatID, replyTo, helpText(), resultMenuKeyboard())
}

func (b *Bot) sendClientPage(ctx context.Context, chatID, replyTo int64, page int) {
	if page < 0 {
		page = 0
	}
	clients, total, err := b.st.ListClientsPage(pageSize, page*pageSize)
	if err != nil {
		b.log.Error("admin bot list clients", "error", err)
		b.sendLongKeyboard(ctx, chatID, replyTo, "⚠️ Gagal membaca clients.", clientsMenuKeyboard())
		return
	}
	maxPage := int((total - 1) / pageSize)
	if total == 0 {
		maxPage = 0
	}
	if page > maxPage {
		page = maxPage
		clients, total, err = b.st.ListClientsPage(pageSize, page*pageSize)
		if err != nil {
			b.log.Error("admin bot list clients", "error", err)
			b.sendLongKeyboard(ctx, chatID, replyTo, "⚠️ Gagal membaca clients.", clientsMenuKeyboard())
			return
		}
	}
	pages := int((total + pageSize - 1) / pageSize)
	if pages < 1 {
		pages = 1
	}
	var out strings.Builder
	fmt.Fprintf(&out, "👥 Daftar Clients (%d/%d)\nTotal: %s\n", page+1, pages, formatInt(total))
	if len(clients) == 0 {
		out.WriteString("\nBelum ada client aktif.")
	} else {
		out.WriteString("\n")
		for i, c := range clients {
			fmt.Fprintf(&out, "%d. %s | %s\n", page*pageSize+i+1, c.ClientID, oneLine(c.ClientName, 48))
		}
	}
	b.sendLongKeyboard(ctx, chatID, replyTo, out.String(), pageKeyboard("clp", page, total, "clients"))
}

func (b *Bot) sendWebhookPage(ctx context.Context, chatID, replyTo int64, page int) {
	if page < 0 {
		page = 0
	}
	hooks, total, err := b.st.ListWebhooksPage(pageSize, page*pageSize)
	if err != nil {
		b.log.Error("admin bot list webhooks", "error", err)
		b.sendLongKeyboard(ctx, chatID, replyTo, "⚠️ Gagal membaca webhooks.", webhooksMenuKeyboard())
		return
	}
	maxPage := int((total - 1) / pageSize)
	if total == 0 {
		maxPage = 0
	}
	if page > maxPage {
		page = maxPage
		hooks, total, err = b.st.ListWebhooksPage(pageSize, page*pageSize)
		if err != nil {
			b.log.Error("admin bot list webhooks", "error", err)
			b.sendLongKeyboard(ctx, chatID, replyTo, "⚠️ Gagal membaca webhooks.", webhooksMenuKeyboard())
			return
		}
	}
	pages := int((total + pageSize - 1) / pageSize)
	if pages < 1 {
		pages = 1
	}
	var out strings.Builder
	fmt.Fprintf(&out, "🔗 Daftar Webhooks (%d/%d)\nTotal: %s\n", page+1, pages, formatInt(total))
	if len(hooks) == 0 {
		out.WriteString("\nBelum ada webhook aktif.")
	} else {
		out.WriteString("\n")
		for i, h := range hooks {
			fmt.Fprintf(&out, "%d. %s | %s | %s\n", page*pageSize+i+1,
				h.WebhookID, h.ClientID, oneLine(h.URL, 70))
		}
	}
	b.sendLongKeyboard(ctx, chatID, replyTo, out.String(), pageKeyboard("whp", page, total, "webhooks"))
}

func (b *Bot) sendWebhookDetail(ctx context.Context, chatID, replyTo int64, id string) {
	hook, err := b.st.GetWebhook(strings.TrimSpace(id))
	if err != nil {
		b.sendLongKeyboard(ctx, chatID, replyTo, "⚠️ Webhook tidak ditemukan.", webhooksMenuKeyboard())
		return
	}
	events := strings.Join(hook.Events, ", ")
	if events == "" {
		events = "-"
	}
	text := fmt.Sprintf("🔗 Webhook: %s\nClient: %s\nURL: %s\nStatus: %s\nEvents: %s",
		hook.WebhookID, hook.ClientID, hook.URL, hook.Status, events)
	b.sendLongKeyboard(ctx, chatID, replyTo, text, webhooksMenuKeyboard())
}

func (b *Bot) promptText(ctx context.Context, chatID int64, action, text, back string) {
	b.setPending(chatID, action, "", back)
	b.sendLongKeyboard(ctx, chatID, 0, text, cancelKeyboard(back))
}

func (b *Bot) handlePending(ctx context.Context, chatID int64, p pendingState, text string) {
	b.clearPending(chatID)
	text = strings.TrimSpace(text)
	switch p.action {
	case "client_check":
		b.sendLongKeyboard(ctx, chatID, 0, b.cmdClient(text), clientsMenuKeyboard())
	case "client_add":
		b.sendLongKeyboard(ctx, chatID, 0, b.cmdClientAdd(text), clientsMenuKeyboard())
	case "client_del":
		c, err := b.st.GetClient(text)
		if err != nil || c.Status != "ACTIVE" {
			b.sendLongKeyboard(ctx, chatID, 0, "⚠️ Client tidak ditemukan atau tidak aktif.", clientsMenuKeyboard())
			return
		}
		b.setPending(chatID, "client_del_confirm", text, "clients")
		b.sendLongKeyboard(ctx, chatID, 0, "⚠️ Nonaktifkan client ini?\n\n"+text,
			confirmKeyboard("cl_del_yes", "clients"))
	case "client_rotate":
		c, err := b.st.GetClient(text)
		if err != nil || c.Status != "ACTIVE" {
			b.sendLongKeyboard(ctx, chatID, 0, "⚠️ Client tidak ditemukan atau tidak aktif.", clientsMenuKeyboard())
			return
		}
		b.setPending(chatID, "client_rotate_confirm", text, "clients")
		b.sendLongKeyboard(ctx, chatID, 0, "⚠️ Rotasi callback secret client ini? Secret lama langsung invalid.\n\n"+text,
			confirmKeyboard("cl_rotate_yes", "clients"))
	case "webhook_check":
		b.sendWebhookDetail(ctx, chatID, 0, text)
	case "webhook_add":
		b.sendLongKeyboard(ctx, chatID, 0, b.createWebhookInput(text), webhooksMenuKeyboard())
	case "webhook_set":
		b.sendLongKeyboard(ctx, chatID, 0, b.updateWebhookInput(text), webhooksMenuKeyboard())
	case "webhook_del":
		h, err := b.st.GetWebhook(text)
		if err != nil {
			b.sendLongKeyboard(ctx, chatID, 0, "⚠️ Webhook tidak ditemukan.", webhooksMenuKeyboard())
			return
		}
		b.setPending(chatID, "webhook_del_confirm", h.WebhookID, "webhooks")
		b.sendLongKeyboard(ctx, chatID, 0, "⚠️ Nonaktifkan webhook ini?\n\n"+h.WebhookID+" | "+h.URL,
			confirmKeyboard("wh_del_yes", "webhooks"))
	case "trx_check":
		b.sendLongKeyboard(ctx, chatID, 0, b.cmdTrx(text), trxMenuKeyboard())
	default:
		b.sendMainMenu(ctx, chatID, 0)
	}
}

func (b *Bot) createWebhookInput(raw string) string {
	clientID, hookURL, events, err := b.parseWebhookAddInput(raw)
	if err != nil {
		return "⚠️ " + err.Error()
	}
	hook, err := b.st.CreateWebhook(clientID, hookURL, events)
	if err != nil {
		b.log.Error("admin bot create webhook", "error", err)
		return "⚠️ Gagal membuat webhook."
	}
	return fmt.Sprintf("✅ Webhook dibuat: %s\nClient: %s\nURL: %s\nEvents: %s",
		hook.WebhookID, hook.ClientID, hook.URL, strings.Join(hook.Events, ", "))
}

func (b *Bot) updateWebhookInput(raw string) string {
	id, hookURL, events, setEvents, err := b.parseWebhookSetInput(raw)
	if err != nil {
		return "⚠️ " + err.Error()
	}
	hook, err := b.st.UpdateWebhookAdmin(id, hookURL, events, setEvents)
	if err != nil {
		b.log.Error("admin bot update webhook", "error", err)
		return "⚠️ Gagal memperbarui webhook."
	}
	return fmt.Sprintf("✅ Webhook diperbarui: %s\nURL: %s\nEvents: %s",
		hook.WebhookID, hook.URL, strings.Join(hook.Events, ", "))
}

func (b *Bot) handleCallback(ctx context.Context, cb *callbackQuery) {
	if cb == nil || cb.From == nil {
		return
	}
	chatID := cb.From.ID
	chatType := "private"
	if cb.Message != nil {
		chatID = cb.Message.Chat.ID
		chatType = cb.Message.Chat.Type
	}
	if chatType != "private" {
		b.answerCallback(ctx, cb.ID, "Hanya private chat.")
		return
	}
	if _, ok := b.allowed[chatID]; !ok {
		b.log.Warn("telegram admin rejected callback from unknown chat",
			"chat_id", chatID, "user_id", cb.From.ID)
		b.answerCallback(ctx, cb.ID, "Tidak diizinkan.")
		return
	}
	b.answerCallback(ctx, cb.ID, "")
	action := strings.TrimSpace(cb.Data)
	b.log.Info("telegram admin callback",
		"chat_id", chatID, "user_id", cb.From.ID, "action", action)
	if action != "cancel" && !strings.HasSuffix(action, "_yes") {
		b.clearPending(chatID)
	}

	switch {
	case action == "menu":
		b.clearPending(chatID)
		b.sendMainMenu(ctx, chatID, 0)
	case action == "help", action == "help_show":
		b.sendHelp(ctx, chatID, 0)
	case action == "status", action == "status_show":
		b.sendStatus(ctx, chatID, 0)
	case action == "balance", action == "balance_show":
		b.sendBalance(ctx, chatID, 0)
	case action == "clients":
		b.clearPending(chatID)
		b.sendClientsMenu(ctx, chatID, 0)
	case action == "webhooks":
		b.clearPending(chatID)
		b.sendWebhooksMenu(ctx, chatID, 0)
	case action == "trx_menu":
		b.clearPending(chatID)
		b.sendTrxMenu(ctx, chatID, 0)
	case strings.HasPrefix(action, "clp:"):
		page := parsePage(action, "clp:")
		b.sendClientPage(ctx, chatID, 0, page)
	case strings.HasPrefix(action, "whp:"):
		page := parsePage(action, "whp:")
		b.sendWebhookPage(ctx, chatID, 0, page)
	case action == "cl_check":
		b.promptText(ctx, chatID, "client_check",
			"🔎 Kirim client_id yang ingin dicek.\nContoh: avn_cl_xxx", "clients")
	case action == "cl_add":
		b.promptText(ctx, chatID, "client_add",
			"➕ Kirim data client:\n\nnama | url | events\n\nContoh:\nAcme | https://example.com/cb | invoice.paid\n\nGunakan - untuk URL jika tanpa webhook.", "clients")
	case action == "cl_del":
		b.promptText(ctx, chatID, "client_del",
			"🗑 Kirim client_id yang ingin dinonaktifkan.", "clients")
	case action == "cl_rotate":
		b.promptText(ctx, chatID, "client_rotate",
			"🔑 Kirim client_id yang callback secret-nya ingin dirotasi.", "clients")
	case action == "wh_check":
		b.promptText(ctx, chatID, "webhook_check",
			"🔎 Kirim webhook_id yang ingin dicek.", "webhooks")
	case action == "wh_add":
		b.promptText(ctx, chatID, "webhook_add",
			"➕ Kirim data webhook:\n\nclient_id | url | events\n\nContoh:\navn_cl_xxx | https://example.com/cb | invoice.paid", "webhooks")
	case action == "wh_set":
		b.promptText(ctx, chatID, "webhook_set",
			"✏️ Kirim data update:\n\nwebhook_id | url|-> | events\n\nGunakan - untuk mempertahankan URL; kosongkan events untuk mempertahankan events.", "webhooks")
	case action == "wh_del":
		b.promptText(ctx, chatID, "webhook_del",
			"🗑 Kirim webhook_id yang ingin dinonaktifkan.", "webhooks")
	case action == "trx_check":
		b.promptText(ctx, chatID, "trx_check",
			"🧾 Kirim refID transaksi.", "trx_menu")
	case action == "cancel":
		p, _ := b.getPending(chatID)
		b.clearPending(chatID)
		b.sendBackMenu(ctx, chatID, p.back)
	case action == "cl_del_yes":
		b.finishConfirm(ctx, chatID, "client_del_confirm", "clients", func(value string) string {
			return b.cmdClientDel(value + " confirm")
		})
	case action == "cl_rotate_yes":
		b.finishConfirm(ctx, chatID, "client_rotate_confirm", "clients", func(value string) string {
			return b.cmdRotate(value + " confirm")
		})
	case action == "wh_del_yes":
		b.finishConfirm(ctx, chatID, "webhook_del_confirm", "webhooks", func(value string) string {
			return b.cmdWebhookDel(value + " confirm")
		})
	default:
		b.sendMainMenu(ctx, chatID, 0)
	}
}

func (b *Bot) finishConfirm(ctx context.Context, chatID int64, wantAction, back string, run func(string) string) {
	p, ok := b.getPending(chatID)
	if !ok || p.action != wantAction {
		b.sendBackMenu(ctx, chatID, back)
		return
	}
	b.clearPending(chatID)
	b.sendLongKeyboard(ctx, chatID, 0, run(p.value), backKeyboardFor(back))
}

func (b *Bot) sendBackMenu(ctx context.Context, chatID int64, back string) {
	switch back {
	case "clients":
		b.sendClientsMenu(ctx, chatID, 0)
	case "webhooks":
		b.sendWebhooksMenu(ctx, chatID, 0)
	case "trx_menu":
		b.sendTrxMenu(ctx, chatID, 0)
	default:
		b.sendMainMenu(ctx, chatID, 0)
	}
}

func backKeyboardFor(back string) map[string]any {
	switch back {
	case "clients":
		return clientsMenuKeyboard()
	case "webhooks":
		return webhooksMenuKeyboard()
	case "trx_menu":
		return trxMenuKeyboard()
	default:
		return mainMenuKeyboard()
	}
}

func parsePage(action, prefix string) int {
	n, err := strconv.Atoi(strings.TrimPrefix(action, prefix))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func (b *Bot) parseWebhookAddInput(raw string) (clientID, hookURL string, events []string, err error) {
	parts := splitInput(raw)
	if len(parts) < 2 {
		return "", "", nil, fmt.Errorf("format: client_id | url | events")
	}
	clientID = parts[0]
	hookURL = parts[1]
	if clientID == "" || hookURL == "" {
		return "", "", nil, fmt.Errorf("client_id dan url wajib diisi")
	}
	if len(parts) > 2 {
		events = normalizeBotEvents(strings.Join(parts[2:], " "))
	} else {
		events = normalizeBotEvents("")
	}
	if err := validateBotEvents(events); err != nil {
		return "", "", nil, err
	}
	if err := webhook.ValidateURL(hookURL, b.cfg.AllowInsecureWebhooks, b.cfg.AllowPrivateWebhooks); err != nil {
		return "", "", nil, err
	}
	return clientID, hookURL, events, nil
}

func (b *Bot) parseWebhookSetInput(raw string) (id, hookURL string, events []string, setEvents bool, err error) {
	parts := splitInput(raw)
	if len(parts) < 2 {
		return "", "", nil, false, fmt.Errorf("format: webhook_id | url|-> | events")
	}
	id = parts[0]
	hookURL = parts[1]
	if id == "" {
		return "", "", nil, false, fmt.Errorf("webhook_id wajib diisi")
	}
	setEvents = len(parts) > 2
	if hookURL == "-" && !setEvents {
		return "", "", nil, false, fmt.Errorf("isi URL baru atau events baru")
	}
	if hookURL == "-" {
		hookURL = ""
	} else if err := webhook.ValidateURL(hookURL, b.cfg.AllowInsecureWebhooks, b.cfg.AllowPrivateWebhooks); err != nil {
		return "", "", nil, false, err
	}
	if setEvents {
		events = normalizeBotEvents(strings.Join(parts[2:], " "))
		if err := validateBotEvents(events); err != nil {
			return "", "", nil, false, err
		}
	}
	return id, hookURL, events, setEvents, nil
}

func splitInput(raw string) []string {
	raw = strings.TrimSpace(raw)
	if strings.Contains(raw, "|") {
		parts := strings.Split(raw, "|")
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		return parts
	}
	return strings.Fields(raw)
}
