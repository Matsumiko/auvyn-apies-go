package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Matsumiko/auvyn-apies-go/internal/ipcenter"
	"github.com/Matsumiko/auvyn-apies-go/internal/security"
	"github.com/Matsumiko/auvyn-apies-go/internal/store"
)

func (s *Server) handlePing(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "message": "pong", "time": time.Now().UTC(),
	})
}

func (s *Server) handleTransaction(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Product string         `json:"product"`
		Dest    string         `json:"dest"`
		Qty     any            `json:"qty"`
		RefID   string         `json:"refID"`
		Nominal any            `json:"nominal"`
		Meta    map[string]any `json:"meta"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON body")
		return
	}
	req.Product = strings.TrimSpace(req.Product)
	req.Dest = strings.TrimSpace(req.Dest)
	if req.Product == "" || req.Dest == "" {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "product and dest are required")
		return
	}
	client := clientFrom(r.Context())
	if client != nil {
		if req.Meta == nil {
			req.Meta = map[string]any{}
		}
		if provided := extractClientID(req.Meta); provided != "" && provided != client.ClientID {
			writeErr(w, http.StatusForbidden, "FORBIDDEN", "meta.client_id does not match authenticated client")
			return
		}
		req.Meta["client_id"] = client.ClientID
	}

	bebas := s.cfg.IsBebasProduct(req.Product)
	nominal := ipcenter.ParseNominalToInt(req.Nominal)
	nominalProvided := hasValue(req.Nominal)
	if bebas && nominal == nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "nominal is required for bebas products")
		return
	}
	if !bebas && nominalProvided && nominal == nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "nominal must be a positive number when provided")
		return
	}

	refID := strings.TrimSpace(req.RefID)
	if refID == "" {
		refID = security.RandomRefID()
	}
	clientID := extractClientID(req.Meta)

	if existing, err := s.st.GetTransaction(refID); err == nil {
		if client != nil && existing.ClientID != client.ClientID {
			writeErr(w, http.StatusConflict, "DUPLICATE_REF", "refID already exists")
			return
		}
		s.respondExisting(w, existing, req.Product, req.Dest)
		return
	} else if !errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to check refID")
		return
	}

	// Insert an intent row before charging the provider so a duplicate refID
	// can never reach the center twice.
	if err := s.st.InsertTransaction(store.Transaction{
		RefID: refID, ClientID: clientID, Product: req.Product, Dest: req.Dest,
		Category: "pending", Meta: req.Meta,
	}); err != nil {
		if errors.Is(err, store.ErrDuplicateRef) {
			writeErr(w, http.StatusConflict, "DUPLICATE_REF", "refID already exists")
			return
		}
		s.log.Error("insert transaction intent", "refID", refID, "error", err)
		writeErr(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to record transaction")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	res, err := s.ip.SendTransaction(ctx, ipcenter.TransactionRequest{
		Product: req.Product, Dest: req.Dest, Qty: req.Qty, RefID: refID,
		Nominal: req.Nominal, Meta: req.Meta,
	})
	if err != nil {
		// A provider timeout is ambiguous: the request may have been received.
		// Keep the intent row so a retry with the same refID cannot charge
		// twice; only pure validation errors are rolled back.
		if errors.Is(err, ipcenter.ErrQtyRequired) {
			_ = s.st.DeletePendingTransaction(refID)
			s.log.Error("transaction rejected", "refID", refID, "error", err)
			writeErr(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
			return
		}
		s.log.Error("transaction failed; intent kept for reconciliation", "refID", refID, "error", err)
		writeErr(w, http.StatusBadGateway, "CENTER_ERROR", "failed to call center")
		return
	}

	if applied, err := s.st.UpdateTransactionResult(refID, res.Category, res.Qty, res.Nominal,
		res.Provider.State, res.Provider.Code, res.Provider.TrxID, res.Provider.SN, res.Raw); err != nil {
		s.log.Error("update transaction result", "refID", refID, "error", err)
	} else if !applied {
		s.log.Warn("transaction was finalised before request update", "refID", refID)
	}

	callbackPayload := ipcenter.SanitizeTransaction(*res, false)
	s.enqueueLegacy(refID, "transaction.request", "transaction.request:"+refID, callbackPayload)
	s.enqueueManaged(refID, managedEventForCategory(res.Category),
		"invoice:"+res.Category+":"+refID, callbackPayload, clientID)

	s.tg.NotifyTrx(res.Category, res.RefID, res.Product, res.Dest, res.Qty, res.StatusCode,
		res.Raw, res.Balance, res.Meta)
	if res.Balance != nil && s.cfg.BalanceLowLimit > 0 && res.Balance.Remaining <= s.cfg.BalanceLowLimit {
		s.tg.NotifyLowBalance(res.Balance)
	}

	writeJSON(w, http.StatusOK, ipcenter.SanitizeTransaction(*res, true))
}

// respondExisting makes caller retries after a lost response idempotent: a
// transaction that already reached the provider is replayed, never re-sent.
// An in-flight placeholder (no provider outcome yet) returns 409.
// handleTransactionStatus returns the client-scoped ledger state for a refID.
// Clients can only read transactions that belong to their authenticated client.
func (s *Server) handleTransactionStatus(w http.ResponseWriter, r *http.Request) {
	refID := strings.TrimSpace(r.URL.Query().Get("refID"))
	if refID == "" {
		refID = strings.TrimSpace(r.URL.Query().Get("refId"))
	}
	if r.Method == http.MethodPost {
		var req struct {
			RefID      string `json:"refID"`
			RefIDAlt   string `json:"refId"`
			RefIDLower string `json:"refid"`
		}
		if err := decodeOptional(w, r, &req); err != nil {
			writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON body")
			return
		}
		if req.RefID != "" {
			refID = req.RefID
		} else if req.RefIDAlt != "" {
			refID = req.RefIDAlt
		} else if req.RefIDLower != "" {
			refID = req.RefIDLower
		}
		refID = strings.TrimSpace(refID)
	}
	if refID == "" {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "refID is required")
		return
	}
	client := clientFrom(r.Context())
	if client == nil {
		writeErr(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid client")
		return
	}
	txn, err := s.st.GetTransaction(refID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "NOT_FOUND", "transaction not found")
			return
		}
		s.log.Error("read transaction status", "refID", refID, "error", err)
		writeErr(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to read transaction")
		return
	}
	if txn.ClientID != client.ClientID {
		// Do not reveal whether another client's refID exists.
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "transaction not found")
		return
	}
	writeJSON(w, http.StatusOK, transactionStatusPayload(txn))
}

func transactionStatusPayload(txn *store.Transaction) map[string]any {
	raw := txn.RequestRaw
	if raw == "" {
		raw = txn.ReportRaw
	}
	out := map[string]any{
		"ok":       true,
		"refID":    txn.RefID,
		"product":  txn.Product,
		"dest":     txn.Dest,
		"qty":      txn.Qty,
		"nominal":  txn.Nominal,
		"category": txn.Category,
		"providerResult": sanitizedProvider(ipcenter.ProviderResult{
			State: txn.ProviderState, Code: txn.ProviderCode, TrxID: txn.ProviderTrxID,
			SN: txn.SN, Raw: raw,
		}),
		"meta":      txn.Meta,
		"createdAt": txn.CreatedAt,
		"updatedAt": txn.UpdatedAt,
	}
	if txn.FinalAt != nil {
		out["finalAt"] = txn.FinalAt
	} else {
		out["finalAt"] = nil
	}
	return out
}

func (s *Server) respondExisting(w http.ResponseWriter, txn *store.Transaction, product, dest string) {
	if txn.Product != product || txn.Dest != dest {
		writeErr(w, http.StatusConflict, "DUPLICATE_REF", "refID already exists with different product/dest")
		return
	}
	if txn.Category == "pending" && txn.ProviderState == "" && txn.RequestRaw == "" {
		writeErr(w, http.StatusConflict, "IN_PROGRESS", "transaction with this refID is being processed")
		return
	}
	res := ipcenter.TransactionResult{
		OK: true, StatusCode: 200, RefID: txn.RefID, Product: txn.Product, Dest: txn.Dest,
		Qty: txn.Qty, Nominal: txn.Nominal, Category: txn.Category, Raw: txn.RequestRaw,
		Meta: txn.Meta, Balance: ipcenter.ParseBalance(txn.RequestRaw), ReceivedAt: txn.UpdatedAt,
		Provider: ipcenter.ProviderResult{
			State: txn.ProviderState, Code: txn.ProviderCode, TrxID: txn.ProviderTrxID,
			SN: txn.SN, Raw: txn.RequestRaw,
		},
	}
	writeJSON(w, http.StatusOK, ipcenter.SanitizeTransaction(res, true))
}

func (s *Server) handleBalance(w http.ResponseWriter, r *http.Request) {
	var req struct {
		MemberID string         `json:"memberId"`
		Meta     map[string]any `json:"meta"`
	}
	if err := decodeOptional(w, r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON body")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	res, err := s.ip.CheckBalance(ctx, strings.TrimSpace(req.MemberID), req.Meta)
	if err != nil {
		s.log.Error("balance check failed", "error", err)
		writeErr(w, http.StatusBadGateway, "CENTER_ERROR", "failed to check balance")
		return
	}

	var remaining *int64
	var balanceRaw string
	if res.Balance != nil {
		v := res.Balance.Remaining
		remaining = &v
		balanceRaw = res.Balance.Raw
	}
	payload := map[string]any{
		"ok":             res.OK,
		"statusCode":     res.StatusCode,
		"memberId":       res.MemberID,
		"balance":        remaining,
		"balanceRaw":     balanceRaw,
		"providerResult": sanitizedProvider(res.Provider),
		"meta":           res.Meta,
		"receivedAt":     res.ReceivedAt,
	}
	s.enqueueLegacy("", "balance.check", "balance.check:"+security.GenerateToken(16, security.NoAmbiguousMixed),
		payload)
	if res.Balance != nil && s.cfg.BalanceLowLimit > 0 && res.Balance.Remaining <= s.cfg.BalanceLowLimit {
		s.tg.NotifyLowBalance(res.Balance)
	}
	writeJSON(w, http.StatusOK, payload)
}

func (s *Server) handleTicket(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Amount   any            `json:"amount"`
		MemberID string         `json:"memberId"`
		Meta     map[string]any `json:"meta"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON body")
		return
	}
	amountPtr := ipcenter.ParseNominalToInt(req.Amount)
	if amountPtr == nil || *amountPtr <= 0 {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "amount must be greater than 0")
		return
	}
	amount := *amountPtr

	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	res, err := s.ip.CreateTicket(ctx, amount, strings.TrimSpace(req.MemberID), req.Meta)
	if err != nil {
		s.log.Error("ticket creation failed", "error", err)
		writeErr(w, http.StatusBadGateway, "CENTER_ERROR", "failed to create ticket")
		return
	}

	payload := map[string]any{
		"ok":             res.OK,
		"statusCode":     res.StatusCode,
		"memberId":       res.MemberID,
		"ticket":         res.Ticket,
		"amount":         res.Amount,
		"providerResult": sanitizedProvider(res.Provider),
		"meta":           res.Meta,
		"receivedAt":     res.ReceivedAt,
	}
	s.enqueueLegacy("", "balance.ticket", "balance.ticket:"+security.GenerateToken(16, security.NoAmbiguousMixed),
		payload)
	writeJSON(w, http.StatusOK, payload)
}

func hasValue(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(x) != ""
	default:
		return true
	}
}

func decodeOptional(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	body, err := readLimited(r, maxBody)
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return nil
	}
	return json.Unmarshal(body, dst)
}
