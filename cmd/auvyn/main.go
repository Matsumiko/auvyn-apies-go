// Command auvyn runs the Auvyn Apies bridge.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Matsumiko/auvyn-apies-go/internal/adminbot"
	"github.com/Matsumiko/auvyn-apies-go/internal/config"
	"github.com/Matsumiko/auvyn-apies-go/internal/httpapi"
	"github.com/Matsumiko/auvyn-apies-go/internal/ipcenter"
	"github.com/Matsumiko/auvyn-apies-go/internal/logging"
	"github.com/Matsumiko/auvyn-apies-go/internal/notify"
	"github.com/Matsumiko/auvyn-apies-go/internal/store"
	"github.com/Matsumiko/auvyn-apies-go/internal/version"
	"github.com/Matsumiko/auvyn-apies-go/internal/webhook"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version", "-version", "--version":
			fmt.Println(version.Version)
			return
		case "import-json":
			if len(os.Args) < 3 {
				fmt.Fprintln(os.Stderr, "usage: auvyn import-json <path/to/db.json>")
				os.Exit(2)
			}
			if err := runImport(os.Args[2]); err != nil {
				fmt.Fprintln(os.Stderr, "import failed:", err)
				os.Exit(1)
			}
			return
		case "import-logs":
			if len(os.Args) < 3 {
				fmt.Fprintln(os.Stderr, "usage: auvyn import-logs <log-dir>")
				os.Exit(2)
			}
			if err := runImportLogs(os.Args[2]); err != nil {
				fmt.Fprintln(os.Stderr, "import logs failed:", err)
				os.Exit(1)
			}
			return
		default:
			fmt.Fprintf(os.Stderr, "unknown command %q\n", os.Args[1])
			os.Exit(2)
		}
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "configuration error:", err)
		os.Exit(1)
	}
	log := logging.New(cfg.LogLevel)

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		log.Error("open database", "error", err)
		os.Exit(1)
	}
	defer st.Close()

	notifier := notify.New(cfg, log)
	dispatcher := webhook.New(st, cfg, log)
	dispatcher.OnDead = func(_ context.Context, d store.Delivery, reason string) {
		notifier.NotifyDeadLetter(d.TargetName, logging.SafeURL(d.URL), reason)
	}
	provider := ipcenter.New(cfg, log)
	adminBot := adminbot.New(cfg, st, provider, log)
	srv := httpapi.New(cfg, st, provider, dispatcher, notifier, log)

	if len(cfg.CallbackTargets) == 0 {
		log.Warn("no legacy callback targets configured; callbacks will only go to managed webhooks")
	}
	switch {
	case cfg.AllowInsecureReport:
		log.Warn("ALLOW_INSECURE_REPORT is enabled: /report has no IP allowlist or token")
	case len(cfg.ReportAllowIPs) == 0:
		log.Warn("REPORT_ALLOW_IPS is empty; /report relies on REPORT_TOKEN + refID + destination matching")
	case cfg.ReportToken == "":
		log.Warn("REPORT_TOKEN is empty; /report relies on the IP allowlist + refID + destination matching")
	}
	for _, t := range cfg.CallbackTargets {
		if strings.HasPrefix(t.URL, "http://") && !cfg.AllowInsecureWebhooks {
			log.Warn("callback target uses http and will be blocked; set ALLOW_INSECURE_WEBHOOKS=true if intentional",
				"target", t.Name)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	dispCtx, stopDispatcher := context.WithCancel(ctx)
	defer stopDispatcher()

	var workers sync.WaitGroup
	workers.Add(1)
	go func() {
		defer workers.Done()
		dispatcher.Start(dispCtx)
	}()
	if cfg.RetentionDays > 0 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			runMaintenance(dispCtx, st, log, cfg.RetentionDays)
		}()
	}
	if cfg.TelegramSelfCheck {
		workers.Add(1)
		go func() {
			defer workers.Done()
			notifier.SelfCheck(dispCtx)
		}()
	}
	if adminBot.Enabled() {
		workers.Add(1)
		go func() {
			defer workers.Done()
			adminBot.Run(dispCtx)
		}()
	}

	startupCtx, cancelStartup := context.WithTimeout(ctx, 5*time.Second)
	stats, statErr := st.Stats(startupCtx)
	cancelStartup()
	if statErr != nil {
		log.Warn("read startup stats", "error", statErr)
	}
	log.Info("auvyn-apies starting", "version", version.Version, "db", cfg.DBPath,
		"addr", cfg.Host+":"+fmt.Sprint(cfg.Port),
		"clients", stats.Clients, "webhooks", stats.Webhooks,
		"transactions", stats.TransactionsByCategory,
		"pendingDeliveries", stats.Deliveries["pending"], "deadDeliveries", stats.Deliveries["dead"],
		"retentionDays", cfg.RetentionDays)
	err = srv.Serve(ctx)
	stopDispatcher()
	workers.Wait()
	if err != nil {
		log.Error("server stopped", "error", err)
		os.Exit(1)
	}
	log.Info("auvyn-apies stopped")
}

func runMaintenance(ctx context.Context, st *store.Store, log *slog.Logger, days int) {
	prune := func() {
		pruneCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		res, err := st.Prune(pruneCtx, time.Duration(days)*24*time.Hour)
		if err != nil {
			log.Warn("prune failed", "error", err)
			return
		}
		log.Info("prune complete", "deliveries", res.Deliveries, "events", res.Events,
			"providerReports", res.ProviderReports, "retentionDays", days)
	}
	prune()
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			prune()
		}
	}
}

type logImportResult struct {
	Inserted int `json:"inserted"`
	Merged   int `json:"merged"`
	Skipped  int `json:"skipped"`
}

// runImportLogs backfills transactions from the legacy Node JSONL logs so a
// report that arrives after cutover still matches a known refID.
func runImportLogs(dir string) error {
	st, err := store.Open(dbPathFromEnv())
	if err != nil {
		return err
	}
	defer st.Close()
	var result logImportResult
	// Pending first so the original request raw is kept when a later report
	// row for the same refID is merged.
	for _, name := range []string{"trx-pending.log", "trx-success.log", "trx-failed.log"} {
		rows, err := readLogRows(filepath.Join(dir, name))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		for _, row := range rows {
			txn, ok := rowToTransaction(row)
			if !ok {
				result.Skipped++
				continue
			}
			inserted, err := st.UpsertTransaction(txn)
			if err != nil {
				return fmt.Errorf("%s refID %s: %w", name, txn.RefID, err)
			}
			if inserted {
				result.Inserted++
			} else {
				result.Merged++
			}
		}
	}
	out, _ := json.MarshalIndent(result, "", "  ")
	fmt.Printf("imported logs from %s: %s\n", dir, out)
	return nil
}

func dbPathFromEnv() string {
	if dbPath := os.Getenv("DB_PATH"); dbPath != "" {
		return dbPath
	}
	dataDir := os.Getenv("DATA_DIR")
	if dataDir == "" {
		dataDir = "./data"
	}
	return filepath.Join(dataDir, "auvyn.db")
}

func readLogRows(path string) ([]map[string]any, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var rows []map[string]any
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64<<10), 2<<20)
	for scanner.Scan() {
		line := scanner.Bytes()
		var row map[string]any
		if err := json.Unmarshal(line, &row); err != nil {
			continue
		}
		if inner, ok := row["message"].(map[string]any); ok {
			row = inner
		}
		rows = append(rows, row)
	}
	return rows, scanner.Err()
}

func rowToTransaction(row map[string]any) (store.Transaction, bool) {
	refID, _ := row["refID"].(string)
	product := stringValue(row["product"])
	dest := stringValue(row["dest"])
	// Report rows logged with an unknown refID have no product/dest; they are
	// audit noise, not transactions.
	if refID == "" || product == "" || dest == "" {
		return store.Transaction{}, false
	}
	category, _ := row["category"].(string)
	provider, _ := row["providerResult"].(map[string]any)
	if category == "" && provider != nil {
		switch provider["state"] {
		case "SUCCESS":
			category = "success"
		case "FAILED":
			category = "failed"
		default:
			category = "pending"
		}
	}
	if category == "" {
		category = "pending"
	}
	meta, _ := row["meta"].(map[string]any)
	txn := store.Transaction{
		RefID:         refID,
		ClientID:      metaString(meta, "client_id"),
		Product:       product,
		Dest:          dest,
		Qty:           intPtr(row["qty"]),
		Nominal:       intPtr(row["nominal"]),
		Category:      category,
		ProviderState: metaString(provider, "state"),
		ProviderCode:  metaString(provider, "code"),
		ProviderTrxID: metaString(provider, "trxid"),
		SN:            metaString(provider, "sn"),
		RequestRaw:    stringValue(row["raw"]),
		Meta:          meta,
	}
	// Historical rows were written by the old parser (often UNKNOWN). Re-parse
	// the raw message so trxid/SN/destination are usable for late reports.
	if parsed := ipcenter.ParseMessage(txn.RequestRaw); parsed.State != "UNKNOWN" {
		if txn.ProviderState == "" || txn.ProviderState == "UNKNOWN" {
			txn.ProviderState = parsed.State
		}
		if txn.ProviderCode == "" || txn.ProviderCode == "UNKNOWN" {
			txn.ProviderCode = parsed.Code
		}
		if txn.ProviderTrxID == "" {
			txn.ProviderTrxID = parsed.TrxID
		}
		if txn.SN == "" {
			txn.SN = parsed.SN
		}
	}
	if ts, ok := row["receivedAt"].(string); ok {
		if v, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			txn.CreatedAt = v
			txn.UpdatedAt = v
		}
	}
	return txn, true
}

func metaString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	s, _ := m[key].(string)
	return s
}

func stringValue(v any) string {
	s, _ := v.(string)
	return s
}

func intPtr(v any) *int64 {
	f, ok := v.(float64)
	if !ok || f <= 0 {
		return nil
	}
	n := int64(f)
	return &n
}

func runImport(path string) error {
	st, err := store.Open(dbPathFromEnv())
	if err != nil {
		return err
	}
	defer st.Close()
	result, err := st.ImportLegacyJSON(path)
	if err != nil {
		return err
	}
	out, _ := json.MarshalIndent(result, "", "  ")
	fmt.Printf("imported into %s: %s\n", st.Path(), out)
	return nil
}
