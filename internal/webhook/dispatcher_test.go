package webhook

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Matsumiko/auvyn-apies-go/internal/config"
	"github.com/Matsumiko/auvyn-apies-go/internal/store"
)

type captured struct {
	headers http.Header
	body    map[string]any
	raw     []byte
}

func testDeps(t *testing.T) (*store.Store, *config.Config, *slog.Logger) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	cfg := &config.Config{
		AllowInsecureWebhooks: true,
		AllowPrivateWebhooks:  true,
		CallbackTimeout:       2 * time.Second,
		DeliveryMaxAttempts:   3,
		DeliveryBackoff:       []time.Duration{0},
	}
	return st, cfg, slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestDeliverManagedWebhookWithSignature(t *testing.T) {
	st, cfg, log := testDeps(t)
	got := make(chan captured, 1)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		got <- captured{headers: r.Header.Clone(), body: body, raw: raw}
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	created, err := st.CreateClientWithWebhook("acme", target.URL+"/cb", []string{"invoice.paid"})
	if err != nil {
		t.Fatal(err)
	}
	eventID, inserted, err := st.InsertEventAndDeliveries("R1", "transaction.report", "k1",
		`{"refID":"R1","category":"success","providerResult":{"sn":"SN-ABC"}}`,
		[]store.Delivery{{
			TargetKind: "managed", TargetName: "acme", ClientID: created.Client.ClientID,
			WebhookID: created.Webhook.WebhookID, URL: target.URL + "/cb", EventName: "invoice.paid",
		}})
	if err != nil || !inserted || eventID == 0 {
		t.Fatalf("insert: %v %v %d", err, inserted, eventID)
	}

	disp := New(st, cfg, log)
	if err := disp.DispatchOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	select {
	case c := <-got:
		if c.headers.Get("x-auvyn-event") != "invoice.paid" {
			t.Errorf("event header = %q", c.headers.Get("x-auvyn-event"))
		}
		if c.headers.Get("x-auvyn-callback-secret") != created.SecretCallback {
			t.Errorf("secret header mismatch")
		}
		if c.headers.Get("x-auvyn-client-id") != created.Client.ClientID {
			t.Errorf("client header = %q", c.headers.Get("x-auvyn-client-id"))
		}
		if got := c.headers.Get("x-auvyn-signature"); got != Sign(created.SecretCallback,
			c.headers.Get("x-auvyn-timestamp"), string(c.raw)) {
			t.Errorf("signature mismatch")
		}
		if c.body["event"] != "invoice.paid" || c.body["client_id"] != created.Client.ClientID {
			t.Errorf("body not enriched: %v", c.body)
		}
		if c.body["refID"] != "R1" {
			t.Errorf("payload not preserved: %v", c.body)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("webhook not delivered")
	}

	due, _ := st.DueDeliveries(10)
	if len(due) != 0 {
		t.Errorf("delivery still pending")
	}
}

func TestDeliveryRetriesThenSucceeds(t *testing.T) {
	st, cfg, log := testDeps(t)
	var calls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	created, _ := st.CreateClientWithWebhook("acme", target.URL, []string{"invoice.*"})
	if _, _, err := st.InsertEventAndDeliveries("R2", "transaction.report", "k2", `{"refID":"R2"}`,
		[]store.Delivery{{
			TargetKind: "managed", TargetName: "acme", ClientID: created.Client.ClientID,
			WebhookID: created.Webhook.WebhookID, URL: target.URL, EventName: "invoice.paid",
		}}); err != nil {
		t.Fatal(err)
	}
	disp := New(st, cfg, log)
	if err := disp.DispatchOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	due, _ := st.DueDeliveries(10)
	if len(due) != 1 || due[0].Attempts != 1 {
		t.Fatalf("expected one pending retry, got %+v", due)
	}
	if err := disp.DispatchOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	due, _ = st.DueDeliveries(10)
	if len(due) != 0 {
		t.Errorf("delivery not marked delivered")
	}
	if calls.Load() != 2 {
		t.Errorf("calls = %d want 2", calls.Load())
	}
}

func TestDeadLetterAfterMaxAttempts(t *testing.T) {
	st, cfg, log := testDeps(t)
	cfg.DeliveryMaxAttempts = 1
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer target.Close()

	created, _ := st.CreateClientWithWebhook("acme", target.URL, []string{"invoice.*"})
	if _, _, err := st.InsertEventAndDeliveries("R3", "transaction.report", "k3", `{"refID":"R3"}`,
		[]store.Delivery{{
			TargetKind: "managed", TargetName: "acme", ClientID: created.Client.ClientID,
			WebhookID: created.Webhook.WebhookID, URL: target.URL, EventName: "invoice.paid",
		}}); err != nil {
		t.Fatal(err)
	}
	dead := make(chan string, 1)
	disp := New(st, cfg, log)
	disp.OnDead = func(_ context.Context, d store.Delivery, reason string) { dead <- reason }
	if err := disp.DispatchOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-dead:
	case <-time.After(time.Second):
		t.Fatal("dead letter callback not invoked")
	}
	due, _ := st.DueDeliveries(10)
	if len(due) != 0 {
		t.Errorf("dead delivery still due")
	}
}

func TestValidateURLBlocksPrivateByDefault(t *testing.T) {
	if err := ValidateURL("http://127.0.0.1/cb", true, false); err == nil {
		t.Error("loopback should be blocked")
	}
	if err := ValidateURL("https://1.1.1.1/cb", false, false); err != nil {
		t.Errorf("public https rejected: %v", err)
	}
	if err := ValidateURL("http://example.com/cb", false, false); err == nil {
		t.Error("http should require explicit opt-in")
	}
}
