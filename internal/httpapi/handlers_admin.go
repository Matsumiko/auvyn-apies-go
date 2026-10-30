package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/Matsumiko/auvyn-apies-go/internal/store"
	"github.com/Matsumiko/auvyn-apies-go/internal/version"
	"github.com/Matsumiko/auvyn-apies-go/internal/webhook"
)

func (s *Server) handleAdmin(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/admin/client/list-clients":
		clients, err := s.st.ListClients()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list clients")
			return
		}
		if clients == nil {
			clients = []store.PublicClient{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "clients": clients})

	case r.Method == http.MethodGet && r.URL.Path == "/admin/stats":
		s.adminStats(w, r)

	case r.Method == http.MethodGet && r.URL.Path == "/admin/client/list-webhook":
		hooks, err := s.st.ListWebhooks()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list webhooks")
			return
		}
		if hooks == nil {
			hooks = []store.Webhook{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "webhooks": hooks})

	case r.Method == http.MethodPost && r.URL.Path == "/admin/client/add-clients":
		s.handleAddClient(w, r)

	case r.Method == http.MethodPost && r.URL.Path == "/admin/client/del-clients":
		var req struct {
			ClientID string `json:"client_id"`
		}
		if err := decodeJSON(w, r, &req); err != nil {
			writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON body")
			return
		}
		if strings.TrimSpace(req.ClientID) == "" {
			writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "client_id is required")
			return
		}
		if err := s.st.DeleteClient(strings.TrimSpace(req.ClientID)); err != nil {
			if err == store.ErrNotFound {
				writeErr(w, http.StatusNotFound, "NOT_FOUND", "client not found")
				return
			}
			writeErr(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to delete client")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "Client deleted"})

	default:
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "route not found")
	}
}

func (s *Server) adminStats(w http.ResponseWriter, r *http.Request) {
	stats, err := s.statsPayload(r)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to read stats")
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (s *Server) adminStatsV1(w http.ResponseWriter, r *http.Request) {
	stats, err := s.statsPayload(r)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to read stats")
		return
	}
	writeData(w, http.StatusOK, map[string]any{
		"version": stats["version"], "uptime_seconds": stats["uptimeSeconds"], "stats": stats["stats"],
	}, nil)
}

func (s *Server) statsPayload(r *http.Request) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	stats, err := s.st.Stats(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"ok": true, "version": version.Version,
		"uptimeSeconds": int64(time.Since(s.startedAt).Seconds()),
		"stats":         stats,
	}, nil
}

func (s *Server) handleAddClient(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ClientName  string          `json:"client_name"`
		ClientName2 string          `json:"clientName"`
		URL         string          `json:"url"`
		Events      json.RawMessage `json:"events"`
		EventsJSON  json.RawMessage `json:"events_json"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON body")
		return
	}
	name := strings.TrimSpace(req.ClientName)
	if name == "" {
		name = strings.TrimSpace(req.ClientName2)
	}
	url := strings.TrimSpace(req.URL)
	if name == "" {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "client_name is required")
		return
	}
	if url == "" {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "url is required")
		return
	}
	if err := webhook.ValidateURL(url, s.cfg.AllowInsecureWebhooks, s.cfg.AllowPrivateWebhooks); err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	rawEvents := req.Events
	if len(rawEvents) == 0 || string(rawEvents) == "null" {
		rawEvents = req.EventsJSON
	}
	events, ok := normalizeEvents(rawEvents)
	if !ok {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "events must be an array or comma-separated string")
		return
	}
	if len(events) == 0 {
		events = []string{"invoice.paid", "invoice.failed", "invoice.pending"}
	}

	created, err := s.st.CreateClientWithWebhook(name, url, events)
	if err != nil {
		s.log.Error("create client", "error", err)
		writeErr(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to create client")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"message": "Client registered",
		"client": map[string]any{
			"client_id":     created.Client.ClientID,
			"client_name":   created.Client.ClientName,
			"status":        created.Client.Status,
			"registered_at": created.Client.RegisteredAt,
			"client_secret": created.Secret,
			"client_pin":    created.PIN,
			"client_keys":   created.Keys,
		},
		"webhook": map[string]any{
			"webhook_id": created.Webhook.WebhookID,
			"url":        created.Webhook.URL,
			"events":     created.Webhook.Events,
		},
		"secret_callback": map[string]any{
			"value":          created.SecretCallback,
			"length":         len(created.SecretCallback),
			"format":         "no_ambiguous_mixed",
			"displayed_once": true,
			"store_now":      "Simpan secret ini sekarang. Tidak bisa diambil ulang.",
		},
	})
}

func (s *Server) handleClient(w http.ResponseWriter, r *http.Request) {
	client := clientFrom(r.Context())
	if client == nil {
		writeErr(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid client")
		return
	}

	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/client/rotate/secret-callback":
		value, err := s.st.RotateSecretCallback(client.ClientID)
		if err != nil {
			writeErr(w, http.StatusNotFound, "NOT_FOUND", "Client not found or not active")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":        true,
			"message":   "Secret callback rotated",
			"client_id": client.ClientID,
			"secret_callback": map[string]any{
				"value":          value,
				"length":         len(value),
				"format":         "no_ambiguous_mixed",
				"displayed_once": true,
				"store_now":      "Simpan secret ini sekarang. Tidak bisa diambil ulang.",
			},
		})

	case r.Method == http.MethodPost && r.URL.Path == "/client/rotate/url-callback":
		var req map[string]json.RawMessage
		if err := decodeJSON(w, r, &req); err != nil {
			writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON body")
			return
		}
		webhookID := rawString(req["webhook_id"])
		if webhookID == "" {
			webhookID = rawString(req["webhookId"])
		}
		if webhookID == "" {
			writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "webhook_id is required")
			return
		}
		url := rawString(req["url"])
		eventsRaw, hasEvents := req["events"]
		if url == "" && !hasEvents {
			writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "url and/or events is required")
			return
		}
		if url != "" {
			if err := webhook.ValidateURL(url, s.cfg.AllowInsecureWebhooks, s.cfg.AllowPrivateWebhooks); err != nil {
				writeErr(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
				return
			}
		}
		var events []string
		if hasEvents {
			var ok bool
			events, ok = normalizeEvents(eventsRaw)
			if !ok {
				writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "events must be an array or comma-separated string")
				return
			}
		}
		updated, err := s.st.UpdateWebhook(client.ClientID, webhookID, url, events, hasEvents)
		if err != nil {
			switch err {
			case store.ErrForbidden:
				writeErr(w, http.StatusForbidden, "FORBIDDEN", "Failed to update webhook")
			case store.ErrNotFound:
				writeErr(w, http.StatusNotFound, "NOT_FOUND", "Failed to update webhook")
			default:
				writeErr(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to update webhook")
			}
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "Webhook updated", "webhook": updated})

	default:
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "route not found")
	}
}

func normalizeEvents(raw json.RawMessage) ([]string, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, true
	}
	var arr []string
	if err := json.Unmarshal(raw, &arr); err == nil {
		return dedupeStrings(arr), true
	}
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		single = strings.ReplaceAll(single, "\n", " ")
		single = strings.ReplaceAll(single, "\r", " ")
		single = strings.ReplaceAll(single, "\t", " ")
		parts := strings.Split(single, ",")
		if len(parts) == 1 {
			parts = strings.Fields(single)
		}
		return dedupeStrings(parts), true
	}
	return nil, false
}

func dedupeStrings(items []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	return out
}

func rawString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return strings.TrimSpace(s)
}
