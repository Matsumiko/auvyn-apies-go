package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Matsumiko/auvyn-apies-go/internal/config"
)

func adminHeaders(cfg *config.Config) map[string]string {
	return map[string]string{
		"Authorization": "Bearer " + cfg.AdminSecret,
		"x-admin-pin":   cfg.AdminPIN,
		"x-admin-keys":  cfg.AdminKeys,
	}
}

func clientHeaders(secret, pin, keys string) map[string]string {
	return map[string]string{
		"Authorization": "Bearer " + secret,
		"x-client-pin":  pin,
		"x-client-keys": keys,
	}
}

func doJSON(t *testing.T, srv *Server, method, path string, body any, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *strings.Reader
	if body == nil {
		reader = strings.NewReader("")
	} else {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = strings.NewReader(string(b))
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("content-type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func decodeData(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v body=%s", err, rec.Body.String())
	}
	data, _ := body["data"].(map[string]any)
	return data
}

func TestAdminV1ClientLifecycle(t *testing.T) {
	cfg := testConfig()
	cfg.AllowInsecureWebhooks = true
	cfg.AllowPrivateWebhooks = true
	srv, _ := newTestServer(t, cfg)
	h := adminHeaders(cfg)

	rec := doJSON(t, srv, http.MethodPost, "/admin/v1/clients", map[string]any{
		"client_name": "acme",
		"webhook": map[string]any{
			"url":    "http://127.0.0.1:9999/cb",
			"events": []string{"invoice.paid"},
		},
	}, h)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); !strings.HasPrefix(loc, "/admin/v1/clients/") {
		t.Errorf("Location = %q", loc)
	}
	data := decodeData(t, rec)
	client, _ := data["client"].(map[string]any)
	clientID, _ := client["client_id"].(string)
	if clientID == "" {
		t.Fatalf("missing client_id: %s", rec.Body.String())
	}
	creds, _ := data["credentials"].(map[string]any)
	if creds["client_secret"] == "" || creds["client_pin"] == "" || creds["client_keys"] == "" {
		t.Errorf("missing credentials: %v", creds)
	}
	callback, _ := data["callback_secret"].(map[string]any)
	if callback["value"] == "" || callback["displayed_once"] != true {
		t.Errorf("missing callback secret: %v", callback)
	}
	hook, _ := data["webhook"].(map[string]any)
	if hook["url"] != "http://127.0.0.1:9999/cb" {
		t.Errorf("webhook = %v", hook)
	}

	rec = doJSON(t, srv, http.MethodGet, "/admin/v1/clients", nil, h)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status=%d", rec.Code)
	}
	var listBody struct {
		Data []map[string]any `json:"data"`
		Meta map[string]any   `json:"meta"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listBody); err != nil {
		t.Fatal(err)
	}
	if len(listBody.Data) != 1 || listBody.Meta["total"] != float64(1) {
		t.Errorf("unexpected list: %s", rec.Body.String())
	}
	if _, ok := listBody.Data[0]["created_at"]; !ok {
		t.Errorf("client DTO missing created_at: %v", listBody.Data[0])
	}

	rec = doJSON(t, srv, http.MethodGet, "/admin/v1/clients/"+clientID, nil, h)
	if rec.Code != http.StatusOK {
		t.Fatalf("get status=%d", rec.Code)
	}
	rec = doJSON(t, srv, http.MethodGet, "/admin/v1/clients/"+clientID+"/webhooks", nil, h)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "invoice.paid") {
		t.Fatalf("client webhooks: %d %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, srv, http.MethodDelete, "/admin/v1/clients/"+clientID, nil, h)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d", rec.Code)
	}
	rec = doJSON(t, srv, http.MethodGet, "/admin/v1/clients/"+clientID, nil, h)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("deleted client get status=%d", rec.Code)
	}
}

func TestAdminV1CreateClientWithoutWebhook(t *testing.T) {
	cfg := testConfig()
	srv, _ := newTestServer(t, cfg)
	rec := doJSON(t, srv, http.MethodPost, "/admin/v1/clients",
		map[string]any{"client_name": "plain"}, adminHeaders(cfg))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	data := decodeData(t, rec)
	if _, ok := data["webhook"]; ok {
		t.Errorf("unexpected webhook: %v", data)
	}
}

func TestAdminV1WebhookCreatePatchDelete(t *testing.T) {
	cfg := testConfig()
	cfg.AllowInsecureWebhooks = true
	cfg.AllowPrivateWebhooks = true
	srv, st := newTestServer(t, cfg)
	h := adminHeaders(cfg)
	created, err := st.CreateClient("acme")
	if err != nil {
		t.Fatal(err)
	}

	rec := doJSON(t, srv, http.MethodPost, "/admin/v1/clients/"+created.Client.ClientID+"/webhooks",
		map[string]any{"url": "http://127.0.0.1:9999/cb", "events": []string{"invoice.paid"}}, h)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create webhook status=%d body=%s", rec.Code, rec.Body.String())
	}
	data := decodeData(t, rec)
	webhookID, _ := data["webhook_id"].(string)
	if webhookID == "" {
		t.Fatal("missing webhook_id")
	}

	rec = doJSON(t, srv, http.MethodPatch, "/admin/v1/webhooks/"+webhookID,
		map[string]any{"events": []string{"invoice.paid", "invoice.failed"}}, h)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "invoice.failed") {
		t.Fatalf("patch status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, srv, http.MethodDelete, "/admin/v1/webhooks/"+webhookID, nil, h)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d", rec.Code)
	}
	rec = doJSON(t, srv, http.MethodGet, "/admin/v1/webhooks/"+webhookID, nil, h)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("deleted webhook get status=%d", rec.Code)
	}
}

func TestClientV1SelfService(t *testing.T) {
	cfg := testConfig()
	cfg.AllowInsecureWebhooks = true
	cfg.AllowPrivateWebhooks = true
	srv, st := newTestServer(t, cfg)
	created, err := st.CreateClient("acme")
	if err != nil {
		t.Fatal(err)
	}
	hook, err := st.CreateWebhook(created.Client.ClientID, "http://127.0.0.1:9999/cb", []string{"invoice.paid"})
	if err != nil {
		t.Fatal(err)
	}
	h := clientHeaders(created.Secret, created.PIN, created.Keys)

	rec := doJSON(t, srv, http.MethodGet, "/client/v1/me", nil, h)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), created.Client.ClientID) {
		t.Fatalf("me status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, srv, http.MethodGet, "/client/v1/webhooks", nil, h)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), hook.WebhookID) {
		t.Fatalf("webhooks status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, srv, http.MethodPatch, "/client/v1/webhooks/"+hook.WebhookID,
		map[string]any{"url": "http://127.0.0.1:9998/cb"}, h)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "9998") {
		t.Fatalf("patch status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, srv, http.MethodPost, "/client/v1/callback-secret/rotate", nil, h)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "callback_secret") {
		t.Fatalf("rotate status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, srv, http.MethodDelete, "/client/v1/webhooks/"+hook.WebhookID, nil, h)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d", rec.Code)
	}
}

func TestAdminV1Stats(t *testing.T) {
	cfg := testConfig()
	srv, _ := newTestServer(t, cfg)
	rec := doJSON(t, srv, http.MethodGet, "/admin/v1/stats", nil, adminHeaders(cfg))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data["stats"] == nil || body.Data["version"] == nil {
		t.Errorf("unexpected stats: %s", rec.Body.String())
	}
	rec = doJSON(t, srv, http.MethodGet, "/admin/v1/stats", nil, nil)
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), `"error"`) {
		t.Errorf("stats without auth = %d %s", rec.Code, rec.Body.String())
	}
}

func TestLegacyAdminAliasIsDeprecated(t *testing.T) {
	cfg := testConfig()
	srv, _ := newTestServer(t, cfg)
	req := httptest.NewRequest(http.MethodGet, "/admin/client/list-clients", nil)
	req.Header.Set("x-admin-secret", cfg.AdminSecret)
	req.Header.Set("x-admin-pin", cfg.AdminPIN)
	req.Header.Set("x-admin-keys", cfg.AdminKeys)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("legacy status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Deprecation") != "true" {
		t.Errorf("missing Deprecation header")
	}
	if !strings.Contains(rec.Body.String(), `"clients"`) {
		t.Errorf("legacy response shape changed: %s", rec.Body.String())
	}
}

func TestAdminV1PaginationValidation(t *testing.T) {
	cfg := testConfig()
	srv, _ := newTestServer(t, cfg)
	rec := doJSON(t, srv, http.MethodGet, "/admin/v1/clients?limit=0", nil, adminHeaders(cfg))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"error"`) {
		t.Errorf("missing error envelope: %s", rec.Body.String())
	}
}
