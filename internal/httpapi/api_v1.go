package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Matsumiko/auvyn-apies-go/internal/store"
	"github.com/Matsumiko/auvyn-apies-go/internal/webhook"
)

// Versioned admin/client API.
//
// Conventions:
//   - resource-oriented paths, plural nouns, IDs in the path
//   - HTTP verbs carry intent (GET/POST/PATCH/DELETE)
//   - success: {"data": ..., "meta": ...}; error: {"error": {"code","message"}}
//   - pagination: ?limit=&offset= with meta.total/limit/offset
//
// Legacy verb-style routes remain as deprecated aliases with the old response
// shapes, so existing automations keep working during migration.

type clientDTO struct {
	ClientID                string     `json:"client_id"`
	ClientName              string     `json:"client_name"`
	Status                  string     `json:"status"`
	CreatedAt               time.Time  `json:"created_at"`
	CallbackSecretRotatedAt *time.Time `json:"callback_secret_rotated_at,omitempty"`
}

type webhookDTO struct {
	WebhookID  string    `json:"webhook_id"`
	ClientID   string    `json:"client_id"`
	ClientName string    `json:"client_name"`
	URL        string    `json:"url"`
	Status     string    `json:"status"`
	Events     []string  `json:"events"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func toClientDTO(c store.PublicClient) clientDTO {
	return clientDTO{
		ClientID: c.ClientID, ClientName: c.ClientName, Status: c.Status, CreatedAt: c.RegisteredAt,
	}
}

func toClientDTOFromFull(c *store.Client) clientDTO {
	dto := clientDTO{
		ClientID: c.ClientID, ClientName: c.ClientName, Status: c.Status, CreatedAt: c.RegisteredAt,
	}
	if !c.SecretCallbackRotatedAt.IsZero() {
		t := c.SecretCallbackRotatedAt
		dto.CallbackSecretRotatedAt = &t
	}
	return dto
}

func toWebhookDTO(w store.Webhook) webhookDTO {
	events := w.Events
	if events == nil {
		events = []string{}
	}
	return webhookDTO{
		WebhookID: w.WebhookID, ClientID: w.ClientID, ClientName: w.ClientName,
		URL: w.URL, Status: w.Status, Events: events,
		CreatedAt: w.CreatedAt, UpdatedAt: w.UpdatedAt,
	}
}

func writeData(w http.ResponseWriter, status int, data any, meta map[string]any) {
	body := map[string]any{"data": data}
	if meta != nil {
		body["meta"] = meta
	}
	writeJSON(w, status, body)
}

func writeAPIError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{"code": code, "message": message},
	})
}

func apiStoreError(w http.ResponseWriter, err error) {
	switch err {
	case store.ErrNotFound:
		writeAPIError(w, http.StatusNotFound, "NOT_FOUND", "resource not found")
	case store.ErrForbidden:
		writeAPIError(w, http.StatusForbidden, "FORBIDDEN", "resource belongs to another client")
	case store.ErrNotActive:
		writeAPIError(w, http.StatusConflict, "NOT_ACTIVE", "resource is not active")
	default:
		writeAPIError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
	}
}

func parsePagination(r *http.Request) (limit, offset int, err error) {
	limit, offset = 50, 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 200 {
			return 0, 0, fmt.Errorf("limit must be between 1 and 200")
		}
	}
	if raw := r.URL.Query().Get("offset"); raw != "" {
		offset, err = strconv.Atoi(raw)
		if err != nil || offset < 0 {
			return 0, 0, fmt.Errorf("offset must be >= 0")
		}
	}
	return limit, offset, nil
}

func paginationMeta(total int64, limit, offset int) map[string]any {
	return map[string]any{"total": total, "limit": limit, "offset": offset}
}

func bearerToken(r *http.Request) string {
	h := strings.TrimSpace(r.Header.Get("authorization"))
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

func deprecated(successor string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Deprecation", "true")
		w.Header().Set("Link", fmt.Sprintf("<%s>; rel=\"successor-version\"", successor))
		next.ServeHTTP(w, r)
	})
}

// ---------- admin clients ----------

func (s *Server) adminListClients(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := parsePagination(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	clients, total, err := s.st.ListClientsPage(limit, offset)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list clients")
		return
	}
	items := make([]clientDTO, 0, len(clients))
	for _, c := range clients {
		items = append(items, toClientDTO(c))
	}
	writeData(w, http.StatusOK, items, paginationMeta(total, limit, offset))
}

func (s *Server) adminCreateClient(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ClientName    string `json:"client_name"`
		ClientNameAlt string `json:"clientName"`
		Webhook       *struct {
			URL    string          `json:"url"`
			Events json.RawMessage `json:"events"`
		} `json:"webhook"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON body")
		return
	}
	name := strings.TrimSpace(req.ClientName)
	if name == "" {
		name = strings.TrimSpace(req.ClientNameAlt)
	}
	if name == "" {
		writeAPIError(w, http.StatusBadRequest, "BAD_REQUEST", "client_name is required")
		return
	}

	var hookURL string
	var hookEvents []string
	if req.Webhook != nil {
		hookURL = strings.TrimSpace(req.Webhook.URL)
		if hookURL == "" {
			writeAPIError(w, http.StatusBadRequest, "BAD_REQUEST", "webhook.url is required")
			return
		}
		if err := webhook.ValidateURL(hookURL, s.cfg.AllowInsecureWebhooks, s.cfg.AllowPrivateWebhooks); err != nil {
			writeAPIError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
			return
		}
		events, ok := normalizeEvents(req.Webhook.Events)
		if !ok {
			writeAPIError(w, http.StatusBadRequest, "BAD_REQUEST", "webhook.events must be an array or comma-separated string")
			return
		}
		if len(events) == 0 {
			events = []string{"invoice.paid", "invoice.failed", "invoice.pending"}
		}
		hookEvents = events
	}

	created, err := s.st.CreateClient(name)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to create client")
		return
	}
	data := map[string]any{
		"client": toClientDTOFromFull(&created.Client),
		"credentials": map[string]any{
			"client_secret": created.Secret,
			"client_pin":    created.PIN,
			"client_keys":   created.Keys,
		},
		"callback_secret": map[string]any{
			"value":          created.SecretCallback,
			"length":         len(created.SecretCallback),
			"displayed_once": true,
		},
	}
	w.Header().Set("Location", "/admin/v1/clients/"+created.Client.ClientID)
	if hookURL != "" {
		hook, err := s.st.CreateWebhook(created.Client.ClientID, hookURL, hookEvents)
		if err != nil {
			_ = s.st.DeleteClient(created.Client.ClientID)
			writeAPIError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to create webhook")
			return
		}
		data["webhook"] = toWebhookDTO(*hook)
	}
	writeData(w, http.StatusCreated, data, map[string]any{"displayed_once": true})
}

func (s *Server) adminGetClient(w http.ResponseWriter, r *http.Request) {
	client, err := s.st.GetClient(r.PathValue("client_id"))
	if err != nil {
		apiStoreError(w, err)
		return
	}
	if client.Status != "ACTIVE" {
		writeAPIError(w, http.StatusNotFound, "NOT_FOUND", "client not found")
		return
	}
	writeData(w, http.StatusOK, toClientDTOFromFull(client), nil)
}

func (s *Server) adminDeleteClient(w http.ResponseWriter, r *http.Request) {
	if err := s.st.DeleteClient(r.PathValue("client_id")); err != nil {
		apiStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) adminRotateClientSecret(w http.ResponseWriter, r *http.Request) {
	clientID := r.PathValue("client_id")
	value, err := s.st.RotateSecretCallback(clientID)
	if err != nil {
		apiStoreError(w, err)
		return
	}
	writeData(w, http.StatusOK, map[string]any{
		"client_id":       clientID,
		"callback_secret": value,
	}, map[string]any{"displayed_once": true})
}

// ---------- admin webhooks ----------

func (s *Server) adminListWebhooks(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := parsePagination(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	hooks, total, err := s.st.ListWebhooksPage(limit, offset)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list webhooks")
		return
	}
	items := make([]webhookDTO, 0, len(hooks))
	for _, h := range hooks {
		items = append(items, toWebhookDTO(h))
	}
	writeData(w, http.StatusOK, items, paginationMeta(total, limit, offset))
}

func (s *Server) adminListClientWebhooks(w http.ResponseWriter, r *http.Request) {
	clientID := r.PathValue("client_id")
	client, err := s.st.GetClient(clientID)
	if err != nil || client.Status != "ACTIVE" {
		writeAPIError(w, http.StatusNotFound, "NOT_FOUND", "client not found")
		return
	}
	hooks, err := s.st.ListWebhooksByClient(clientID)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list webhooks")
		return
	}
	items := make([]webhookDTO, 0, len(hooks))
	for _, h := range hooks {
		items = append(items, toWebhookDTO(h))
	}
	writeData(w, http.StatusOK, items, map[string]any{"total": len(items)})
}

func (s *Server) adminCreateWebhook(w http.ResponseWriter, r *http.Request) {
	clientID := r.PathValue("client_id")
	var req struct {
		URL    string          `json:"url"`
		Events json.RawMessage `json:"events"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON body")
		return
	}
	req.URL = strings.TrimSpace(req.URL)
	if req.URL == "" {
		writeAPIError(w, http.StatusBadRequest, "BAD_REQUEST", "url is required")
		return
	}
	if err := webhook.ValidateURL(req.URL, s.cfg.AllowInsecureWebhooks, s.cfg.AllowPrivateWebhooks); err != nil {
		writeAPIError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	events, ok := normalizeEvents(req.Events)
	if !ok {
		writeAPIError(w, http.StatusBadRequest, "BAD_REQUEST", "events must be an array or comma-separated string")
		return
	}
	if len(events) == 0 {
		events = []string{"invoice.paid", "invoice.failed", "invoice.pending"}
	}
	hook, err := s.st.CreateWebhook(clientID, req.URL, events)
	if err != nil {
		apiStoreError(w, err)
		return
	}
	w.Header().Set("Location", "/admin/v1/webhooks/"+hook.WebhookID)
	writeData(w, http.StatusCreated, toWebhookDTO(*hook), nil)
}

func (s *Server) adminGetWebhook(w http.ResponseWriter, r *http.Request) {
	hook, err := s.st.GetWebhook(r.PathValue("webhook_id"))
	if err != nil {
		apiStoreError(w, err)
		return
	}
	writeData(w, http.StatusOK, toWebhookDTO(*hook), nil)
}

func (s *Server) adminUpdateWebhook(w http.ResponseWriter, r *http.Request) {
	s.updateWebhookResource(w, r, r.PathValue("webhook_id"), "")
}

func (s *Server) adminDeleteWebhook(w http.ResponseWriter, r *http.Request) {
	if err := s.st.DeleteWebhook(r.PathValue("webhook_id")); err != nil {
		apiStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------- client self-service ----------

func (s *Server) clientMe(w http.ResponseWriter, r *http.Request) {
	client := clientFrom(r.Context())
	if client == nil {
		writeAPIError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid client")
		return
	}
	writeData(w, http.StatusOK, toClientDTOFromFull(client), nil)
}

func (s *Server) clientListWebhooks(w http.ResponseWriter, r *http.Request) {
	client := clientFrom(r.Context())
	hooks, err := s.st.ListWebhooksByClient(client.ClientID)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list webhooks")
		return
	}
	items := make([]webhookDTO, 0, len(hooks))
	for _, h := range hooks {
		items = append(items, toWebhookDTO(h))
	}
	writeData(w, http.StatusOK, items, map[string]any{"total": len(items)})
}

func (s *Server) clientUpdateWebhook(w http.ResponseWriter, r *http.Request) {
	client := clientFrom(r.Context())
	s.updateWebhookResource(w, r, r.PathValue("webhook_id"), client.ClientID)
}

func (s *Server) updateWebhookResource(w http.ResponseWriter, r *http.Request, webhookID, expectedClientID string) {
	var req map[string]json.RawMessage
	if err := decodeJSON(w, r, &req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON body")
		return
	}
	urlValue := rawString(req["url"])
	eventsRaw, hasEvents := req["events"]
	if urlValue == "" && !hasEvents {
		writeAPIError(w, http.StatusBadRequest, "BAD_REQUEST", "url and/or events is required")
		return
	}
	if urlValue != "" {
		if err := webhook.ValidateURL(urlValue, s.cfg.AllowInsecureWebhooks, s.cfg.AllowPrivateWebhooks); err != nil {
			writeAPIError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
			return
		}
	}
	var events []string
	if hasEvents {
		var ok bool
		events, ok = normalizeEvents(eventsRaw)
		if !ok {
			writeAPIError(w, http.StatusBadRequest, "BAD_REQUEST", "events must be an array or comma-separated string")
			return
		}
	}

	var (
		updated *store.Webhook
		err     error
	)
	if expectedClientID == "" {
		updated, err = s.st.UpdateWebhookAdmin(webhookID, urlValue, events, hasEvents)
	} else {
		updated, err = s.st.UpdateWebhook(expectedClientID, webhookID, urlValue, events, hasEvents)
	}
	if err != nil {
		apiStoreError(w, err)
		return
	}
	writeData(w, http.StatusOK, toWebhookDTO(*updated), nil)
}

func (s *Server) clientDeleteWebhook(w http.ResponseWriter, r *http.Request) {
	client := clientFrom(r.Context())
	hook, err := s.st.GetWebhook(r.PathValue("webhook_id"))
	if err != nil {
		apiStoreError(w, err)
		return
	}
	if hook.ClientID != client.ClientID {
		writeAPIError(w, http.StatusForbidden, "FORBIDDEN", "resource belongs to another client")
		return
	}
	if err := s.st.DeleteWebhook(hook.WebhookID); err != nil {
		apiStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) clientRotateSecret(w http.ResponseWriter, r *http.Request) {
	client := clientFrom(r.Context())
	value, err := s.st.RotateSecretCallback(client.ClientID)
	if err != nil {
		apiStoreError(w, err)
		return
	}
	writeData(w, http.StatusOK, map[string]any{
		"client_id":       client.ClientID,
		"callback_secret": value,
	}, map[string]any{"displayed_once": true})
}
