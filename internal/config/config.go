// Package config loads and validates all runtime configuration from the
// environment. It fails fast: a service that cannot talk to the provider or
// protect its own API must not start.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Telegram struct {
	SuccessToken    string
	SuccessChatID   string
	SuccessThreadID int64
	PendingToken    string
	PendingChatID   string
	PendingThreadID int64
	FailedToken     string
	FailedChatID    string
	FailedThreadID  int64
	SystemToken     string
	SystemChatID    string
	SystemThreadID  int64
	BalanceThreadID int64

	// Admin bot (long polling). Empty token disables it.
	AdminToken   string
	AdminChatIDs []int64
	AdminAPIBase string
}

// CallbackTarget is a legacy (env-configured) callback destination.
type CallbackTarget struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	Secret string `json:"secret"`
}

type Config struct {
	Host string
	Port int

	CenterURL  string
	MemberID   string
	PIN        string
	Password   string
	SignPrefix string

	AuvynSecret string

	AdminSecret string
	AdminPIN    string
	AdminKeys   string

	DataDir string
	DBPath  string

	CallbackURL     string
	CallbackSecret  string
	CallbackTargets []CallbackTarget

	ManagedWebhooksEnabled bool
	CallbackTimeout        time.Duration
	DeliveryMaxAttempts    int
	DeliveryBackoff        []time.Duration

	ReportToken         string
	ReportAllowIPs      []netip.Prefix
	TrustedProxies      []netip.Prefix
	AllowInsecureReport bool

	BalanceLowLimit int64

	EwalletBebasProducts     []string
	EwalletBebasQtyIsNominal bool
	EwalletBebasUnit         int64
	SignIncludeNominal       bool

	AllowInsecureWebhooks bool
	AllowPrivateWebhooks  bool

	AccessLog         bool
	RetentionDays     int
	TelegramSelfCheck bool

	Telegram Telegram

	LogLevel string
}

func Load() (*Config, error) {
	c := &Config{
		Host: get("HOST", "127.0.0.1"),
		Port: getInt("PORT", 5882),

		CenterURL:  strings.TrimRight(get("CENTER_URL", ""), "/"),
		MemberID:   get("MEMBER_ID", ""),
		PIN:        getRaw("PIN", ""),
		Password:   getRaw("PASSWORD", ""),
		SignPrefix: get("SIGN_PREFIX", "ENGINE"),

		AuvynSecret: getRaw("AUVYN_SECRET", ""),
		AdminSecret: getRaw("ADMIN_SECRET", ""),
		AdminPIN:    getRaw("ADMIN_PIN", ""),
		AdminKeys:   getRaw("ADMIN_KEYS", ""),

		DataDir:     get("DATA_DIR", "./data"),
		CallbackURL: get("CALLBACK_URL", ""),
		ReportToken: getRaw("REPORT_TOKEN", ""),

		ManagedWebhooksEnabled: getBool("MANAGED_WEBHOOKS_ENABLED", true),
		CallbackTimeout:        getDuration("CALLBACK_TIMEOUT_MS", 15*time.Second, time.Millisecond),
		DeliveryMaxAttempts:    getInt("DELIVERY_MAX_ATTEMPTS", 6),

		BalanceLowLimit: getInt64("BALANCE_LOW_LIMIT", 50000),

		EwalletBebasQtyIsNominal: getBool("EWALLET_BEBAS_QTY_IS_NOMINAL", true),
		EwalletBebasUnit:         getInt64("EWALLET_BEBAS_UNIT", 1000),
		SignIncludeNominal:       getBool("SIGN_INCLUDE_NOMINAL", false),

		AllowInsecureWebhooks: getBool("ALLOW_INSECURE_WEBHOOKS", false),
		AllowPrivateWebhooks:  getBool("ALLOW_PRIVATE_WEBHOOKS", false),
		AllowInsecureReport:   getBool("ALLOW_INSECURE_REPORT", false),
		AccessLog:             getBool("ACCESS_LOG", true),
		RetentionDays:         getInt("RETENTION_DAYS", 365),
		TelegramSelfCheck:     getBool("TELEGRAM_SELFCHECK", true),

		LogLevel: get("LOG_LEVEL", "info"),
	}

	c.DBPath = get("DB_PATH", filepath.Join(c.DataDir, "auvyn.db"))
	c.CallbackSecret = getRaw("CALLBACK_SECRET", "")
	if c.CallbackSecret == "" {
		c.CallbackSecret = c.AuvynSecret
	}

	if raw := get("EWALLET_BEBAS_PRODUCTS", ""); raw != "" {
		for _, p := range strings.Split(raw, ",") {
			if p = strings.TrimSpace(p); p != "" {
				c.EwalletBebasProducts = append(c.EwalletBebasProducts, p)
			}
		}
	}

	c.Telegram = Telegram{
		SuccessToken:    get("TG_SUCCESS_BOT_TOKEN", ""),
		SuccessChatID:   get("TG_SUCCESS_CHAT_ID", ""),
		SuccessThreadID: getInt64("TG_SUCCESS_THREAD_ID", 0),
		PendingToken:    get("TG_PENDING_BOT_TOKEN", ""),
		PendingChatID:   get("TG_PENDING_CHAT_ID", ""),
		PendingThreadID: getInt64("TG_PENDING_THREAD_ID", 0),
		FailedToken:     get("TG_FAILED_BOT_TOKEN", ""),
		FailedChatID:    get("TG_FAILED_CHAT_ID", ""),
		FailedThreadID:  getInt64("TG_FAILED_THREAD_ID", 0),
		SystemToken:     get("TG_SYSTEM_BOT_TOKEN", ""),
		SystemChatID:    get("TG_SYSTEM_CHAT_ID", ""),
		SystemThreadID:  getInt64("TG_SYSTEM_THREAD_ID", 0),
		BalanceThreadID: getInt64("TG_BALANCE_THREAD_ID", 0),
		AdminToken:      getRaw("TG_ADMIN_BOT_TOKEN", ""),
		AdminAPIBase:    get("TG_ADMIN_API_BASE", "https://api.telegram.org"),
	}
	if raw := get("TG_ADMIN_CHAT_IDS", ""); raw != "" {
		ids, err := parseChatIDs(raw)
		if err != nil {
			return nil, fmt.Errorf("TG_ADMIN_CHAT_IDS: %w", err)
		}
		c.Telegram.AdminChatIDs = ids
	}

	if err := c.loadCallbackTargets(); err != nil {
		return nil, err
	}

	var err error
	if c.ReportAllowIPs, err = parsePrefixes(get("REPORT_ALLOW_IPS", "")); err != nil {
		return nil, fmt.Errorf("REPORT_ALLOW_IPS: %w", err)
	}
	if c.TrustedProxies, err = parsePrefixes(get("TRUSTED_PROXY_CIDRS", "127.0.0.1/32,::1/128")); err != nil {
		return nil, fmt.Errorf("TRUSTED_PROXY_CIDRS: %w", err)
	}
	if c.DeliveryBackoff, err = parseDurations(get("WEBHOOK_BACKOFF", "1m,5m,30m,2h,6h,24h")); err != nil {
		return nil, fmt.Errorf("WEBHOOK_BACKOFF: %w", err)
	}

	if err := c.Validate(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Config) loadCallbackTargets() error {
	if c.CallbackURL != "" {
		c.CallbackTargets = append(c.CallbackTargets, CallbackTarget{
			Name: "default", URL: c.CallbackURL, Secret: c.CallbackSecret,
		})
	}
	if raw := get("CALLBACK_TARGETS", ""); raw != "" {
		var parsed []CallbackTarget
		if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
			return fmt.Errorf("CALLBACK_TARGETS: %w", err)
		}
		for _, t := range parsed {
			if t.URL == "" {
				continue
			}
			if t.Name == "" {
				t.Name = t.URL
			}
			c.CallbackTargets = append(c.CallbackTargets, t)
		}
	}
	for _, legacy := range []struct{ env, name, secretEnv string }{
		{"CALLBACK_WEB_URL", "web-worker", "CALLBACK_WEB_SECRET"},
		{"CALLBACK_BOT_URL", "bot-worker", "CALLBACK_BOT_SECRET"},
		{"CALLBACK_THIRD_URL", "third-worker", "CALLBACK_THIRD_SECRET"},
	} {
		if u := get(legacy.env, ""); u != "" {
			c.CallbackTargets = append(c.CallbackTargets, CallbackTarget{
				Name: legacy.name, URL: u, Secret: get(legacy.secretEnv, ""),
			})
		}
	}
	return nil
}

func (c *Config) Validate() error {
	var errs []error
	require := func(name, value string) {
		if strings.TrimSpace(value) == "" {
			errs = append(errs, fmt.Errorf("%s is required", name))
		}
	}
	require("CENTER_URL", c.CenterURL)
	require("MEMBER_ID", c.MemberID)
	require("PIN", c.PIN)
	require("PASSWORD", c.Password)
	require("AUVYN_SECRET", c.AuvynSecret)
	if c.Port < 1 || c.Port > 65535 {
		errs = append(errs, fmt.Errorf("PORT must be between 1 and 65535"))
	}
	if c.EwalletBebasUnit < 1 {
		errs = append(errs, fmt.Errorf("EWALLET_BEBAS_UNIT must be >= 1"))
	}
	if c.DeliveryMaxAttempts < 1 {
		errs = append(errs, fmt.Errorf("DELIVERY_MAX_ATTEMPTS must be >= 1"))
	}
	if c.CallbackTimeout <= 0 {
		errs = append(errs, fmt.Errorf("CALLBACK_TIMEOUT_MS must be > 0"))
	}
	if c.RetentionDays < 0 {
		errs = append(errs, fmt.Errorf("RETENTION_DAYS must be >= 0"))
	}
	if c.Telegram.AdminToken != "" {
		if len(c.Telegram.AdminChatIDs) == 0 {
			errs = append(errs, fmt.Errorf("TG_ADMIN_CHAT_IDS is required when TG_ADMIN_BOT_TOKEN is set"))
		}
		if strings.TrimSpace(c.Telegram.AdminAPIBase) == "" {
			errs = append(errs, fmt.Errorf("TG_ADMIN_API_BASE must not be empty"))
		}
	} else if len(c.Telegram.AdminChatIDs) > 0 {
		errs = append(errs, fmt.Errorf("TG_ADMIN_BOT_TOKEN is required when TG_ADMIN_CHAT_IDS is set"))
	}
	// Secure by default: a public /report endpoint must have at least one
	// control, because reverse proxies often expose every path.
	if c.ReportToken == "" && len(c.ReportAllowIPs) == 0 && !c.AllowInsecureReport {
		errs = append(errs, fmt.Errorf(
			"report guard is not configured: set REPORT_ALLOW_IPS or REPORT_TOKEN (or ALLOW_INSECURE_REPORT=true for local development)"))
	}
	return errors.Join(errs...)
}

// IsBebasProduct reports whether product uses nominal-as-qty mode.
func (c *Config) IsBebasProduct(product string) bool {
	up := strings.ToUpper(strings.TrimSpace(product))
	for _, p := range c.EwalletBebasProducts {
		if strings.ToUpper(strings.TrimSpace(p)) == up {
			return true
		}
	}
	return false
}

func get(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return strings.TrimSpace(v)
	}
	return def
}

// getRaw preserves whitespace for credentials and secrets.
func getRaw(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}

func getInt(key string, def int) int {
	if v, ok := os.LookupEnv(key); ok {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
	}
	return def
}

func getInt64(key string, def int64) int64 {
	if v, ok := os.LookupEnv(key); ok {
		if n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
			return n
		}
	}
	return def
}

func getBool(key string, def bool) bool {
	if v, ok := os.LookupEnv(key); ok {
		b, err := strconv.ParseBool(strings.TrimSpace(v))
		if err == nil {
			return b
		}
	}
	return def
}

func getDuration(key string, def time.Duration, unit time.Duration) time.Duration {
	if v, ok := os.LookupEnv(key); ok {
		if n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
			return time.Duration(n) * unit
		}
	}
	return def
}

func parsePrefixes(raw string) ([]netip.Prefix, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var out []netip.Prefix
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if p, err := netip.ParsePrefix(part); err == nil {
			out = append(out, p.Masked())
			continue
		}
		addr, err := netip.ParseAddr(part)
		if err != nil {
			return nil, fmt.Errorf("invalid IP or CIDR %q", part)
		}
		out = append(out, netip.PrefixFrom(addr, addr.BitLen()))
	}
	return out, nil
}

func parseChatIDs(raw string) ([]int64, error) {
	seen := map[int64]bool{}
	var out []int64
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, err := strconv.ParseInt(part, 10, 64)
		if err != nil || id == 0 {
			return nil, fmt.Errorf("invalid chat ID %q", part)
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no valid chat IDs")
	}
	return out, nil
}

func parseDurations(raw string) ([]time.Duration, error) {
	var out []time.Duration
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		d, err := time.ParseDuration(part)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	if len(out) == 0 {
		return nil, errors.New("at least one backoff duration is required")
	}
	return out, nil
}
