package httpapi

import (
	"encoding/json"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Matsumiko/auvyn-apies-go/internal/ipcenter"
	"github.com/Matsumiko/auvyn-apies-go/internal/security"
	"github.com/Matsumiko/auvyn-apies-go/internal/store"
)

const maxReportBody = 64 << 10

// handleReport accepts the provider's final report. The provider cannot add
// headers or tokens, so identity comes from the IP allowlist plus the
// unguessable refID and the stored destination.
func (s *Server) handleReport(w http.ResponseWriter, r *http.Request) {
	ip := s.remoteIP(r)

	if len(s.cfg.ReportAllowIPs) > 0 {
		if !ipAllowed(ip, s.cfg.ReportAllowIPs) {
			s.log.Warn("report rejected: source IP not allowed", "ip", ip)
			writeErr(w, http.StatusForbidden, "FORBIDDEN", "source IP not allowed")
			return
		}
	}
	if s.cfg.ReportToken != "" {
		provided := r.URL.Query().Get("key")
		if provided == "" {
			provided = r.URL.Query().Get("token")
		}
		if provided == "" {
			provided = r.Header.Get("x-report-token")
		}
		if !security.ConstantTimeEqual(provided, s.cfg.ReportToken) {
			s.log.Warn("report rejected: invalid token", "ip", ip)
			writeErr(w, http.StatusForbidden, "FORBIDDEN", "invalid report token")
			return
		}
	}

	body, err := readLimited(r, maxReportBody)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "failed to read body")
		return
	}
	fields := collectReportFields(r, body)

	refID := strings.TrimSpace(first(fields, "refid", "refID"))
	message := first(fields, "message")
	status := first(fields, "status")

	record := store.ProviderReport{
		RefID: refID, RemoteIP: ip, Method: r.Method,
		Query: redactQuery(r.URL), Body: string(body), RawMessage: message,
		ReceivedAt: time.Now().UTC(),
	}

	if refID == "" {
		record.Reason = "missing_ref"
		s.saveReportRecord(record)
		s.log.Warn("report rejected: missing refID", "ip", ip)
		writePlainOK(w)
		return
	}

	txn, err := s.st.GetTransaction(refID)
	if err != nil {
		record.Reason = "unknown_ref"
		s.saveReportRecord(record)
		s.log.Error("report rejected: unknown refID", "refID", refID, "ip", ip)
		s.tg.NotifyReport(refID, r.Method, ip, message)
		writePlainOK(w)
		return
	}

	provider := ipcenter.ParseMessage(message)
	if provider.Tujuan != "" && digitsOnly(provider.Tujuan) != digitsOnly(txn.Dest) {
		record.Reason = "dest_mismatch"
		record.Category = provider.State
		s.saveReportRecord(record)
		s.log.Error("report rejected: destination mismatch", "refID", refID,
			"expected", txn.Dest, "got", provider.Tujuan, "ip", ip)
		s.tg.NotifyReport(refID, r.Method, ip, message)
		writePlainOK(w)
		return
	}

	category := ipcenter.ClassifyReport(message, status)
	record.Category = category
	result, err := s.st.ApplyReport(refID, category, provider.State, provider.Code,
		provider.TrxID, provider.SN, message)
	if err != nil {
		s.log.Error("apply report", "refID", refID, "error", err)
		writePlainOK(w)
		return
	}

	switch result {
	case store.ApplyApplied:
		record.Accepted = true
		record.Reason = "applied"
		s.saveReportRecord(record)
		s.log.Info("report applied", "refID", refID, "category", category, "ip", ip)

		updated, err := s.st.GetTransaction(refID)
		if err != nil {
			updated = txn
		}
		payload := ipcenter.SanitizedTransaction{
			OK:             category != "failed",
			StatusCode:     http.StatusOK,
			RefID:          refID,
			Product:        updated.Product,
			Dest:           updated.Dest,
			Qty:            updated.Qty,
			Nominal:        updated.Nominal,
			Category:       category,
			ReceivedAt:     time.Now().UTC(),
			Meta:           updated.Meta,
			ProviderResult: sanitizedProvider(provider),
			Raw:            ipcenter.RedactRawForClient(message),
		}
		s.enqueueLegacy(refID, "transaction.report", "transaction.report:"+refID+":"+category, payload)
		s.enqueueManaged(refID, managedEventForCategory(category),
			"invoice:"+category+":"+refID, payload, updated.ClientID)
		s.tg.NotifyTrx(category, refID, updated.Product, updated.Dest, updated.Qty,
			http.StatusOK, message, nil, updated.Meta)
		s.tg.NotifyReport(refID, r.Method, ip, message)

	case store.ApplyDuplicate:
		record.Accepted = true
		record.Reason = "duplicate"
		s.saveReportRecord(record)
		s.log.Info("report duplicate ignored", "refID", refID, "category", category)

	case store.ApplyConflict:
		record.Accepted = false
		record.Reason = "conflict"
		s.saveReportRecord(record)
		s.log.Error("report conflict: final status already set", "refID", refID,
			"incoming", category, "ip", ip)
		s.tg.NotifyReport(refID, r.Method, ip, "CONFLICT "+message)

	default:
		record.Accepted = false
		record.Reason = string(result)
		s.saveReportRecord(record)
		s.log.Warn("report not applied", "refID", refID, "result", result)
	}

	writePlainOK(w)
}

func (s *Server) saveReportRecord(record store.ProviderReport) {
	if err := s.st.InsertProviderReport(record); err != nil {
		s.log.Error("save provider report", "refID", record.RefID, "error", err)
	}
}

func writePlainOK(w http.ResponseWriter) {
	w.Header().Set("content-type", "text/plain")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("OK"))
}

func ipAllowed(ip string, prefixes []netip.Prefix) bool {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	return inPrefixes(addr, prefixes)
}

func redactQuery(u *url.URL) string {
	q := u.Query()
	q.Del("key")
	q.Del("token")
	return q.Encode()
}

func collectReportFields(r *http.Request, body []byte) url.Values {
	values := url.Values{}
	for k, v := range r.URL.Query() {
		for _, item := range v {
			values.Add(k, item)
		}
	}
	add := func(k, v string) {
		if v == "" || values.Has(k) {
			return
		}
		values.Set(k, v)
	}
	ct := strings.ToLower(r.Header.Get("content-type"))
	addJSON := func() {
		var m map[string]any
		if json.Unmarshal(body, &m) == nil {
			for k, v := range m {
				switch x := v.(type) {
				case string:
					add(k, x)
				case float64:
					add(k, strconv.FormatFloat(x, 'f', -1, 64))
				}
			}
		}
	}
	switch {
	case strings.Contains(ct, "application/json"):
		addJSON()
	default:
		parsed, err := url.ParseQuery(string(body))
		if err == nil && strings.Contains(string(body), "=") {
			for k, v := range parsed {
				for _, item := range v {
					add(k, item)
				}
			}
		} else {
			addJSON()
		}
	}
	// Last resort: a provider that posts an unstructured message body.
	if values.Get("refid") == "" && values.Get("refID") == "" && values.Get("message") == "" && len(body) > 0 {
		values.Set("message", string(body))
	}
	return values
}

func first(values url.Values, keys ...string) string {
	for _, k := range keys {
		if v := values.Get(k); v != "" {
			return v
		}
	}
	return ""
}

func digitsOnly(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}
