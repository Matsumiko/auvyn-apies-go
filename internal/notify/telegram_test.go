package notify

import (
	"errors"
	"net/url"
	"strings"
	"testing"
)

func TestTruncate(t *testing.T) {
	if got := truncate("abcdef", 4); got != "abcd…" {
		t.Errorf("got %q", got)
	}
	if got := truncate("abc", 4); got != "abc" {
		t.Errorf("got %q", got)
	}
	// rune-safe, not byte-safe
	if got := truncate("éééé", 2); got != "éé…" {
		t.Errorf("got %q", got)
	}
}

func TestSafeErrHidesBotToken(t *testing.T) {
	err := &url.Error{Op: "Get", URL: "https://api.telegram.org/botSECRET/getChat", Err: errors.New("boom")}
	got := safeErr(err)
	if strings.Contains(got, "SECRET") {
		t.Errorf("token leaked: %q", got)
	}
	if !strings.Contains(got, "boom") {
		t.Errorf("underlying error lost: %q", got)
	}
}
