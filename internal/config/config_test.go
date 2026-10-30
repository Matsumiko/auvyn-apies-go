package config

import (
	"net/netip"
	"testing"
	"time"
)

func validConfig() *Config {
	return &Config{
		Port:                5882,
		CenterURL:           "http://center",
		MemberID:            "M",
		PIN:                 "1",
		Password:            "p",
		AuvynSecret:         "secret",
		CallbackTimeout:     time.Second,
		DeliveryMaxAttempts: 1,
		EwalletBebasUnit:    1000,
		RetentionDays:       90,
	}
}

func TestValidateRequiresReportGuard(t *testing.T) {
	c := validConfig()
	if err := c.Validate(); err == nil {
		t.Fatal("expected error when /report has no guard")
	}
	c.ReportAllowIPs = []netip.Prefix{netip.MustParsePrefix("203.0.113.10/32")}
	if err := c.Validate(); err != nil {
		t.Fatalf("allowlist should satisfy the guard: %v", err)
	}
	c.ReportAllowIPs = nil
	c.ReportToken = "token"
	if err := c.Validate(); err != nil {
		t.Fatalf("token should satisfy the guard: %v", err)
	}
	c.ReportToken = ""
	c.AllowInsecureReport = true
	if err := c.Validate(); err != nil {
		t.Fatalf("explicit insecure opt-in should pass: %v", err)
	}
}

func TestValidateRejectsNegativeRetention(t *testing.T) {
	c := validConfig()
	c.AllowInsecureReport = true
	c.RetentionDays = -1
	if err := c.Validate(); err == nil {
		t.Fatal("expected retention error")
	}
}

func TestValidateAdminBotConfig(t *testing.T) {
	c := validConfig()
	c.AllowInsecureReport = true
	c.Telegram.AdminToken = "token"
	c.Telegram.AdminAPIBase = "https://api.telegram.org"
	if err := c.Validate(); err == nil {
		t.Fatal("expected error when admin chat IDs are missing")
	}
	c.Telegram.AdminChatIDs = []int64{42}
	if err := c.Validate(); err != nil {
		t.Fatalf("valid admin bot config rejected: %v", err)
	}
	c.Telegram.AdminToken = ""
	if err := c.Validate(); err == nil {
		t.Fatal("expected error when token is missing but chat IDs are set")
	}
}

func TestParseChatIDs(t *testing.T) {
	ids, err := parseChatIDs("42, -100123, 42")
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != 42 || ids[1] != -100123 {
		t.Fatalf("ids = %v", ids)
	}
	if _, err := parseChatIDs("42,abc"); err == nil {
		t.Fatal("expected invalid chat ID error")
	}
}
