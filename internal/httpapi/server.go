package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/Matsumiko/auvyn-apies-go/internal/config"
	"github.com/Matsumiko/auvyn-apies-go/internal/ipcenter"
	"github.com/Matsumiko/auvyn-apies-go/internal/notify"
	"github.com/Matsumiko/auvyn-apies-go/internal/store"
	"github.com/Matsumiko/auvyn-apies-go/internal/version"
	"github.com/Matsumiko/auvyn-apies-go/internal/webhook"
)

type Server struct {
	cfg       *config.Config
	st        *store.Store
	ip        *ipcenter.Client
	disp      *webhook.Dispatcher
	tg        *notify.Notifier
	log       *slog.Logger
	startedAt time.Time
}

func New(cfg *config.Config, st *store.Store, ip *ipcenter.Client, disp *webhook.Dispatcher,
	tg *notify.Notifier, log *slog.Logger) *Server {
	return &Server{cfg: cfg, st: st, ip: ip, disp: disp, tg: tg, log: log, startedAt: time.Now().UTC()}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /{$}", s.handleHealth)
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /readyz", s.handleReady)
	mux.HandleFunc("GET /version", s.handleVersion)

	mux.Handle("POST /api/ping", s.requireSecret(http.HandlerFunc(s.handlePing)))
	mux.Handle("POST /api/transaction", s.requireTransactionAuth(http.HandlerFunc(s.handleTransaction)))
	mux.Handle("GET /api/transaction/status", s.requireTransactionAuth(http.HandlerFunc(s.handleTransactionStatus)))
	mux.Handle("POST /api/transaction/status", s.requireTransactionAuth(http.HandlerFunc(s.handleTransactionStatus)))
	mux.Handle("POST /api/balance", s.requireSecret(http.HandlerFunc(s.handleBalance)))
	mux.Handle("POST /api/ticket-mbal", s.requireSecret(http.HandlerFunc(s.handleTicket)))

	report := http.HandlerFunc(s.handleReport)
	mux.Handle("/report", report)
	mux.Handle("/report/", report)

	adminV1 := http.NewServeMux()
	adminV1.HandleFunc("GET /clients", s.adminListClients)
	adminV1.HandleFunc("POST /clients", s.adminCreateClient)
	adminV1.HandleFunc("GET /clients/{client_id}", s.adminGetClient)
	adminV1.HandleFunc("DELETE /clients/{client_id}", s.adminDeleteClient)
	adminV1.HandleFunc("GET /clients/{client_id}/webhooks", s.adminListClientWebhooks)
	adminV1.HandleFunc("POST /clients/{client_id}/webhooks", s.adminCreateWebhook)
	adminV1.HandleFunc("POST /clients/{client_id}/callback-secret/rotate", s.adminRotateClientSecret)
	adminV1.HandleFunc("GET /webhooks", s.adminListWebhooks)
	adminV1.HandleFunc("GET /stats", s.adminStatsV1)
	adminV1.HandleFunc("GET /webhooks/{webhook_id}", s.adminGetWebhook)
	adminV1.HandleFunc("PATCH /webhooks/{webhook_id}", s.adminUpdateWebhook)
	adminV1.HandleFunc("DELETE /webhooks/{webhook_id}", s.adminDeleteWebhook)
	adminV1.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeAPIError(w, http.StatusNotFound, "NOT_FOUND", "route not found")
	})
	mux.Handle("/admin/v1/", s.requireAdmin(http.StripPrefix("/admin/v1", adminV1)))

	clientV1 := http.NewServeMux()
	clientV1.HandleFunc("GET /me", s.clientMe)
	clientV1.HandleFunc("GET /webhooks", s.clientListWebhooks)
	clientV1.HandleFunc("PATCH /webhooks/{webhook_id}", s.clientUpdateWebhook)
	clientV1.HandleFunc("DELETE /webhooks/{webhook_id}", s.clientDeleteWebhook)
	clientV1.HandleFunc("POST /callback-secret/rotate", s.clientRotateSecret)
	clientV1.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeAPIError(w, http.StatusNotFound, "NOT_FOUND", "route not found")
	})
	mux.Handle("/client/v1/", s.requireClient(http.StripPrefix("/client/v1", clientV1)))

	mux.Handle("GET /admin/stats", s.requireAdmin(http.HandlerFunc(s.adminStats)))

	admin := http.HandlerFunc(s.handleAdmin)
	mux.Handle("/admin/", s.requireAdmin(deprecated("/admin/v1/clients", admin)))

	client := http.HandlerFunc(s.handleClient)
	mux.Handle("/client/", s.requireClient(deprecated("/client/v1/me", client)))

	mux.HandleFunc("/", s.handleNotFound)

	return s.recoverer(s.accessLog(mux))
}

func (s *Server) Serve(ctx context.Context) error {
	srv := &http.Server{
		Addr:              net.JoinHostPort(s.cfg.Host, strconv.Itoa(s.cfg.Port)),
		Handler:           s.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		s.log.Info("http server listening", "addr", srv.Addr)
		errCh <- srv.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return err
		}
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func (s *Server) handleNotFound(w http.ResponseWriter, r *http.Request) {
	writeErr(w, http.StatusNotFound, "NOT_FOUND", "route not found")
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"name":    "auvyn-apies",
		"message": "IP center bridge online",
		"time":    time.Now().UTC(),
	})
}

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "name": "auvyn-apies", "version": version.Version,
	})
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.st.Ping(ctx); err != nil {
		writeErr(w, http.StatusServiceUnavailable, "DB_UNAVAILABLE", "database is not ready")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (s *Server) accessLog(next http.Handler) http.Handler {
	if !s.cfg.AccessLog {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		s.log.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"durationMs", time.Since(start).Milliseconds(),
			"ip", s.remoteIP(r),
		)
	})
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.log.Error("panic recovered", "error", rec, "path", r.URL.Path)
				writeErr(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// remoteIP prefers proxy headers only when the direct peer is a trusted proxy.
func (s *Server) remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	addr, err := netip.ParseAddr(host)
	if err != nil || !inPrefixes(addr, s.cfg.TrustedProxies) {
		return host
	}
	if xr := strings.TrimSpace(r.Header.Get("X-Real-IP")); xr != "" {
		if a, err := netip.ParseAddr(xr); err == nil {
			return a.String()
		}
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		first := strings.TrimSpace(strings.Split(xff, ",")[0])
		if a, err := netip.ParseAddr(first); err == nil {
			return a.String()
		}
	}
	return host
}

func inPrefixes(addr netip.Addr, prefixes []netip.Prefix) bool {
	addr = addr.Unmap()
	for _, p := range prefixes {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}
