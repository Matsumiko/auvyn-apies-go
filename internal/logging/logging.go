// Package logging configures structured logging. Logs go to stdout so the
// process manager / journald owns rotation. Callers must never pass raw URLs
// containing credentials; helpers here redact known sensitive query params.
package logging

import (
	"log/slog"
	"net/url"
	"os"
	"strings"
)

func New(level string) *slog.Logger {
	var lv slog.Level
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		lv = slog.LevelDebug
	case "warn", "warning":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		lv = slog.LevelInfo
	}
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lv})
	return slog.New(h)
}

// SafeURL returns scheme://host/path without query or fragment. Provider URLs
// carry pin/password/sign in the query and must never be logged verbatim.
func SafeURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "[unparsable-url]"
	}
	return u.Scheme + "://" + u.Host + u.Path
}
