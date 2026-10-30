// Package store persists clients, webhooks, transactions and the webhook
// outbox in SQLite. All writes that change business state use a single
// statement or an explicit transaction so a crash cannot leave a
// half-recorded transaction.
package store

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Matsumiko/auvyn-apies-go/internal/security"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaFS embed.FS

var (
	ErrNotFound     = errors.New("not found")
	ErrDuplicateRef = errors.New("duplicate refID")
	ErrForbidden    = errors.New("forbidden")
	ErrNotActive    = errors.New("not active")
)

type Store struct {
	db   *sql.DB
	path string
}

type Client struct {
	ClientID                string
	ClientName              string
	Status                  string
	RegisteredAt            time.Time
	SecretHash              string
	PINHash                 string
	KeysHash                string
	SecretCallback          string
	SecretCallbackRotatedAt time.Time
}

type PublicClient struct {
	ClientID     string    `json:"client_id"`
	ClientName   string    `json:"client_name"`
	Status       string    `json:"status"`
	RegisteredAt time.Time `json:"registered_at"`
}

type Webhook struct {
	WebhookID  string    `json:"webhook_id"`
	ClientID   string    `json:"client_id"`
	ClientName string    `json:"client_name"`
	URL        string    `json:"url"`
	Status     string    `json:"status,omitempty"`
	Events     []string  `json:"events"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at,omitempty"`
}

type Transaction struct {
	RefID         string
	ClientID      string
	Product       string
	Dest          string
	Qty           *int64
	Nominal       *int64
	Category      string
	ProviderState string
	ProviderCode  string
	ProviderTrxID string
	SN            string
	RequestRaw    string
	ReportRaw     string
	Meta          map[string]any
	CreatedAt     time.Time
	UpdatedAt     time.Time
	FinalAt       *time.Time
}

type ProviderReport struct {
	RefID      string
	Accepted   bool
	Reason     string
	Category   string
	RemoteIP   string
	Method     string
	Query      string
	Body       string
	RawMessage string
	ReceivedAt time.Time
}

type Event struct {
	ID        int64
	RefID     string
	Kind      string
	DedupeKey string
	Payload   string
	CreatedAt time.Time
}

type Delivery struct {
	ID            int64
	EventID       int64
	TargetKind    string // legacy | managed
	TargetName    string
	ClientID      string
	WebhookID     string
	URL           string
	EventName     string
	Payload       string
	Attempts      int
	NextAttemptAt time.Time
}

type DueDelivery struct {
	Delivery
}

type ApplyResult string

const (
	ApplyApplied   ApplyResult = "applied"
	ApplyDuplicate ApplyResult = "duplicate"
	ApplyConflict  ApplyResult = "conflict"
	ApplyNotFound  ApplyResult = "not_found"
)

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(1)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	s := &Store{db: db, path: path}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Path() string { return s.path }

func (s *Store) migrate() error {
	raw, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		return err
	}
	for _, stmt := range strings.Split(string(raw), ";") {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	return nil
}

// ---------- transactions ----------

func (s *Store) InsertTransaction(t Transaction) error {
	now := time.Now().UTC()
	if t.CreatedAt.IsZero() {
		t.CreatedAt = now
	}
	t.UpdatedAt = now
	var finalAt any
	if t.Category != "" && t.Category != "pending" {
		finalAt = formatTime(now)
	}
	var meta any
	if len(t.Meta) > 0 {
		if b, err := json.Marshal(t.Meta); err == nil {
			meta = string(b)
		}
	}
	_, err := s.db.Exec(`
INSERT INTO transactions
 (ref_id, client_id, product, dest, qty, nominal, category,
  provider_state, provider_code, provider_trxid, sn, request_raw, meta,
  created_at, updated_at, final_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		t.RefID, nullString(t.ClientID), t.Product, t.Dest, nullInt(t.Qty), nullInt(t.Nominal),
		firstNonEmpty(t.Category, "pending"), t.ProviderState, t.ProviderCode, t.ProviderTrxID, t.SN,
		t.RequestRaw, meta, formatTime(t.CreatedAt), formatTime(t.UpdatedAt), finalAt)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return ErrDuplicateRef
		}
		return err
	}
	return nil
}

func (s *Store) GetTransaction(refID string) (*Transaction, error) {
	row := s.db.QueryRow(`
SELECT ref_id, COALESCE(client_id,''), product, dest, qty, nominal, category,
       COALESCE(provider_state,''), COALESCE(provider_code,''), COALESCE(provider_trxid,''),
       COALESCE(sn,''), COALESCE(request_raw,''), COALESCE(report_raw,''), COALESCE(meta,''),
       created_at, updated_at, final_at
FROM transactions WHERE ref_id = ?`, refID)
	t := &Transaction{}
	var qty, nominal sql.NullInt64
	var finalAt sql.NullString
	var metaJSON string
	var created, updated string
	if err := row.Scan(&t.RefID, &t.ClientID, &t.Product, &t.Dest, &qty, &nominal, &t.Category,
		&t.ProviderState, &t.ProviderCode, &t.ProviderTrxID, &t.SN, &t.RequestRaw, &t.ReportRaw, &metaJSON,
		&created, &updated, &finalAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if qty.Valid {
		t.Qty = &qty.Int64
	}
	if nominal.Valid {
		t.Nominal = &nominal.Int64
	}
	t.CreatedAt = parseTime(created)
	t.UpdatedAt = parseTime(updated)
	if finalAt.Valid {
		v := parseTime(finalAt.String)
		t.FinalAt = &v
	}
	if metaJSON != "" {
		_ = json.Unmarshal([]byte(metaJSON), &t.Meta)
	}
	return t, nil
}

func (s *Store) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

// UpdateTransactionResult fills in the provider outcome for a transaction that
// was inserted as pending before the provider call.
func (s *Store) UpdateTransactionResult(refID, category string, qty, nominal *int64,
	state, code, trxid, sn, raw string) (bool, error) {
	now := time.Now().UTC()
	var finalAt any
	if category != "" && category != "pending" {
		finalAt = formatTime(now)
	}
	res, err := s.db.Exec(`
UPDATE transactions
SET category=?, qty=?, nominal=?, provider_state=?, provider_code=?, provider_trxid=?,
    sn=?, request_raw=?, updated_at=?, final_at=COALESCE(final_at, ?)
WHERE ref_id=? AND category='pending'`,
		firstNonEmpty(category, "pending"), nullInt(qty), nullInt(nominal), state, code, trxid,
		sn, raw, formatTime(now), finalAt, refID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// DeletePendingTransaction removes the pre-provider placeholder when the
// provider call fails, so the caller can retry the same refID.
func (s *Store) DeletePendingTransaction(refID string) error {
	_, err := s.db.Exec(`DELETE FROM transactions WHERE ref_id=? AND category='pending'`, refID)
	return err
}

// UpsertTransaction imports a historical transaction. Final status always wins
// over pending; two different final statuses never overwrite each other.
func (s *Store) UpsertTransaction(t Transaction) (bool, error) {
	existing, err := s.GetTransaction(t.RefID)
	if errors.Is(err, ErrNotFound) {
		return true, s.InsertTransaction(t)
	}
	if err != nil {
		return false, err
	}
	now := time.Now().UTC()
	currentFinal := existing.Category == "success" || existing.Category == "failed"
	incomingFinal := t.Category == "success" || t.Category == "failed"
	if currentFinal {
		if t.Category != existing.Category {
			return false, nil
		}
	} else if !incomingFinal && t.Category == "" {
		t.Category = "pending"
	}
	category := t.Category
	if category == "" {
		category = existing.Category
	}
	var meta any
	if len(t.Meta) > 0 {
		if b, err := json.Marshal(t.Meta); err == nil {
			meta = string(b)
		}
	}
	var finalAt any
	if category != "pending" {
		finalAt = formatTime(now)
	}
	_, err = s.db.Exec(`
UPDATE transactions SET
 category=?, client_id=COALESCE(NULLIF(?,''), client_id),
 product=COALESCE(NULLIF(?,''), product), dest=COALESCE(NULLIF(?,''), dest),
 qty=COALESCE(?, qty), nominal=COALESCE(?, nominal),
 provider_state=COALESCE(NULLIF(?,''), provider_state),
 provider_code=COALESCE(NULLIF(?,''), provider_code),
 provider_trxid=COALESCE(NULLIF(?,''), provider_trxid),
 sn=COALESCE(NULLIF(?,''), sn),
 request_raw=COALESCE(NULLIF(request_raw,''), NULLIF(?,'')),
 meta=COALESCE(?, meta), updated_at=?,
 final_at=CASE WHEN ?='pending' THEN final_at ELSE COALESCE(final_at, ?) END
WHERE ref_id=?`,
		category, t.ClientID, t.Product, t.Dest, nullInt(t.Qty), nullInt(t.Nominal),
		t.ProviderState, t.ProviderCode, t.ProviderTrxID, t.SN, t.RequestRaw, meta,
		formatTime(now), category, finalAt, t.RefID)
	return false, err
}

func (s *Store) ApplyReport(refID, category, state, code, trxid, sn, raw string) (ApplyResult, error) {
	now := time.Now().UTC()
	var finalAt any
	if category != "pending" {
		finalAt = formatTime(now)
	}
	res, err := s.db.Exec(`
UPDATE transactions
SET category = ?, provider_state = ?, provider_code = ?, provider_trxid = ?, sn = ?,
    report_raw = ?, updated_at = ?, final_at = COALESCE(final_at, ?)
WHERE ref_id = ? AND category = 'pending'`,
		category, state, code, trxid, sn, raw, formatTime(now), finalAt, refID)
	if err != nil {
		return "", err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return ApplyApplied, nil
	}
	var current string
	err = s.db.QueryRow(`SELECT category FROM transactions WHERE ref_id = ?`, refID).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		return ApplyNotFound, nil
	}
	if err != nil {
		return "", err
	}
	if current == category {
		return ApplyDuplicate, nil
	}
	return ApplyConflict, nil
}

func (s *Store) InsertProviderReport(r ProviderReport) error {
	if r.ReceivedAt.IsZero() {
		r.ReceivedAt = time.Now().UTC()
	}
	accepted := 0
	if r.Accepted {
		accepted = 1
	}
	_, err := s.db.Exec(`
INSERT INTO provider_reports
 (ref_id, accepted, reason, category, remote_ip, method, query, body, raw_message, received_at)
VALUES (?,?,?,?,?,?,?,?,?,?)`,
		nullString(r.RefID), accepted, r.Reason, r.Category, r.RemoteIP, r.Method,
		r.Query, r.Body, r.RawMessage, formatTime(r.ReceivedAt))
	return err
}

// ---------- operations ----------

type Stats struct {
	TransactionsByCategory map[string]int64 `json:"transactionsByCategory"`
	StaleIntents           int64            `json:"staleIntents"`
	Events                 int64            `json:"events"`
	Deliveries             map[string]int64 `json:"deliveries"`
	ProviderReports24h     int64            `json:"providerReports24h"`
	RejectedReports24h     int64            `json:"rejectedReports24h"`
	Clients                int64            `json:"clients"`
	Webhooks               int64            `json:"webhooks"`
	DBSizeBytes            int64            `json:"dbSizeBytes"`
}

func (s *Store) Stats(ctx context.Context) (Stats, error) {
	out := Stats{TransactionsByCategory: map[string]int64{}, Deliveries: map[string]int64{}}
	rows, err := s.db.QueryContext(ctx, `SELECT category, COUNT(1) FROM transactions GROUP BY category`)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var cat string
		var n int64
		if err := rows.Scan(&cat, &n); err != nil {
			rows.Close()
			return out, err
		}
		out.TransactionsByCategory[cat] = n
	}
	rows.Close()
	if out.StaleIntents, err = s.count(ctx, `SELECT COUNT(1) FROM transactions
		WHERE category='pending' AND COALESCE(provider_state,'')='' AND created_at < ?`,
		formatTime(time.Now().UTC().Add(-15*time.Minute))); err != nil {
		return out, err
	}
	if out.Events, err = s.count(ctx, `SELECT COUNT(1) FROM events`); err != nil {
		return out, err
	}
	rows, err = s.db.QueryContext(ctx, `SELECT status, COUNT(1) FROM deliveries GROUP BY status`)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var status string
		var n int64
		if err := rows.Scan(&status, &n); err != nil {
			rows.Close()
			return out, err
		}
		out.Deliveries[status] = n
	}
	rows.Close()
	since := formatTime(time.Now().UTC().Add(-24 * time.Hour))
	if out.ProviderReports24h, err = s.count(ctx, `SELECT COUNT(1) FROM provider_reports WHERE received_at >= ?`, since); err != nil {
		return out, err
	}
	if out.RejectedReports24h, err = s.count(ctx, `SELECT COUNT(1) FROM provider_reports WHERE received_at >= ? AND accepted=0`, since); err != nil {
		return out, err
	}
	if out.Clients, err = s.count(ctx, `SELECT COUNT(1) FROM clients WHERE status='ACTIVE'`); err != nil {
		return out, err
	}
	if out.Webhooks, err = s.count(ctx, `SELECT COUNT(1) FROM webhooks WHERE status='ACTIVE'`); err != nil {
		return out, err
	}
	if info, err := os.Stat(s.path); err == nil {
		out.DBSizeBytes = info.Size()
	}
	return out, nil
}

func (s *Store) count(ctx context.Context, query string, args ...any) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx, query, args...).Scan(&n)
	return n, err
}

type PruneResult struct {
	Deliveries      int64 `json:"deliveries"`
	Events          int64 `json:"events"`
	ProviderReports int64 `json:"providerReports"`
}

// Prune removes delivered/dead outbox rows and old provider audit rows. It
// never touches transactions: they are the financial ledger.
func (s *Store) Prune(ctx context.Context, olderThan time.Duration) (PruneResult, error) {
	var out PruneResult
	cutoff := formatTime(time.Now().UTC().Add(-olderThan))
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx,
		`DELETE FROM deliveries WHERE status IN ('delivered','dead') AND created_at < ?`, cutoff)
	if err != nil {
		return out, err
	}
	out.Deliveries, _ = res.RowsAffected()

	res, err = tx.ExecContext(ctx, `DELETE FROM events WHERE created_at < ?
		AND NOT EXISTS (SELECT 1 FROM deliveries WHERE deliveries.event_id = events.id)`, cutoff)
	if err != nil {
		return out, err
	}
	out.Events, _ = res.RowsAffected()

	res, err = tx.ExecContext(ctx, `DELETE FROM provider_reports WHERE received_at < ?`, cutoff)
	if err != nil {
		return out, err
	}
	out.ProviderReports, _ = res.RowsAffected()

	if err := tx.Commit(); err != nil {
		return out, err
	}
	if err := s.Checkpoint(ctx); err != nil {
		return out, err
	}
	return out, nil
}

// Checkpoint keeps the WAL file from growing after large deletions.
func (s *Store) Checkpoint(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`)
	return err
}

// ---------- outbox ----------

func (s *Store) InsertEvent(refID, kind, dedupeKey, payload string) (int64, bool, error) {
	now := formatTime(time.Now().UTC())
	res, err := s.db.Exec(`
INSERT INTO events (ref_id, kind, dedupe_key, payload, created_at)
VALUES (?,?,?,?,?) ON CONFLICT(dedupe_key) DO NOTHING`,
		nullString(refID), kind, dedupeKey, payload, now)
	if err != nil {
		return 0, false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return 0, false, nil
	}
	id, err := res.LastInsertId()
	return id, true, err
}

func (s *Store) InsertEventAndDeliveries(refID, kind, dedupeKey, payload string, items []Delivery) (int64, bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback()
	now := formatTime(time.Now().UTC())
	res, err := tx.Exec(`
INSERT INTO events (ref_id, kind, dedupe_key, payload, created_at)
VALUES (?,?,?,?,?) ON CONFLICT(dedupe_key) DO NOTHING`,
		nullString(refID), kind, dedupeKey, payload, now)
	if err != nil {
		return 0, false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return 0, false, nil
	}
	eventID, err := res.LastInsertId()
	if err != nil {
		return 0, false, err
	}
	for _, d := range items {
		next := d.NextAttemptAt
		if next.IsZero() {
			next = time.Now().UTC()
		}
		if _, err := tx.Exec(`
INSERT INTO deliveries
 (event_id, target_kind, target_name, client_id, webhook_id, url, event_name,
  status, attempts, next_attempt_at, created_at)
VALUES (?,?,?,?,?,?,?,'pending',0,?,?)`,
			eventID, d.TargetKind, d.TargetName, nullString(d.ClientID), nullString(d.WebhookID),
			d.URL, d.EventName, formatTime(next), now); err != nil {
			return 0, false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, false, err
	}
	return eventID, true, nil
}

func (s *Store) EnqueueDeliveries(items []Delivery) error {
	if len(items) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := formatTime(time.Now().UTC())
	for _, d := range items {
		next := d.NextAttemptAt
		if next.IsZero() {
			next = time.Now().UTC()
		}
		if _, err := tx.Exec(`
INSERT INTO deliveries
 (event_id, target_kind, target_name, client_id, webhook_id, url, event_name,
  status, attempts, next_attempt_at, created_at)
VALUES (?,?,?,?,?,?,?,'pending',0,?,?)`,
			d.EventID, d.TargetKind, d.TargetName, nullString(d.ClientID), nullString(d.WebhookID),
			d.URL, d.EventName, formatTime(next), now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// DueDeliveries returns pending deliveries whose retry time has arrived.
func (s *Store) DueDeliveries(limit int) ([]DueDelivery, error) {
	rows, err := s.db.Query(`
SELECT d.id, d.event_id, d.target_kind, d.target_name, COALESCE(d.client_id,''),
       COALESCE(d.webhook_id,''), d.url, d.event_name, d.attempts, e.payload
FROM deliveries d
JOIN events e ON e.id = d.event_id
WHERE d.status = 'pending' AND d.next_attempt_at <= ?
ORDER BY d.id LIMIT ?`, formatTime(time.Now().UTC()), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DueDelivery
	for rows.Next() {
		var d DueDelivery
		if err := rows.Scan(&d.ID, &d.EventID, &d.TargetKind, &d.TargetName, &d.ClientID,
			&d.WebhookID, &d.URL, &d.EventName, &d.Attempts, &d.Payload); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) MarkDelivering(id int64) error {
	_, err := s.db.Exec(`UPDATE deliveries SET status='sending' WHERE id=? AND status='pending'`, id)
	return err
}

func (s *Store) MarkDelivered(id int64, statusCode int) error {
	now := formatTime(time.Now().UTC())
	_, err := s.db.Exec(`UPDATE deliveries SET status='delivered', response_status=?, delivered_at=? WHERE id=?`,
		statusCode, now, id)
	return err
}

func (s *Store) MarkFailed(id int64, errMsg string, next time.Time, maxAttempts int) (dead bool, err error) {
	var attempts int
	if err := s.db.QueryRow(`SELECT attempts FROM deliveries WHERE id=?`, id).Scan(&attempts); err != nil {
		return false, err
	}
	attempts++
	status := "pending"
	if attempts >= maxAttempts {
		status = "dead"
		dead = true
	}
	_, err = s.db.Exec(`UPDATE deliveries SET attempts=?, status=?, last_error=?, next_attempt_at=? WHERE id=?`,
		attempts, status, errMsg, formatTime(next), id)
	return dead, err
}

// ReclaimStale returns deliveries stuck in 'sending' (crash recovery).
func (s *Store) ReclaimStale(olderThan time.Duration) error {
	cutoff := formatTime(time.Now().UTC().Add(-olderThan))
	_, err := s.db.Exec(`UPDATE deliveries SET status='pending' WHERE status='sending' AND next_attempt_at <= ?`, cutoff)
	return err
}

// ---------- clients & webhooks ----------

type CreatedClient struct {
	Client         Client
	Webhook        Webhook
	Secret         string
	PIN            string
	Keys           string
	SecretCallback string
}

func (s *Store) CreateClientWithWebhook(name, url string, events []string) (*CreatedClient, error) {
	now := time.Now().UTC()
	clientID := security.GenerateID("avn_cl")
	secret := "avn_sec_" + security.GenerateToken(28, security.NoAmbiguousMixed)
	pin := "avn_pin_" + security.GenerateToken(10, security.NoAmbiguousMixed)
	keys := "avn_key_" + security.GenerateToken(24, security.NoAmbiguousMixed)
	callback := security.GenerateToken(126, security.NoAmbiguousMixed)
	webhookID := security.GenerateID("avn_wh")

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`
INSERT INTO clients
 (client_id, client_name, status, registered_at, secret_hash, pin_hash, keys_hash,
  secret_callback, secret_callback_rotated_at)
VALUES (?,?,'ACTIVE',?,?,?,?,?,?)`,
		clientID, name, formatTime(now),
		security.SHA256Base64(secret), security.SHA256Base64(pin), security.SHA256Base64(keys),
		callback, formatTime(now)); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`
INSERT INTO webhooks (webhook_id, client_id, url, status, created_at, updated_at)
VALUES (?,?,?,'ACTIVE',?,?)`, webhookID, clientID, url, formatTime(now), formatTime(now)); err != nil {
		return nil, err
	}
	for _, ev := range events {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO webhook_events (webhook_id, event) VALUES (?,?)`, webhookID, ev); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return &CreatedClient{
		Client: Client{
			ClientID: clientID, ClientName: name, Status: "ACTIVE", RegisteredAt: now,
			SecretHash: security.SHA256Base64(secret), PINHash: security.SHA256Base64(pin),
			KeysHash: security.SHA256Base64(keys), SecretCallback: callback,
			SecretCallbackRotatedAt: now,
		},
		Webhook: Webhook{
			WebhookID: webhookID, ClientID: clientID, ClientName: name, URL: url,
			Status: "ACTIVE", Events: events, CreatedAt: now, UpdatedAt: now,
		},
		Secret: secret, PIN: pin, Keys: keys, SecretCallback: callback,
	}, nil
}

func (s *Store) ListClients() ([]PublicClient, error) {
	rows, err := s.db.Query(`
SELECT client_id, client_name, status, registered_at
FROM clients WHERE status='ACTIVE' ORDER BY registered_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PublicClient
	for rows.Next() {
		var c PublicClient
		var reg string
		if err := rows.Scan(&c.ClientID, &c.ClientName, &c.Status, &reg); err != nil {
			return nil, err
		}
		c.RegisteredAt = parseTime(reg)
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) ListWebhooks() ([]Webhook, error) {
	rows, err := s.db.Query(`
SELECT w.webhook_id, w.client_id, COALESCE(c.client_name,''), w.url, w.created_at
FROM webhooks w JOIN clients c ON c.client_id = w.client_id
WHERE w.status='ACTIVE' ORDER BY w.created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Webhook
	for rows.Next() {
		var w Webhook
		var created string
		if err := rows.Scan(&w.WebhookID, &w.ClientID, &w.ClientName, &w.URL, &created); err != nil {
			return nil, err
		}
		w.Status = "ACTIVE"
		w.CreatedAt = parseTime(created)
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		ev, err := s.webhookEvents(out[i].WebhookID)
		if err != nil {
			return nil, err
		}
		out[i].Events = ev
	}
	return out, nil
}

func (s *Store) webhookEvents(webhookID string) ([]string, error) {
	rows, err := s.db.Query(`SELECT event FROM webhook_events WHERE webhook_id=? ORDER BY event`, webhookID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ev []string
	for rows.Next() {
		var e string
		if err := rows.Scan(&e); err != nil {
			return nil, err
		}
		ev = append(ev, e)
	}
	return ev, rows.Err()
}

func (s *Store) DeleteClient(clientID string) error {
	now := formatTime(time.Now().UTC())
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE clients SET status='DELETED', deleted_at=? WHERE client_id=? AND status='ACTIVE'`, now, clientID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if _, err := tx.Exec(`UPDATE webhooks SET status='DELETED', deleted_at=? WHERE client_id=?`, now, clientID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE deliveries SET status='dead', last_error='client deleted'
		WHERE client_id=? AND status IN ('pending','sending')`, clientID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) FindClientByCredentials(secret, pin, keys string) (*Client, error) {
	rows, err := s.db.Query(`
SELECT client_id, client_name, status, registered_at, secret_hash, pin_hash, keys_hash,
       secret_callback, secret_callback_rotated_at
FROM clients WHERE status='ACTIVE'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	sHash := security.SHA256Base64(secret)
	pHash := security.SHA256Base64(pin)
	kHash := security.SHA256Base64(keys)
	var found *Client
	for rows.Next() {
		c := Client{}
		var reg, rotated string
		if err := rows.Scan(&c.ClientID, &c.ClientName, &c.Status, &reg, &c.SecretHash,
			&c.PINHash, &c.KeysHash, &c.SecretCallback, &rotated); err != nil {
			return nil, err
		}
		if security.ConstantTimeEqual(c.SecretHash, sHash) &&
			security.ConstantTimeEqual(c.PINHash, pHash) &&
			security.ConstantTimeEqual(c.KeysHash, kHash) {
			c.RegisteredAt = parseTime(reg)
			c.SecretCallbackRotatedAt = parseTime(rotated)
			found = &c
			break
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if found == nil {
		return nil, ErrNotFound
	}
	return found, nil
}

func (s *Store) GetClient(clientID string) (*Client, error) {
	c := &Client{}
	var reg, rotated string
	err := s.db.QueryRow(`
SELECT client_id, client_name, status, registered_at, secret_hash, pin_hash, keys_hash,
       secret_callback, secret_callback_rotated_at
FROM clients WHERE client_id=?`, clientID).Scan(&c.ClientID, &c.ClientName, &c.Status, &reg,
		&c.SecretHash, &c.PINHash, &c.KeysHash, &c.SecretCallback, &rotated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	c.RegisteredAt = parseTime(reg)
	c.SecretCallbackRotatedAt = parseTime(rotated)
	return c, nil
}

func (s *Store) RotateSecretCallback(clientID string) (string, error) {
	value := security.GenerateToken(126, security.NoAmbiguousMixed)
	res, err := s.db.Exec(`UPDATE clients SET secret_callback=?, secret_callback_rotated_at=? WHERE client_id=? AND status='ACTIVE'`,
		value, formatTime(time.Now().UTC()), clientID)
	if err != nil {
		return "", err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", ErrNotFound
	}
	return value, nil
}

func (s *Store) UpdateWebhook(clientID, webhookID, url string, events []string, setEvents bool) (*Webhook, error) {
	w, err := s.GetWebhook(webhookID)
	if err != nil {
		return nil, err
	}
	if w.ClientID != clientID {
		return nil, ErrForbidden
	}
	if w.Status != "ACTIVE" {
		return nil, ErrNotActive
	}
	return s.updateWebhookFields(w, url, events, setEvents)
}

func (s *Store) updateWebhookFields(w *Webhook, url string, events []string, setEvents bool) (*Webhook, error) {
	if url != "" {
		w.URL = url
	}
	now := time.Now().UTC()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE webhooks SET url=?, updated_at=? WHERE webhook_id=?`,
		w.URL, formatTime(now), w.WebhookID); err != nil {
		return nil, err
	}
	if setEvents {
		if _, err := tx.Exec(`DELETE FROM webhook_events WHERE webhook_id=?`, w.WebhookID); err != nil {
			return nil, err
		}
		for _, ev := range events {
			if _, err := tx.Exec(`INSERT OR IGNORE INTO webhook_events (webhook_id, event) VALUES (?,?)`, w.WebhookID, ev); err != nil {
				return nil, err
			}
		}
		w.Events = events
	} else {
		rows, err := tx.Query(`SELECT event FROM webhook_events WHERE webhook_id=? ORDER BY event`, w.WebhookID)
		if err != nil {
			return nil, err
		}
		w.Events = nil
		for rows.Next() {
			var ev string
			if err := rows.Scan(&ev); err != nil {
				rows.Close()
				return nil, err
			}
			w.Events = append(w.Events, ev)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	w.UpdatedAt = now
	return w, nil
}

// WebhooksForEvent returns active webhooks of a fresh client that subscribe to
// eventName, including wildcard subscriptions such as "invoice.*".
func (s *Store) WebhooksForEvent(clientID, eventName string) (*Client, []Webhook, error) {
	client, err := s.GetClient(clientID)
	if err != nil {
		return nil, nil, err
	}
	if client.Status != "ACTIVE" {
		return nil, nil, ErrNotFound
	}
	rows, err := s.db.Query(`
SELECT w.webhook_id, w.client_id, w.url, e.event
FROM webhooks w LEFT JOIN webhook_events e ON e.webhook_id = w.webhook_id
WHERE w.client_id=? AND w.status='ACTIVE'`, clientID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	byID := map[string]*Webhook{}
	order := []string{}
	for rows.Next() {
		var id, cid, url string
		var ev sql.NullString
		if err := rows.Scan(&id, &cid, &url, &ev); err != nil {
			return nil, nil, err
		}
		w, ok := byID[id]
		if !ok {
			w = &Webhook{WebhookID: id, ClientID: cid, URL: url, Status: "ACTIVE", ClientName: client.ClientName}
			byID[id] = w
			order = append(order, id)
		}
		if ev.Valid {
			w.Events = append(w.Events, ev.String)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	var out []Webhook
	for _, id := range order {
		w := byID[id]
		for _, ev := range w.Events {
			if MatchEvent(ev, eventName) {
				out = append(out, *w)
				break
			}
		}
	}
	return client, out, nil
}

func MatchEvent(subscribed, eventName string) bool {
	if subscribed == "" {
		return false
	}
	if subscribed == eventName {
		return true
	}
	if strings.HasSuffix(subscribed, ".*") {
		return strings.HasPrefix(eventName, strings.TrimSuffix(subscribed, ".*")+".")
	}
	return false
}

// ---------- paginated / admin API ----------

func (s *Store) ListClientsPage(limit, offset int) ([]PublicClient, int64, error) {
	var total int64
	if err := s.db.QueryRow(`SELECT COUNT(1) FROM clients WHERE status='ACTIVE'`).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.Query(`
SELECT client_id, client_name, status, registered_at
FROM clients WHERE status='ACTIVE' ORDER BY registered_at LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []PublicClient
	for rows.Next() {
		var c PublicClient
		var reg string
		if err := rows.Scan(&c.ClientID, &c.ClientName, &c.Status, &reg); err != nil {
			return nil, 0, err
		}
		c.RegisteredAt = parseTime(reg)
		out = append(out, c)
	}
	return out, total, rows.Err()
}

func (s *Store) ListWebhooksPage(limit, offset int) ([]Webhook, int64, error) {
	var total int64
	if err := s.db.QueryRow(`SELECT COUNT(1) FROM webhooks WHERE status='ACTIVE'`).Scan(&total); err != nil {
		return nil, 0, err
	}
	hooks, err := s.listWebhooksLimit(limit, offset)
	return hooks, total, err
}

func (s *Store) listWebhooksLimit(limit, offset int) ([]Webhook, error) {
	rows, err := s.db.Query(`
SELECT w.webhook_id, w.client_id, COALESCE(c.client_name,''), w.url, w.status,
       w.created_at, w.updated_at
FROM webhooks w JOIN clients c ON c.client_id = w.client_id
WHERE w.status='ACTIVE' ORDER BY w.created_at LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Webhook
	for rows.Next() {
		var w Webhook
		var created, updated string
		if err := rows.Scan(&w.WebhookID, &w.ClientID, &w.ClientName, &w.URL, &w.Status,
			&created, &updated); err != nil {
			return nil, err
		}
		w.CreatedAt = parseTime(created)
		w.UpdatedAt = parseTime(updated)
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		ev, err := s.webhookEvents(out[i].WebhookID)
		if err != nil {
			return nil, err
		}
		out[i].Events = ev
	}
	return out, nil
}

func (s *Store) ListWebhooksByClient(clientID string) ([]Webhook, error) {
	rows, err := s.db.Query(`
SELECT w.webhook_id, w.client_id, COALESCE(c.client_name,''), w.url, w.status,
       w.created_at, w.updated_at
FROM webhooks w JOIN clients c ON c.client_id = w.client_id
WHERE w.client_id=? AND w.status='ACTIVE' ORDER BY w.created_at`, clientID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Webhook
	for rows.Next() {
		var w Webhook
		var created, updated string
		if err := rows.Scan(&w.WebhookID, &w.ClientID, &w.ClientName, &w.URL, &w.Status,
			&created, &updated); err != nil {
			return nil, err
		}
		w.CreatedAt = parseTime(created)
		w.UpdatedAt = parseTime(updated)
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		ev, err := s.webhookEvents(out[i].WebhookID)
		if err != nil {
			return nil, err
		}
		out[i].Events = ev
	}
	return out, nil
}

func (s *Store) GetWebhook(webhookID string) (*Webhook, error) {
	w := &Webhook{}
	var created, updated string
	err := s.db.QueryRow(`
SELECT w.webhook_id, w.client_id, COALESCE(c.client_name,''), w.url, w.status,
       w.created_at, w.updated_at
FROM webhooks w JOIN clients c ON c.client_id = w.client_id
WHERE w.webhook_id=? AND w.status='ACTIVE'`, webhookID).Scan(&w.WebhookID, &w.ClientID, &w.ClientName, &w.URL,
		&w.Status, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	w.CreatedAt = parseTime(created)
	w.UpdatedAt = parseTime(updated)
	w.Events, err = s.webhookEvents(webhookID)
	return w, err
}

func (s *Store) CreateClient(name string) (*CreatedClient, error) {
	now := time.Now().UTC()
	clientID := security.GenerateID("avn_cl")
	secret := "avn_sec_" + security.GenerateToken(28, security.NoAmbiguousMixed)
	pin := "avn_pin_" + security.GenerateToken(10, security.NoAmbiguousMixed)
	keys := "avn_key_" + security.GenerateToken(24, security.NoAmbiguousMixed)
	callback := security.GenerateToken(126, security.NoAmbiguousMixed)
	if _, err := s.db.Exec(`
INSERT INTO clients
 (client_id, client_name, status, registered_at, secret_hash, pin_hash, keys_hash,
  secret_callback, secret_callback_rotated_at)
VALUES (?,?,'ACTIVE',?,?,?,?,?,?)`,
		clientID, name, formatTime(now), security.SHA256Base64(secret),
		security.SHA256Base64(pin), security.SHA256Base64(keys), callback, formatTime(now)); err != nil {
		return nil, err
	}
	return &CreatedClient{
		Client: Client{
			ClientID: clientID, ClientName: name, Status: "ACTIVE", RegisteredAt: now,
			SecretHash: security.SHA256Base64(secret), PINHash: security.SHA256Base64(pin),
			KeysHash: security.SHA256Base64(keys), SecretCallback: callback,
			SecretCallbackRotatedAt: now,
		},
		Secret: secret, PIN: pin, Keys: keys, SecretCallback: callback,
	}, nil
}

func (s *Store) CreateWebhook(clientID, url string, events []string) (*Webhook, error) {
	client, err := s.GetClient(clientID)
	if err != nil {
		return nil, err
	}
	if client.Status != "ACTIVE" {
		return nil, ErrNotActive
	}
	now := time.Now().UTC()
	webhookID := security.GenerateID("avn_wh")
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`
INSERT INTO webhooks (webhook_id, client_id, url, status, created_at, updated_at)
VALUES (?,?,?,'ACTIVE',?,?)`, webhookID, clientID, url, formatTime(now), formatTime(now)); err != nil {
		return nil, err
	}
	for _, ev := range events {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO webhook_events (webhook_id, event) VALUES (?,?)`, webhookID, ev); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &Webhook{
		WebhookID: webhookID, ClientID: clientID, ClientName: client.ClientName,
		URL: url, Status: "ACTIVE", Events: events, CreatedAt: now, UpdatedAt: now,
	}, nil
}

func (s *Store) DeleteWebhook(webhookID string) error {
	now := formatTime(time.Now().UTC())
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE webhooks SET status='DELETED', deleted_at=? WHERE webhook_id=? AND status='ACTIVE'`, now, webhookID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if _, err := tx.Exec(`UPDATE deliveries SET status='dead', last_error='webhook deleted'
		WHERE webhook_id=? AND status IN ('pending','sending')`, webhookID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) UpdateWebhookAdmin(webhookID, url string, events []string, setEvents bool) (*Webhook, error) {
	w, err := s.GetWebhook(webhookID)
	if err != nil {
		return nil, err
	}
	if w.Status != "ACTIVE" {
		return nil, ErrNotActive
	}
	return s.updateWebhookFields(w, url, events, setEvents)
}

// ---------- legacy import ----------

type legacyDB struct {
	Clients []struct {
		ClientID              string `json:"client_id"`
		ClientName            string `json:"client_name"`
		Status                string `json:"status"`
		RegisteredAt          string `json:"registered_at"`
		SecretHash            string `json:"secret_hash"`
		PINHash               string `json:"pin_hash"`
		KeysHash              string `json:"keys_hash"`
		SecretCallback        string `json:"secret_callback"`
		SecretCallbackRotated string `json:"secret_callback_rotated_at"`
	} `json:"clients"`
	Webhooks []struct {
		WebhookID string   `json:"webhook_id"`
		ClientID  string   `json:"client_id"`
		URL       string   `json:"url"`
		Status    string   `json:"status"`
		CreatedAt string   `json:"created_at"`
		Events    []string `json:"events"`
	} `json:"webhooks"`
}

type ImportResult struct {
	Clients  int `json:"clients"`
	Webhooks int `json:"webhooks"`
	Skipped  int `json:"skipped"`
}

func (s *Store) ImportLegacyJSON(path string) (ImportResult, error) {
	var out ImportResult
	raw, err := os.ReadFile(path)
	if err != nil {
		return out, err
	}
	var legacy legacyDB
	if err := json.Unmarshal(raw, &legacy); err != nil {
		return out, fmt.Errorf("parse legacy db: %w", err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	for _, c := range legacy.Clients {
		if c.ClientID == "" || c.SecretHash == "" {
			out.Skipped++
			continue
		}
		reg := c.RegisteredAt
		if reg == "" {
			reg = formatTime(time.Now().UTC())
		}
		rotated := c.SecretCallbackRotated
		if rotated == "" {
			rotated = reg
		}
		res, err := tx.Exec(`
INSERT INTO clients
 (client_id, client_name, status, registered_at, secret_hash, pin_hash, keys_hash,
  secret_callback, secret_callback_rotated_at, deleted_at)
VALUES (?,?,?,?,?,?,?,?,?,NULL) ON CONFLICT(client_id) DO NOTHING`,
			c.ClientID, firstNonEmpty(c.ClientName, "client"), firstNonEmpty(c.Status, "ACTIVE"),
			reg, c.SecretHash, c.PINHash, c.KeysHash, c.SecretCallback, rotated)
		if err != nil {
			return out, err
		}
		n, _ := res.RowsAffected()
		out.Clients += int(n)
	}
	for _, w := range legacy.Webhooks {
		if w.WebhookID == "" || w.ClientID == "" || w.URL == "" {
			out.Skipped++
			continue
		}
		var exists int
		if err := tx.QueryRow(`SELECT COUNT(1) FROM clients WHERE client_id=?`, w.ClientID).Scan(&exists); err != nil {
			return out, err
		}
		if exists == 0 {
			out.Skipped++
			continue
		}
		created := w.CreatedAt
		if created == "" {
			created = formatTime(time.Now().UTC())
		}
		res, err := tx.Exec(`
INSERT INTO webhooks (webhook_id, client_id, url, status, created_at, updated_at)
VALUES (?,?,?,?,?,?) ON CONFLICT(webhook_id) DO NOTHING`,
			w.WebhookID, w.ClientID, w.URL, firstNonEmpty(w.Status, "ACTIVE"), created, created)
		if err != nil {
			return out, err
		}
		n, _ := res.RowsAffected()
		out.Webhooks += int(n)
		for _, ev := range w.Events {
			if _, err := tx.Exec(`INSERT OR IGNORE INTO webhook_events (webhook_id, event) VALUES (?,?)`, w.WebhookID, ev); err != nil {
				return out, err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return out, err
	}
	return out, nil
}

// ---------- helpers ----------

func formatTime(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000000000Z")
}

func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullInt(v *int64) any {
	if v == nil {
		return nil
	}
	return *v
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}
