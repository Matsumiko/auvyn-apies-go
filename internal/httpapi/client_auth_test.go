package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Matsumiko/auvyn-apies-go/internal/ipcenter"
	"github.com/Matsumiko/auvyn-apies-go/internal/store"
)

func TestTransactionClientCredentialsAuth(t *testing.T) {
	center := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("R1.081234 SUKSES. SN/Ref: SN-AUTH. M-Bal: 5 - 1 = 4"))
	}))
	defer center.Close()

	cfg := testConfig()
	cfg.CenterURL = center.URL
	cfg.MemberID, cfg.PIN, cfg.Password = "M", "1", "p"
	srv, st := newTestServer(t, cfg)
	srv.ip = ipcenter.New(cfg, srv.log)
	created, err := st.CreateClient("acme")
	if err != nil {
		t.Fatal(err)
	}

	rec := doJSON(t, srv, http.MethodPost, "/api/transaction", map[string]any{
		"product": "P", "dest": "081234", "refID": "AUTH1",
	}, clientHeaders(created.Secret, created.PIN, created.Keys))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"client_id":"`+created.Client.ClientID+`"`) {
		t.Fatalf("client_id not injected: %s", rec.Body.String())
	}
	txn, err := st.GetTransaction("AUTH1")
	if err != nil {
		t.Fatal(err)
	}
	if txn.ClientID != created.Client.ClientID {
		t.Fatalf("stored client_id = %q", txn.ClientID)
	}
}

func TestTransactionRejectsClientIDMismatch(t *testing.T) {
	srv, st := newTestServer(t, testConfig())
	created, err := st.CreateClient("acme")
	if err != nil {
		t.Fatal(err)
	}
	rec := doJSON(t, srv, http.MethodPost, "/api/transaction", map[string]any{
		"product": "P", "dest": "081234", "refID": "AUTH2",
		"meta": map[string]any{"client_id": "avn_cl_other"},
	}, clientHeaders(created.Secret, created.PIN, created.Keys))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestTransactionRejectsOtherClientsRefID(t *testing.T) {
	srv, st := newTestServer(t, testConfig())
	first, err := st.CreateClient("first")
	if err != nil {
		t.Fatal(err)
	}
	second, err := st.CreateClient("second")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.InsertTransaction(store.Transaction{
		RefID: "SHARED", ClientID: first.Client.ClientID, Product: "P", Dest: "081234",
		Category: "success", ProviderState: "SUCCESS", ProviderCode: "SUKSES",
	}); err != nil {
		t.Fatal(err)
	}
	rec := doJSON(t, srv, http.MethodPost, "/api/transaction", map[string]any{
		"product": "P", "dest": "081234", "refID": "SHARED",
	}, clientHeaders(second.Secret, second.PIN, second.Keys))
	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestTransactionAuthRequiresCompleteClientCredentials(t *testing.T) {
	srv, st := newTestServer(t, testConfig())
	created, err := st.CreateClient("acme")
	if err != nil {
		t.Fatal(err)
	}
	rec := doJSON(t, srv, http.MethodPost, "/api/transaction", map[string]any{
		"product": "P", "dest": "081234", "refID": "AUTH3",
	}, map[string]string{"Authorization": "Bearer " + created.Secret})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestTransactionRejectsGlobalSecret(t *testing.T) {
	cfg := testConfig()
	srv, _ := newTestServer(t, cfg)
	rec := doJSON(t, srv, http.MethodPost, "/api/transaction", map[string]any{
		"product": "P", "dest": "081234", "refID": "GLOBAL1",
	}, map[string]string{"x-auvyn-secret": cfg.AuvynSecret})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func seedStatusTransaction(t *testing.T, st *store.Store, refID, clientID string) {
	t.Helper()
	if err := st.InsertTransaction(store.Transaction{
		RefID: refID, ClientID: clientID, Product: "P", Dest: "081234",
		Category: "success", ProviderState: "SUCCESS", ProviderCode: "SUKSES",
		ProviderTrxID: "R1", SN: "SN-1", RequestRaw: "R1.081234 SUKSES. SN/Ref: SN-1. M-Bal: 5 - 1 = 4",
	}); err != nil {
		t.Fatal(err)
	}
}

func TestTransactionStatusGETAndPOST(t *testing.T) {
	srv, st := newTestServer(t, testConfig())
	client, err := st.CreateClient("status-client")
	if err != nil {
		t.Fatal(err)
	}
	seedStatusTransaction(t, st, "STATUS1", client.Client.ClientID)

	rec := doJSON(t, srv, http.MethodGet, "/api/transaction/status?refID=STATUS1", nil,
		clientHeaders(client.Secret, client.PIN, client.Keys))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["category"] != "success" || body["refID"] != "STATUS1" {
		t.Fatalf("GET body=%s", rec.Body.String())
	}

	rec = doJSON(t, srv, http.MethodPost, "/api/transaction/status",
		map[string]any{"refId": "STATUS1"}, clientHeaders(client.Secret, client.PIN, client.Keys))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestTransactionStatusIsClientScoped(t *testing.T) {
	srv, st := newTestServer(t, testConfig())
	first, err := st.CreateClient("first-status")
	if err != nil {
		t.Fatal(err)
	}
	second, err := st.CreateClient("second-status")
	if err != nil {
		t.Fatal(err)
	}
	seedStatusTransaction(t, st, "STATUS2", first.Client.ClientID)
	rec := doJSON(t, srv, http.MethodGet, "/api/transaction/status?refID=STATUS2", nil,
		clientHeaders(second.Secret, second.PIN, second.Keys))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestTransactionStatusValidation(t *testing.T) {
	srv, st := newTestServer(t, testConfig())
	client, err := st.CreateClient("status-validation")
	if err != nil {
		t.Fatal(err)
	}
	rec := doJSON(t, srv, http.MethodGet, "/api/transaction/status", nil,
		clientHeaders(client.Secret, client.PIN, client.Keys))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing refID status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, srv, http.MethodGet, "/api/transaction/status?refID=STATUS404", nil,
		clientHeaders(client.Secret, client.PIN, client.Keys))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("not found status=%d body=%s", rec.Code, rec.Body.String())
	}
}
