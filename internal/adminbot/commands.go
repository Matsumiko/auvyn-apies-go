package adminbot

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Matsumiko/auvyn-apies-go/internal/store"
	"github.com/Matsumiko/auvyn-apies-go/internal/version"
	"github.com/Matsumiko/auvyn-apies-go/internal/webhook"
)

func (b *Bot) execute(ctx context.Context, cmd, rest string) string {
	switch cmd {
	case "start":
		return startText()
	case "help":
		return helpText()
	case "menu":
		return "📋 Pilih menu di bawah."
	case "status", "stats":
		return b.cmdStatus(ctx)
	case "balance", "saldo":
		return b.cmdBalance(ctx)
	case "clients", "listclients":
		return b.cmdClients(rest)
	case "client", "getclient":
		return b.cmdClient(rest)
	case "client_add", "addclient":
		return b.cmdClientAdd(rest)
	case "client_del", "delclient":
		return b.cmdClientDel(rest)
	case "rotate":
		return b.cmdRotate(rest)
	case "webhooks", "listwebhooks":
		return b.cmdWebhooks(rest)
	case "webhook_add", "addwebhook":
		return b.cmdWebhookAdd(rest)
	case "webhook_set", "setwebhook":
		return b.cmdWebhookSet(rest)
	case "webhook_del", "delwebhook":
		return b.cmdWebhookDel(rest)
	case "trx":
		return b.cmdTrx(rest)
	default:
		return "Perintah tidak dikenal. Ketik /help."
	}
}

func startText() string {
	return `🤖 Auvyn Apies Admin

Pilih menu di bawah atau ketik /help.
Bot hanya melayani private chat admin.`
}

func helpText() string {
	return `🤖 Auvyn Apies Admin

📊 Operasional
/status — kondisi service
/balance — saldo provider
/menu — tampilkan tombol menu

👥 Client
/clients [limit]
/client <client_id>
/client_add <name> | <url|-> | [events]
/client_del <client_id> confirm
/rotate <client_id> confirm

🔗 Webhook
/webhooks [client_id]
/webhook_add <client_id> <url> [events]
/webhook_set <webhook_id> <url|-> [events]
/webhook_del <webhook_id> confirm

🧾 Ledger
/trx <refID>
/batal — batalkan input aktif

Events: invoice.paid, invoice.failed, invoice.pending, invoice.* (semua)
Default jika kosong: invoice.paid,invoice.failed,invoice.pending
Perintah yang mengubah data wajib diakhiri "confirm".
Gunakan private chat; bot menolak grup dan chat yang tidak terdaftar.`
}

// configureMenu installs the Telegram command menu and the persistent menu
// button for each allowlisted private chat. Failures are logged, not fatal.
func (b *Bot) configureMenu(ctx context.Context) {
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmds := menuCommands()
	for id := range b.allowed {
		scope := map[string]any{"type": "chat", "chat_id": id}
		if _, err := b.call(cctx, "setMyCommands", map[string]any{
			"scope": scope, "commands": cmds,
		}); err != nil {
			b.log.Warn("telegram admin setMyCommands failed",
				"chat_id", id, "error", safeTelegramError(err))
		}
		if _, err := b.call(cctx, "setChatMenuButton", map[string]any{
			"chat_id":     id,
			"menu_button": map[string]any{"type": "commands"},
		}); err != nil {
			b.log.Warn("telegram admin setChatMenuButton failed",
				"chat_id", id, "error", safeTelegramError(err))
		}
	}
	b.log.Info("telegram admin menu configured", "chats", len(b.allowed))
}

func menuCommands() []map[string]any {
	return []map[string]any{
		{"command": "start", "description": "Buka menu admin"},
		{"command": "menu", "description": "Tampilkan tombol menu"},
		{"command": "status", "description": "Kondisi service, transaksi, delivery"},
		{"command": "balance", "description": "Cek saldo provider"},
		{"command": "clients", "description": "Daftar client aktif"},
		{"command": "client", "description": "Detail client dan webhook"},
		{"command": "client_add", "description": "Buat client + webhook"},
		{"command": "client_del", "description": "Nonaktifkan client (confirm)"},
		{"command": "rotate", "description": "Rotasi callback secret (confirm)"},
		{"command": "webhooks", "description": "Daftar webhook"},
		{"command": "webhook_add", "description": "Tambah webhook client"},
		{"command": "webhook_set", "description": "Ubah URL/events webhook"},
		{"command": "webhook_del", "description": "Nonaktifkan webhook (confirm)"},
		{"command": "trx", "description": "Detail transaksi dari ledger"},
		{"command": "batal", "description": "Batalkan input aktif"},
		{"command": "help", "description": "Daftar perintah"},
	}
}

func (b *Bot) cmdStatus(ctx context.Context) string {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	stats, err := b.st.Stats(cctx)
	if err != nil {
		b.log.Error("admin bot stats", "error", err)
		return "Gagal membaca statistik."
	}
	var out strings.Builder
	fmt.Fprintf(&out, "📊 Status Auvyn Apies %s\n", version.Version)
	fmt.Fprintf(&out, "Uptime: %s\n", time.Since(b.startedAt).Round(time.Second))
	fmt.Fprintf(&out, "Clients: %s | Webhooks: %s\n", formatInt(stats.Clients), formatInt(stats.Webhooks))
	fmt.Fprintf(&out, "TRX: %s\n", formatCounts(stats.TransactionsByCategory,
		[]string{"success", "pending", "failed"}))
	fmt.Fprintf(&out, "Report 24h: %s (rejected %s)\n",
		formatInt(stats.ProviderReports24h), formatInt(stats.RejectedReports24h))
	fmt.Fprintf(&out, "Deliveries: %s\n", formatCounts(stats.Deliveries,
		[]string{"pending", "sending", "delivered", "dead"}))
	fmt.Fprintf(&out, "DB: %s", formatBytes(stats.DBSizeBytes))
	return out.String()
}

func (b *Bot) cmdBalance(ctx context.Context) string {
	if b.ip == nil {
		return "Provider client tidak tersedia."
	}
	cctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	res, err := b.ip.CheckBalance(cctx, b.cfg.MemberID, nil)
	if err != nil {
		b.log.Warn("admin bot balance", "error", err)
		return "Gagal cek saldo ke provider."
	}
	if res.Balance == nil {
		return fmt.Sprintf("Saldo tidak terdeteksi (HTTP %d).", res.StatusCode)
	}
	text := fmt.Sprintf("💰 Saldo: Rp %s\nMember: %s\nHTTP: %d",
		formatInt(res.Balance.Remaining), res.MemberID, res.StatusCode)
	if b.cfg.BalanceLowLimit > 0 && res.Balance.Remaining < b.cfg.BalanceLowLimit {
		text += fmt.Sprintf("\nLOW: di bawah Rp %s", formatInt(b.cfg.BalanceLowLimit))
	}
	return text
}

func (b *Bot) cmdClients(rest string) string {
	limit := 20
	if rest != "" {
		n, err := strconv.Atoi(strings.Fields(rest)[0])
		if err != nil || n < 1 || n > 50 {
			return "Limit harus 1-50."
		}
		limit = n
	}
	clients, total, err := b.st.ListClientsPage(limit, 0)
	if err != nil {
		b.log.Error("admin bot list clients", "error", err)
		return "Gagal membaca clients."
	}
	if len(clients) == 0 {
		return "Belum ada client aktif."
	}
	lines := []string{fmt.Sprintf("👥 Clients: %d", total)}
	for _, c := range clients {
		lines = append(lines, fmt.Sprintf("%s | %s | %s",
			c.ClientID, oneLine(c.ClientName, 80), c.Status))
	}
	if total > int64(len(clients)) {
		lines = append(lines, fmt.Sprintf("... %d lainnya; pakai /clients <limit>.",
			total-int64(len(clients))))
	}
	return strings.Join(lines, "\n")
}

func (b *Bot) cmdClient(rest string) string {
	id := strings.TrimSpace(rest)
	if id == "" {
		return "Format: /client <client_id>"
	}
	c, err := b.st.GetClient(id)
	if errors.Is(err, store.ErrNotFound) || (err == nil && c.Status != "ACTIVE") {
		return "Client tidak ditemukan atau tidak aktif."
	}
	if err != nil {
		b.log.Error("admin bot get client", "error", err)
		return "Gagal membaca client."
	}
	hooks, err := b.st.ListWebhooksByClient(id)
	if err != nil {
		b.log.Error("admin bot client webhooks", "error", err)
		return "Client ditemukan, tetapi gagal membaca webhook."
	}
	lines := []string{
		"👤 Client: " + c.ClientID,
		"Nama: " + oneLine(c.ClientName, 100),
		"Status: " + c.Status,
		"Terdaftar: " + c.RegisteredAt.UTC().Format(time.RFC3339),
	}
	if !c.SecretCallbackRotatedAt.IsZero() {
		lines = append(lines, "Callback secret dirotasi: "+
			c.SecretCallbackRotatedAt.UTC().Format(time.RFC3339))
	}
	if len(hooks) == 0 {
		lines = append(lines, "Webhook: belum ada")
	} else {
		lines = append(lines, "Webhook:")
		for _, h := range hooks {
			lines = append(lines, "  "+formatWebhookLine(h))
		}
	}
	return strings.Join(lines, "\n")
}

func (b *Bot) cmdClientAdd(rest string) string {
	name, hookURL, events, err := parseClientAdd(rest)
	if err != nil {
		return err.Error()
	}
	if hookURL != "-" {
		if err := webhook.ValidateURL(hookURL, b.cfg.AllowInsecureWebhooks, b.cfg.AllowPrivateWebhooks); err != nil {
			return "URL webhook ditolak: " + err.Error()
		}
	}
	created, err := b.st.CreateClient(name)
	if err != nil {
		b.log.Error("admin bot create client", "error", err)
		return "Gagal membuat client."
	}
	hook := (*store.Webhook)(nil)
	if hookURL != "-" {
		hook, err = b.st.CreateWebhook(created.Client.ClientID, hookURL, events)
		if err != nil {
			_ = b.st.DeleteClient(created.Client.ClientID)
			b.log.Error("admin bot create webhook for new client", "error", err)
			return "Client dibatalkan: gagal membuat webhook."
		}
	}
	lines := []string{
		"✅ Client dibuat. Simpan kredensial ini sekarang; tidak bisa dilihat lagi.",
		"",
		"ID: " + created.Client.ClientID,
		"Nama: " + oneLine(created.Client.ClientName, 100),
		"Secret: " + created.Secret,
		"PIN: " + created.PIN,
		"Keys: " + created.Keys,
		"Callback secret: " + created.SecretCallback,
		"",
		"Auth transaksi:",
		"Authorization: Bearer <Secret>",
		"X-Client-Pin: <PIN>",
		"X-Client-Keys: <Keys>",
	}
	if hook != nil {
		lines = append(lines, "Webhook: "+hook.WebhookID+" | "+oneLine(hook.URL, 120))
	} else {
		lines = append(lines, "Webhook: belum dibuat")
	}
	return strings.Join(lines, "\n")
}

func (b *Bot) cmdClientDel(rest string) string {
	fields := strings.Fields(rest)
	if len(fields) != 2 || !strings.EqualFold(fields[1], "confirm") {
		return "Konfirmasi diperlukan: /client_del <client_id> confirm"
	}
	if err := b.st.DeleteClient(fields[0]); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return "Client tidak ditemukan atau sudah tidak aktif."
		}
		b.log.Error("admin bot delete client", "error", err)
		return "Gagal menghapus client."
	}
	return "Client dinonaktifkan: " + fields[0]
}

func (b *Bot) cmdRotate(rest string) string {
	fields := strings.Fields(rest)
	if len(fields) != 2 || !strings.EqualFold(fields[1], "confirm") {
		return "Konfirmasi diperlukan: /rotate <client_id> confirm"
	}
	value, err := b.st.RotateSecretCallback(fields[0])
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return "Client tidak ditemukan atau tidak aktif."
		}
		b.log.Error("admin bot rotate callback secret", "error", err)
		return "Gagal merotasi callback secret."
	}
	return "Callback secret baru untuk " + fields[0] + ":\n" + value +
		"\n\nSecret lama langsung tidak valid. Simpan sekarang."
}

func (b *Bot) cmdWebhooks(rest string) string {
	var (
		hooks []store.Webhook
		total int64
		title string
		err   error
	)
	if clientID := strings.TrimSpace(rest); clientID != "" {
		c, getErr := b.st.GetClient(clientID)
		if errors.Is(getErr, store.ErrNotFound) || (getErr == nil && c.Status != "ACTIVE") {
			return "Client tidak ditemukan atau tidak aktif."
		}
		if getErr != nil {
			b.log.Error("admin bot webhooks client", "error", getErr)
			return "Gagal membaca client."
		}
		hooks, err = b.st.ListWebhooksByClient(clientID)
		total = int64(len(hooks))
		title = "🔗 Webhooks " + clientID + ": " + formatInt(total)
	} else {
		hooks, total, err = b.st.ListWebhooksPage(50, 0)
		title = "🔗 Webhooks: " + formatInt(total)
	}
	if err != nil {
		b.log.Error("admin bot list webhooks", "error", err)
		return "Gagal membaca webhooks."
	}
	if len(hooks) == 0 {
		return "Belum ada webhook aktif."
	}
	lines := []string{title}
	for _, h := range hooks {
		lines = append(lines, formatWebhookLine(h))
	}
	if total > int64(len(hooks)) {
		lines = append(lines, fmt.Sprintf("... %d lainnya.", total-int64(len(hooks))))
	}
	return strings.Join(lines, "\n")
}

func (b *Bot) cmdWebhookAdd(rest string) string {
	fields := strings.Fields(rest)
	if len(fields) < 2 {
		return "Format: /webhook_add <client_id> <url> [events]"
	}
	clientID, hookURL := fields[0], fields[1]
	if err := webhook.ValidateURL(hookURL, b.cfg.AllowInsecureWebhooks, b.cfg.AllowPrivateWebhooks); err != nil {
		return "URL webhook ditolak: " + err.Error()
	}
	events := normalizeBotEvents(strings.Join(fields[2:], " "))
	if err := validateBotEvents(events); err != nil {
		return err.Error()
	}
	hook, err := b.st.CreateWebhook(clientID, hookURL, events)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			return "Client tidak ditemukan."
		case errors.Is(err, store.ErrNotActive):
			return "Client tidak aktif."
		}
		b.log.Error("admin bot create webhook", "error", err)
		return "Gagal membuat webhook."
	}
	return fmt.Sprintf("Webhook dibuat: %s\nClient: %s\nURL: %s\nEvents: %s",
		hook.WebhookID, hook.ClientID, hook.URL, strings.Join(hook.Events, ","))
}

func (b *Bot) cmdWebhookSet(rest string) string {
	fields := strings.Fields(rest)
	if len(fields) < 2 {
		return "Format: /webhook_set <webhook_id> <url|-> [events]"
	}
	id, hookURL := fields[0], fields[1]
	setEvents := len(fields) > 2
	if hookURL == "-" && !setEvents {
		return "Isi URL baru atau events baru."
	}
	if hookURL != "-" {
		if err := webhook.ValidateURL(hookURL, b.cfg.AllowInsecureWebhooks, b.cfg.AllowPrivateWebhooks); err != nil {
			return "URL webhook ditolak: " + err.Error()
		}
	}
	var events []string
	if setEvents {
		events = normalizeBotEvents(strings.Join(fields[2:], " "))
		if err := validateBotEvents(events); err != nil {
			return err.Error()
		}
	}
	updateURL := hookURL
	if updateURL == "-" {
		updateURL = ""
	}
	hook, err := b.st.UpdateWebhookAdmin(id, updateURL, events, setEvents)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			return "Webhook tidak ditemukan."
		case errors.Is(err, store.ErrNotActive):
			return "Webhook tidak aktif."
		}
		b.log.Error("admin bot update webhook", "error", err)
		return "Gagal memperbarui webhook."
	}
	return fmt.Sprintf("Webhook diperbarui: %s\nURL: %s\nEvents: %s",
		hook.WebhookID, hook.URL, strings.Join(hook.Events, ","))
}

func (b *Bot) cmdWebhookDel(rest string) string {
	fields := strings.Fields(rest)
	if len(fields) != 2 || !strings.EqualFold(fields[1], "confirm") {
		return "Konfirmasi diperlukan: /webhook_del <webhook_id> confirm"
	}
	if err := b.st.DeleteWebhook(fields[0]); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return "Webhook tidak ditemukan atau sudah tidak aktif."
		}
		b.log.Error("admin bot delete webhook", "error", err)
		return "Gagal menghapus webhook."
	}
	return "Webhook dinonaktifkan: " + fields[0]
}

func (b *Bot) cmdTrx(rest string) string {
	refID := strings.TrimSpace(rest)
	if refID == "" {
		return "Format: /trx <refID>"
	}
	t, err := b.st.GetTransaction(refID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return "Transaksi tidak ditemukan."
		}
		b.log.Error("admin bot get transaction", "error", err)
		return "Gagal membaca transaksi."
	}
	qty, nominal := "-", "-"
	if t.Qty != nil {
		qty = strconv.FormatInt(*t.Qty, 10)
	}
	if t.Nominal != nil {
		nominal = formatInt(*t.Nominal)
	}
	final := "-"
	if t.FinalAt != nil {
		final = t.FinalAt.UTC().Format(time.RFC3339)
	}
	lines := []string{
		"RefID: " + t.RefID,
		"Client: " + dash(t.ClientID),
		"Produk: " + dash(t.Product),
		"Tujuan: " + dash(t.Dest),
		"Qty: " + qty,
		"Nominal: " + nominal,
		"Kategori: " + dash(t.Category),
		"Provider: " + dash(t.ProviderState) + " / " + dash(t.ProviderCode),
		"TrxID: " + dash(t.ProviderTrxID),
		"SN: " + dash(t.SN),
		"Dibuat: " + t.CreatedAt.UTC().Format(time.RFC3339),
		"Final: " + final,
	}
	return strings.Join(lines, "\n")
}

func parseClientAdd(rest string) (name, hookURL string, events []string, err error) {
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return "", "", nil, errors.New("Format: /client_add <name> | <url|-> | [events]")
	}
	if strings.Contains(rest, "|") {
		parts := strings.Split(rest, "|")
		if len(parts) < 2 {
			return "", "", nil, errors.New("Format: /client_add <name> | <url|-> | [events]")
		}
		name = strings.TrimSpace(parts[0])
		hookURL = strings.TrimSpace(parts[1])
		if len(parts) > 2 {
			events = normalizeBotEvents(strings.Join(parts[2:], ","))
		}
	} else {
		fields := strings.Fields(rest)
		if len(fields) < 2 {
			return "", "", nil, errors.New("Format: /client_add <name> | <url|-> | [events]")
		}
		name = fields[0]
		hookURL = fields[1]
		if len(fields) > 2 {
			events = normalizeBotEvents(strings.Join(fields[2:], " "))
		}
	}
	if name == "" {
		return "", "", nil, errors.New("Nama client wajib diisi.")
	}
	if hookURL == "" {
		return "", "", nil, errors.New("URL webhook wajib diisi; gunakan '-' untuk tanpa webhook.")
	}
	if events == nil {
		events = normalizeBotEvents("")
	}
	if hookURL != "-" {
		if err := validateBotEvents(events); err != nil {
			return "", "", nil, err
		}
	}
	return name, hookURL, events, nil
}

func validateBotEvents(events []string) error {
	for _, ev := range events {
		switch ev {
		case "invoice.paid", "invoice.failed", "invoice.pending", "invoice.*":
		default:
			return fmt.Errorf("event tidak dikenal: %s (pakai invoice.paid, invoice.failed, invoice.pending, atau invoice.*)", ev)
		}
	}
	return nil
}

func normalizeBotEvents(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return []string{"invoice.paid", "invoice.failed", "invoice.pending"}
	}
	if strings.EqualFold(raw, "all") || raw == "*" {
		return []string{"invoice.*"}
	}
	seen := map[string]bool{}
	var out []string
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\n' || r == '\t'
	}) {
		part = strings.TrimSpace(part)
		if part == "" || seen[part] {
			continue
		}
		seen[part] = true
		out = append(out, part)
	}
	if len(out) == 0 {
		return []string{"invoice.paid", "invoice.failed", "invoice.pending"}
	}
	return out
}

func formatWebhookLine(h store.Webhook) string {
	events := strings.Join(h.Events, ",")
	if events == "" {
		events = "-"
	}
	return fmt.Sprintf("%s | %s | %s | %s",
		h.WebhookID, h.ClientID, oneLine(h.URL, 100), events)
}

func formatCounts(values map[string]int64, order []string) string {
	if len(values) == 0 {
		return "-"
	}
	var parts []string
	seen := map[string]bool{}
	for _, key := range order {
		if n, ok := values[key]; ok {
			parts = append(parts, key+"="+formatInt(n))
			seen[key] = true
		}
	}
	var extra []string
	for key := range values {
		if !seen[key] {
			extra = append(extra, key)
		}
	}
	sort.Strings(extra)
	for _, key := range extra {
		parts = append(parts, key+"="+formatInt(values[key]))
	}
	return strings.Join(parts, " ")
}

func formatInt(n int64) string {
	neg := n < 0
	if neg {
		n = -n
	}
	s := strconv.FormatInt(n, 10)
	var out strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			out.WriteByte('.')
		}
		out.WriteRune(r)
	}
	if neg {
		return "-" + out.String()
	}
	return out.String()
}

func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit && exp < len("KMGTPE")-1; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len([]rune(s)) > max {
		r := []rune(s)
		return string(r[:max]) + "..."
	}
	return s
}

func dash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}
