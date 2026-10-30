package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestClientLifecycle(t *testing.T) {
	st := newStore(t)
	created, err := st.CreateClientWithWebhook("acme", "https://example.com/cb",
		[]string{"invoice.paid", "invoice.failed"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(created.SecretCallback) != 126 {
		t.Errorf("secret_callback length = %d", len(created.SecretCallback))
	}

	client, err := st.FindClientByCredentials(created.Secret, created.PIN, created.Keys)
	if err != nil {
		t.Fatalf("find by credentials: %v", err)
	}
	if client.ClientID != created.Client.ClientID {
		t.Errorf("client id mismatch")
	}
	if _, err := st.FindClientByCredentials(created.Secret, created.PIN, "wrong"); !errors.Is(err, ErrNotFound) {
		t.Errorf("wrong keys: got %v", err)
	}

	_, hooks, err := st.WebhooksForEvent(client.ClientID, "invoice.paid")
	if err != nil || len(hooks) != 1 {
		t.Fatalf("hooks for invoice.paid = %d, err %v", len(hooks), err)
	}
	if _, hooks, _ := st.WebhooksForEvent(client.ClientID, "transaction.report"); len(hooks) != 0 {
		t.Errorf("unexpected hooks for transaction.report: %d", len(hooks))
	}

	rotated, err := st.RotateSecretCallback(client.ClientID)
	if err != nil || rotated == created.SecretCallback {
		t.Fatalf("rotate failed: %v", err)
	}
	updated, _ := st.GetClient(client.ClientID)
	if updated.SecretCallback != rotated {
		t.Errorf("stored secret callback not rotated")
	}

	if err := st.DeleteClient(client.ClientID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := st.FindClientByCredentials(created.Secret, created.PIN, created.Keys); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleted client still authenticates: %v", err)
	}
}

func TestWildcardWebhook(t *testing.T) {
	st := newStore(t)
	created, err := st.CreateClientWithWebhook("wild", "https://example.com/cb", []string{"invoice.*"})
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range []string{"invoice.paid", "invoice.failed", "invoice.pending"} {
		_, hooks, err := st.WebhooksForEvent(created.Client.ClientID, ev)
		if err != nil || len(hooks) != 1 {
			t.Errorf("%s: hooks=%d err=%v", ev, len(hooks), err)
		}
	}
	if _, hooks, _ := st.WebhooksForEvent(created.Client.ClientID, "transaction.request"); len(hooks) != 0 {
		t.Errorf("wildcard matched unrelated event")
	}
}

func TestTransactionTransitions(t *testing.T) {
	st := newStore(t)
	ref := "REF1"
	if err := st.InsertTransaction(Transaction{
		RefID: ref, Product: "P", Dest: "D", Category: "pending",
		Meta: map[string]any{"client_id": "c1"},
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	result, err := st.ApplyReport(ref, "success", "SUCCESS", "SUKSES", "1", "SN", "raw")
	if err != nil || result != ApplyApplied {
		t.Fatalf("apply: %v %v", result, err)
	}
	txn, err := st.GetTransaction(ref)
	if err != nil {
		t.Fatal(err)
	}
	if txn.Category != "success" || txn.Meta["client_id"] != "c1" {
		t.Errorf("unexpected txn: %+v", txn)
	}
	if txn.FinalAt == nil {
		t.Errorf("final_at not set")
	}

	if r, _ := st.ApplyReport(ref, "success", "SUCCESS", "SUKSES", "1", "SN", "raw"); r != ApplyDuplicate {
		t.Errorf("duplicate report: got %s", r)
	}
	if r, _ := st.ApplyReport(ref, "failed", "FAILED", "GAGAL", "", "", "raw"); r != ApplyConflict {
		t.Errorf("conflict report: got %s", r)
	}
	txn, _ = st.GetTransaction(ref)
	if txn.Category != "success" {
		t.Errorf("conflict overwrote final status: %s", txn.Category)
	}
	if r, _ := st.ApplyReport("MISSING", "success", "", "", "", "", ""); r != ApplyNotFound {
		t.Errorf("missing ref: got %s", r)
	}
}

func TestUpdateTransactionResult(t *testing.T) {
	st := newStore(t)
	if err := st.InsertTransaction(Transaction{RefID: "R1", Product: "P", Dest: "D"}); err != nil {
		t.Fatal(err)
	}
	qty := int64(2)
	applied, err := st.UpdateTransactionResult("R1", "failed", &qty, nil, "FAILED", "GAGAL", "", "", "raw")
	if err != nil || !applied {
		t.Fatalf("update: %v %v", applied, err)
	}
	applied, _ = st.UpdateTransactionResult("R1", "success", nil, nil, "SUCCESS", "", "", "", "")
	if applied {
		t.Errorf("final transaction must not be updated again")
	}
	// Rollback only removes the pre-provider placeholder.
	if err := st.DeletePendingTransaction("R1"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetTransaction("R1"); err != nil {
		t.Errorf("final transaction must be kept: %v", err)
	}
	if err := st.InsertTransaction(Transaction{RefID: "R2", Product: "P", Dest: "D"}); err != nil {
		t.Fatal(err)
	}
	if err := st.DeletePendingTransaction("R2"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetTransaction("R2"); !errors.Is(err, ErrNotFound) {
		t.Errorf("pending placeholder was not removed: %v", err)
	}
}

func TestImportLegacyJSON(t *testing.T) {
	st := newStore(t)
	path := filepath.Join(t.TempDir(), "db.json")
	content := `{
	  "clients": [{
	    "client_id": "avn_cl_1", "client_name": "legacy", "status": "ACTIVE",
	    "registered_at": "2025-12-21T03:40:27.330Z",
	    "secret_hash": "h1", "pin_hash": "h2", "keys_hash": "h3",
	    "secret_callback": "cb", "secret_callback_rotated_at": "2025-12-21T03:40:27.332Z"
	  }],
	  "webhooks": [{
	    "webhook_id": "avn_wh_1", "client_id": "avn_cl_1",
	    "url": "https://example.com/cb", "status": "ACTIVE",
	    "created_at": "2025-12-21T03:40:27.332Z",
	    "events": ["invoice.paid", "invoice.failed"]
	  }]
	}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := st.ImportLegacyJSON(path)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if res.Clients != 1 || res.Webhooks != 1 {
		t.Fatalf("unexpected import result: %+v", res)
	}
	res, err = st.ImportLegacyJSON(path)
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	if res.Clients != 0 || res.Webhooks != 0 {
		t.Errorf("import not idempotent: %+v", res)
	}
	_, hooks, err := st.WebhooksForEvent("avn_cl_1", "invoice.paid")
	if err != nil || len(hooks) != 1 || len(hooks[0].Events) != 2 {
		t.Errorf("imported webhook not routed: %+v err=%v", hooks, err)
	}
}

func TestUpsertTransactionPrecedence(t *testing.T) {
	st := newStore(t)
	inserted, err := st.UpsertTransaction(Transaction{
		RefID: "U1", Product: "P", Dest: "D", Category: "pending",
	})
	if err != nil || !inserted {
		t.Fatalf("insert pending: %v %v", inserted, err)
	}
	_, err = st.UpsertTransaction(Transaction{
		RefID: "U1", Product: "P", Dest: "D", Category: "success",
		ProviderState: "SUCCESS", ProviderCode: "SUKSES", SN: "SN1",
		Meta: map[string]any{"client_id": "c1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	txn, _ := st.GetTransaction("U1")
	if txn.Category != "success" || txn.SN != "SN1" || txn.Meta["client_id"] != "c1" || txn.FinalAt == nil {
		t.Fatalf("success not applied: %+v", txn)
	}
	_, err = st.UpsertTransaction(Transaction{
		RefID: "U1", Product: "P", Dest: "D", Category: "failed", ProviderCode: "GAGAL",
	})
	if err != nil {
		t.Fatal(err)
	}
	txn, _ = st.GetTransaction("U1")
	if txn.Category != "success" {
		t.Fatalf("conflicting final overwrote: %s", txn.Category)
	}

	if _, err := st.UpsertTransaction(Transaction{RefID: "U2", Product: "P", Dest: "D", Category: "pending"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertTransaction(Transaction{
		RefID: "U2", Product: "P", Dest: "D", Category: "pending",
		Meta: map[string]any{"orderId": "INV-2"},
	}); err != nil {
		t.Fatal(err)
	}
	txn, _ = st.GetTransaction("U2")
	if txn.Meta["orderId"] != "INV-2" {
		t.Fatalf("pending merge failed: %+v", txn.Meta)
	}
}

func TestPruneAndStats(t *testing.T) {
	st := newStore(t)
	if err := st.InsertTransaction(Transaction{RefID: "R1", Product: "P", Dest: "D", Category: "success"}); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertTransaction(Transaction{RefID: "R2", Product: "P", Dest: "D", Category: "pending"}); err != nil {
		t.Fatal(err)
	}
	deliveredID, _, err := st.InsertEventAndDeliveries("R1", "transaction.report", "p1", `{}`, []Delivery{{
		TargetKind: "legacy", TargetName: "default", URL: "https://e/cb", EventName: "transaction.report",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.MarkDelivering(deliveredID); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkDelivered(deliveredID, 200); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.InsertEventAndDeliveries("R2", "transaction.report", "p2", `{}`, []Delivery{{
		TargetKind: "legacy", TargetName: "default", URL: "https://e/cb", EventName: "transaction.report",
	}}); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertProviderReport(ProviderReport{RefID: "R1", Accepted: false, Reason: "test"}); err != nil {
		t.Fatal(err)
	}

	stats, err := st.Stats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.TransactionsByCategory["success"] != 1 || stats.TransactionsByCategory["pending"] != 1 {
		t.Errorf("transactions: %+v", stats.TransactionsByCategory)
	}
	if stats.Deliveries["delivered"] != 1 || stats.Deliveries["pending"] != 1 {
		t.Errorf("deliveries: %+v", stats.Deliveries)
	}
	if stats.ProviderReports24h != 1 || stats.RejectedReports24h != 1 {
		t.Errorf("provider reports: %+v", stats)
	}

	// Negative retention forces the cutoff into the future for the test.
	pruned, err := st.Prune(context.Background(), -time.Hour)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if pruned.Deliveries != 1 || pruned.Events != 1 || pruned.ProviderReports != 1 {
		t.Errorf("unexpected prune result: %+v", pruned)
	}
	due, err := st.DueDeliveries(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 {
		t.Errorf("pending delivery was pruned: %d", len(due))
	}
	stats, _ = st.Stats(context.Background())
	if stats.Deliveries["delivered"] != 0 || stats.ProviderReports24h != 0 {
		t.Errorf("prune did not remove rows: %+v", stats)
	}
}

func TestDeleteWebhookCancelsQueuedDeliveries(t *testing.T) {
	st := newStore(t)
	created, err := st.CreateClientWithWebhook("acme", "https://example.com/cb", []string{"invoice.paid"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.InsertEventAndDeliveries("R1", "transaction.report", "k1", `{}`, []Delivery{{
		TargetKind: "managed", TargetName: "acme", ClientID: created.Client.ClientID,
		WebhookID: created.Webhook.WebhookID, URL: created.Webhook.URL, EventName: "invoice.paid",
	}}); err != nil {
		t.Fatal(err)
	}
	if due, _ := st.DueDeliveries(10); len(due) != 1 {
		t.Fatalf("delivery not queued: %d", len(due))
	}
	if err := st.DeleteWebhook(created.Webhook.WebhookID); err != nil {
		t.Fatal(err)
	}
	if due, _ := st.DueDeliveries(10); len(due) != 0 {
		t.Errorf("queued delivery still active after webhook delete: %d", len(due))
	}
}

func TestMatchEvent(t *testing.T) {
	cases := []struct {
		sub, ev string
		want    bool
	}{
		{"invoice.paid", "invoice.paid", true},
		{"invoice.*", "invoice.failed", true},
		{"invoice.*", "transaction.report", false},
		{"transaction.*", "transaction.report", true},
		{"", "invoice.paid", false},
	}
	for _, tc := range cases {
		if got := MatchEvent(tc.sub, tc.ev); got != tc.want {
			t.Errorf("%q vs %q = %v want %v", tc.sub, tc.ev, got, tc.want)
		}
	}
}

func TestDueDeliveriesOrdering(t *testing.T) {
	st := newStore(t)
	id, inserted, err := st.InsertEventAndDeliveries("R1", "transaction.report", "k1", `{"a":1}`,
		[]Delivery{{TargetKind: "legacy", TargetName: "default", URL: "https://e/cb", EventName: "transaction.report"}})
	if err != nil || !inserted || id == 0 {
		t.Fatalf("insert event: %v %v %d", err, inserted, id)
	}
	_, inserted, _ = st.InsertEventAndDeliveries("R1", "transaction.report", "k1", `{"a":1}`, nil)
	if inserted {
		t.Errorf("duplicate dedupe key was inserted")
	}
	due, err := st.DueDeliveries(10)
	if err != nil || len(due) != 1 {
		t.Fatalf("due=%d err=%v", len(due), err)
	}
	if due[0].Payload != `{"a":1}` || due[0].EventName != "transaction.report" {
		t.Errorf("unexpected due delivery: %+v", due[0])
	}
	if err := st.MarkDelivering(due[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkDelivered(due[0].ID, 200); err != nil {
		t.Fatal(err)
	}
	due, _ = st.DueDeliveries(10)
	if len(due) != 0 {
		t.Errorf("delivered item still due")
	}

	// failed delivery is retried when next_attempt_at has passed
	id2, _, _ := st.InsertEventAndDeliveries("R2", "transaction.report", "k2", `{}`, []Delivery{{
		TargetKind: "legacy", TargetName: "default", URL: "https://e/cb", EventName: "transaction.report",
	}})
	_ = st.MarkDelivering(id2)
	dead, err := st.MarkFailed(id2, "boom", time.Now().Add(-time.Second), 3)
	if err != nil || dead {
		t.Fatalf("mark failed: dead=%v err=%v", dead, err)
	}
	due, _ = st.DueDeliveries(10)
	if len(due) != 1 {
		t.Errorf("failed delivery not retried")
	}
}
