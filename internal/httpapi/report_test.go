package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Matsumiko/auvyn-apies-go/internal/config"
	"github.com/Matsumiko/auvyn-apies-go/internal/ipcenter"
	"github.com/Matsumiko/auvyn-apies-go/internal/notify"
	"github.com/Matsumiko/auvyn-apies-go/internal/store"
)

func testConfig() *config.Config {
	return &config.Config{
		AuvynSecret:            "test-secret",
		AdminSecret:            "admin",
		AdminPIN:               "pin",
		AdminKeys:              "keys",
		CallbackTargets:        []config.CallbackTarget{{Name: "default", URL: "https://example.com/cb", Secret: "cb"}},
		ManagedWebhooksEnabled: true,
		CallbackTimeout:        time.Second,
		DeliveryMaxAttempts:    2,
		DeliveryBackoff:        []time.Duration{0},
		LogLevel:               "error",
	}
}

func newTestServer(t *testing.T, cfg *config.Config) (*Server, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	tg := notify.New(cfg, log)
	return New(cfg, st, nil, nil, tg, log), st
}

func seedTransaction(t *testing.T, st *store.Store, refID, dest string) {
	t.Helper()
	if err := st.InsertTransaction(store.Transaction{
		RefID: refID, Product: "PULSA10", Dest: dest, Category: "pending",
		Meta: map[string]any{"client_id": "client-1", "orderId": "INV-1"},
	}); err != nil {
		t.Fatal(err)
	}
}

func reportRequest(query url.Values) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/report?"+query.Encode(), nil)
	req.RemoteAddr = "127.0.0.1:1234"
	return req
}

func TestReportAppliesSuccessAndIsIdempotent(t *testing.T) {
	cfg := testConfig()
	srv, st := newTestServer(t, cfg)
	seedTransaction(t, st, "REF1", "081234")

	msg := "R1.081234 SUKSES. SN/Ref: SN-ABC. M-Bal: 10.000 - 1.000 = 9.000"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, reportRequest(url.Values{
		"refid": {"REF1"}, "status": {"20"}, "message": {msg},
	}))
	if rec.Code != http.StatusOK || rec.Body.String() != "OK" {
		t.Fatalf("report status=%d body=%q", rec.Code, rec.Body.String())
	}
	txn, _ := st.GetTransaction("REF1")
	if txn.Category != "success" {
		t.Fatalf("category = %s", txn.Category)
	}
	if strings.Contains(txn.ReportRaw, "SN-ABC") {
		// report_raw stores the raw provider message intentionally for audit,
		// but it must never be exposed without redaction.
		t.Log("raw audit row contains SN (expected, not exposed)")
	}
	due, _ := st.DueDeliveries(10)
	if len(due) != 1 || due[0].EventName != "transaction.report" {
		t.Fatalf("expected one legacy report delivery, got %+v", due)
	}
	if !strings.Contains(due[0].Payload, `"sn":"SN-ABC"`) {
		t.Errorf("callback payload missing SN: %s", due[0].Payload)
	}
	if strings.Contains(due[0].Payload, "10.000") || strings.Contains(due[0].Payload, "9.000") {
		t.Errorf("callback payload leaked balance: %s", due[0].Payload)
	}

	// Repeat the same report: no second event/delivery.
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, reportRequest(url.Values{
		"refid": {"REF1"}, "status": {"20"}, "message": {msg},
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("duplicate report status=%d", rec.Code)
	}
	due, _ = st.DueDeliveries(10)
	if len(due) != 1 {
		t.Fatalf("duplicate report created extra delivery: %d", len(due))
	}
}

func TestReportConflictKeepsFirstFinalStatus(t *testing.T) {
	cfg := testConfig()
	srv, st := newTestServer(t, cfg)
	seedTransaction(t, st, "REF2", "081234")

	srv.Handler().ServeHTTP(httptest.NewRecorder(), reportRequest(url.Values{
		"refid": {"REF2"}, "status": {"20"}, "message": {"R1.081234 SUKSES. SN/Ref: A. M-Bal: 1 - 1 = 0"},
	}))
	srv.Handler().ServeHTTP(httptest.NewRecorder(), reportRequest(url.Values{
		"refid": {"REF2"}, "status": {"20"}, "message": {"R1.081234 GAGAL. Nomor tujuan salah."},
	}))
	txn, _ := st.GetTransaction("REF2")
	if txn.Category != "success" {
		t.Fatalf("conflict overwrote status: %s", txn.Category)
	}
	due, _ := st.DueDeliveries(10)
	if len(due) != 1 {
		t.Fatalf("conflict emitted extra event: %d", len(due))
	}
}

func TestReportFailureWinsOverStatus20(t *testing.T) {
	cfg := testConfig()
	srv, st := newTestServer(t, cfg)
	seedTransaction(t, st, "REF3", "081234")
	srv.Handler().ServeHTTP(httptest.NewRecorder(), reportRequest(url.Values{
		"refid": {"REF3"}, "status": {"20"}, "message": {"R1.081234 GAGAL. Nomor tujuan salah."},
	}))
	txn, _ := st.GetTransaction("REF3")
	if txn.Category != "failed" {
		t.Fatalf("category = %s, want failed", txn.Category)
	}
}

func TestReportUnknownRefIsAcceptedQuietly(t *testing.T) {
	cfg := testConfig()
	srv, st := newTestServer(t, cfg)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, reportRequest(url.Values{
		"refid": {"NOPE"}, "status": {"20"}, "message": {"R1.081 SUKSES. SN/Ref: A. M-Bal: 1"},
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	due, _ := st.DueDeliveries(10)
	if len(due) != 0 {
		t.Errorf("unknown ref produced a delivery")
	}
}

func TestReportFieldFormat(t *testing.T) {
	cfg := testConfig()
	srv, st := newTestServer(t, cfg)
	seedTransaction(t, st, "REF-FIELD", "081234567890")

	msg := "#123456789 GAME100.081234567890 SUKSES. SN/Ref: SN-ABC123. Saldo 100.000 - 25.000 = 75.000 @22/02 09:46:48"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, reportRequest(url.Values{
		"refid": {"REF-FIELD"}, "status": {"20"}, "message": {msg},
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	txn, _ := st.GetTransaction("REF-FIELD")
	if txn.Category != "success" {
		t.Fatalf("category = %s", txn.Category)
	}
	due, _ := st.DueDeliveries(10)
	if len(due) != 1 {
		t.Fatalf("deliveries = %d", len(due))
	}
	for _, banned := range []string{"100.000", "25.000", "75.000"} {
		if strings.Contains(due[0].Payload, banned) {
			t.Errorf("callback payload leaked balance %q: %s", banned, due[0].Payload)
		}
	}
	if !strings.Contains(due[0].Payload, `"sn":"SN-ABC123"`) {
		t.Errorf("sn missing from callback: %s", due[0].Payload)
	}
}

func TestReportTextPlainBody(t *testing.T) {
	cfg := testConfig()
	srv, st := newTestServer(t, cfg)
	seedTransaction(t, st, "REF4", "081234")

	body := "refid=REF4&status=20&message=" + url.QueryEscape("R1.081234 SUKSES. SN/Ref: A. M-Bal: 1 - 1 = 0")
	req := httptest.NewRequest(http.MethodPost, "/report", strings.NewReader(body))
	req.Header.Set("content-type", "text/plain")
	req.RemoteAddr = "127.0.0.1:1234"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	txn, _ := st.GetTransaction("REF4")
	if txn.Category != "success" {
		t.Fatalf("text/plain report not applied: %s", txn.Category)
	}
}

func TestReportTokenAndAllowlist(t *testing.T) {
	cfg := testConfig()
	cfg.ReportToken = "tok"
	srv, st := newTestServer(t, cfg)
	seedTransaction(t, st, "REF5", "081234")
	msg := "R1.081234 SUKSES. SN/Ref: A. M-Bal: 1 - 1 = 0"

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, reportRequest(url.Values{"refid": {"REF5"}, "message": {msg}}))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("missing token status = %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, reportRequest(url.Values{"refid": {"REF5"}, "message": {msg}, "key": {"tok"}}))
	if rec.Code != http.StatusOK {
		t.Fatalf("valid token status = %d", rec.Code)
	}

	cfg2 := testConfig()
	cfg2.ReportAllowIPs = mustPrefixes(t)
	srv2, st2 := newTestServer(t, cfg2)
	seedTransaction(t, st2, "REF6", "081234")
	rec = httptest.NewRecorder()
	srv2.Handler().ServeHTTP(rec, reportRequest(url.Values{"refid": {"REF6"}, "message": {msg}}))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("allowlist status = %d", rec.Code)
	}
}

func TestTransactionDuplicateRefDoesNotHitCenter(t *testing.T) {
	cfg := testConfig()
	cfg.CenterURL = "http://127.0.0.1:1" // unreachable: a second provider call would fail
	cfg.MemberID = "M"
	cfg.PIN = "1"
	cfg.Password = "p"
	srv, st := newTestServer(t, cfg)
	client, err := st.CreateClient("dup-client")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.InsertTransaction(store.Transaction{
		RefID: "DUP", ClientID: client.Client.ClientID, Product: "P", Dest: "D", Category: "pending",
	}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/transaction",
		strings.NewReader(`{"product":"P","dest":"D","refID":"DUP"}`))
	req.Header.Set("content-type", "application/json")
	req.Header.Set("Authorization", "Bearer "+client.Secret)
	req.Header.Set("x-client-pin", client.PIN)
	req.Header.Set("x-client-keys", client.Keys)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["error"] != "IN_PROGRESS" {
		t.Errorf("error = %v", body["error"])
	}
}

func TestTransactionReplaysFinalizedRef(t *testing.T) {
	cfg := testConfig()
	cfg.CenterURL = "http://127.0.0.1:1" // must not be called
	cfg.MemberID = "M"
	cfg.PIN = "1"
	cfg.Password = "p"
	srv, st := newTestServer(t, cfg)
	client, err := st.CreateClient("replay-client")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.InsertTransaction(store.Transaction{
		RefID: "DONE", ClientID: client.Client.ClientID, Product: "P", Dest: "D", Category: "success",
		ProviderState: "SUCCESS", ProviderCode: "SUKSES", ProviderTrxID: "1", SN: "SN-1",
		RequestRaw: "R1.D SUKSES. SN/Ref: SN-1. M-Bal: 1 - 1 = 0",
	}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/transaction",
		strings.NewReader(`{"product":"P","dest":"D","refID":"DONE"}`))
	req.Header.Set("content-type", "application/json")
	req.Header.Set("Authorization", "Bearer "+client.Secret)
	req.Header.Set("x-client-pin", client.PIN)
	req.Header.Set("x-client-keys", client.Keys)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["category"] != "success" {
		t.Errorf("category = %v", body["category"])
	}
	if !strings.Contains(rec.Body.String(), "SN-1") {
		t.Errorf("replay missing SN: %s", rec.Body.String())
	}
}

func TestImmediateSuccessEmitsInvoicePaid(t *testing.T) {
	center := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("R1.081234 SUKSES. SN/Ref: SN-ABC. M-Bal: 5 - 1 = 4"))
	}))
	defer center.Close()

	cfg := testConfig()
	cfg.CenterURL = center.URL
	cfg.MemberID, cfg.PIN, cfg.Password = "M", "1", "p"
	srv, st := newTestServer(t, cfg)
	srv.ip = ipcenter.New(cfg, srv.log)
	created, err := st.CreateClientWithWebhook("c", "https://example.com/cb", []string{"invoice.*"})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/transaction",
		strings.NewReader(`{"product":"P","dest":"081234","refID":"IMM1"}`))
	req.Header.Set("content-type", "application/json")
	req.Header.Set("Authorization", "Bearer "+created.Secret)
	req.Header.Set("x-client-pin", created.PIN)
	req.Header.Set("x-client-keys", created.Keys)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "SN-ABC") {
		t.Errorf("response missing SN: %s", rec.Body.String())
	}

	due, _ := st.DueDeliveries(20)
	counts := map[string]int{}
	for _, d := range due {
		counts[d.EventName]++
	}
	if counts["transaction.request"] != 1 || counts["invoice.paid"] != 1 || counts["invoice.pending"] != 0 {
		t.Fatalf("unexpected events: %+v", counts)
	}

	// A later success report is a duplicate and must not emit another paid event.
	srv.Handler().ServeHTTP(httptest.NewRecorder(), reportRequest(url.Values{
		"refid": {"IMM1"}, "status": {"20"}, "message": {"R1.081234 SUKSES. SN/Ref: SN-ABC. M-Bal: 5 - 1 = 4"},
	}))
	due, _ = st.DueDeliveries(20)
	counts = map[string]int{}
	for _, d := range due {
		counts[d.EventName]++
	}
	if counts["invoice.paid"] != 1 {
		t.Fatalf("duplicate paid event: %+v", counts)
	}
}

func TestProviderFailureKeepsIntent(t *testing.T) {
	cfg := testConfig()
	cfg.CenterURL = "http://127.0.0.1:1" // connection refused
	cfg.MemberID, cfg.PIN, cfg.Password = "M", "1230", "123pass"
	srv, st := newTestServer(t, cfg)
	srv.ip = ipcenter.New(cfg, srv.log)
	client, err := st.CreateClient("timeout-client")
	if err != nil {
		t.Fatal(err)
	}

	post := func() (int, string) {
		req := httptest.NewRequest(http.MethodPost, "/api/transaction",
			strings.NewReader(`{"product":"P","dest":"D","refID":"AMB1"}`))
		req.Header.Set("content-type", "application/json")
		req.Header.Set("Authorization", "Bearer "+client.Secret)
		req.Header.Set("x-client-pin", client.PIN)
		req.Header.Set("x-client-keys", client.Keys)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}
	code, body := post()
	if code != http.StatusBadGateway {
		t.Fatalf("first status = %d", code)
	}
	for _, banned := range []string{"123pass", "1230", "pin=", "sign=", "password="} {
		if strings.Contains(body, banned) {
			t.Errorf("error response leaked %q: %s", banned, body)
		}
	}
	txn, err := st.GetTransaction("AMB1")
	if err != nil {
		t.Fatalf("intent must be kept for reconciliation: %v", err)
	}
	if txn.Category != "pending" || txn.ProviderState != "" {
		t.Fatalf("unexpected intent: %+v", txn)
	}
	if code, _ := post(); code != http.StatusConflict {
		t.Fatalf("retry status = %d; a retry must not reach the provider", code)
	}
}

func TestVersionAndAdminStats(t *testing.T) {
	cfg := testConfig()
	srv, _ := newTestServer(t, cfg)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/version", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "2.0.0") {
		t.Fatalf("version: %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/stats", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("stats without auth: %d", rec.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/stats", nil)
	req.Header.Set("x-admin-secret", cfg.AdminSecret)
	req.Header.Set("x-admin-pin", cfg.AdminPIN)
	req.Header.Set("x-admin-keys", cfg.AdminKeys)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("stats: %d %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["ok"] != true || body["stats"] == nil {
		t.Errorf("unexpected stats body: %s", rec.Body.String())
	}
}

func mustPrefixes(t *testing.T) []netip.Prefix {
	t.Helper()
	return []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
}
