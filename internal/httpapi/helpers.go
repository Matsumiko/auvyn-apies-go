package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/Matsumiko/auvyn-apies-go/internal/ipcenter"
	"github.com/Matsumiko/auvyn-apies-go/internal/store"
)

const maxBody = 128 << 10

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"ok": false, "error": code, "message": message})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain a single JSON object")
	}
	return nil
}

func readLimited(r *http.Request, max int64) ([]byte, error) {
	return io.ReadAll(io.LimitReader(r.Body, max))
}

func sanitizedProvider(p ipcenter.ProviderResult) ipcenter.SanitizedProvider {
	return ipcenter.SanitizedProvider{
		State:     p.State,
		Code:      p.Code,
		TrxID:     p.TrxID,
		Tujuan:    p.Tujuan,
		SN:        p.SN,
		Duplicate: p.Duplicate,
		Raw:       ipcenter.RedactRawForClient(p.Raw),
	}
}

func extractClientID(meta map[string]any) string {
	if len(meta) == 0 {
		return ""
	}
	for _, k := range []string{"client_id", "clientId", "clientID"} {
		if v, ok := meta[k]; ok {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
	}
	return ""
}

func (s *Server) enqueueLegacy(refID, kind, dedupeKey string, payload any) {
	body, err := json.Marshal(payload)
	if err != nil {
		s.log.Error("marshal event payload", "kind", kind, "error", err)
		return
	}
	var items []store.Delivery
	for _, t := range s.cfg.CallbackTargets {
		items = append(items, store.Delivery{
			TargetKind: "legacy", TargetName: t.Name, URL: t.URL, EventName: kind,
		})
	}
	if _, inserted, err := s.st.InsertEventAndDeliveries(refID, kind, dedupeKey, string(body), items); err != nil {
		s.log.Error("enqueue legacy event", "kind", kind, "refID", refID, "error", err)
	} else if !inserted {
		s.log.Info("legacy event already enqueued", "kind", kind, "dedupeKey", dedupeKey)
	}
}

// enqueueManaged uses its own dedupe key so a final invoice event is delivered
// exactly once even if the provider reports it both immediately and later.
func (s *Server) enqueueManaged(refID, eventName, dedupeKey string, payload any, clientID string) {
	if !s.cfg.ManagedWebhooksEnabled || clientID == "" || eventName == "" {
		return
	}
	body, err := json.Marshal(payload)
	if err != nil {
		s.log.Error("marshal managed event payload", "event", eventName, "error", err)
		return
	}
	_, hooks, err := s.st.WebhooksForEvent(clientID, eventName)
	if err != nil {
		s.log.Warn("load managed webhooks", "clientId", clientID, "error", err)
		return
	}
	var items []store.Delivery
	for _, h := range hooks {
		items = append(items, store.Delivery{
			TargetKind: "managed", TargetName: clientID + ":" + h.WebhookID,
			ClientID: clientID, WebhookID: h.WebhookID, URL: h.URL, EventName: eventName,
		})
	}
	if _, inserted, err := s.st.InsertEventAndDeliveries(refID, eventName, dedupeKey, string(body), items); err != nil {
		s.log.Error("enqueue managed event", "event", eventName, "refID", refID, "error", err)
	} else if !inserted {
		s.log.Info("managed event already enqueued", "event", eventName, "dedupeKey", dedupeKey)
	}
}

func managedEventForCategory(category string) string {
	switch category {
	case "success":
		return "invoice.paid"
	case "failed":
		return "invoice.failed"
	default:
		return "invoice.pending"
	}
}
