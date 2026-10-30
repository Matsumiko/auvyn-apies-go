// Package ipcenter implements the frozen IP Center (Otomax-style) wire
// protocol: signatures, outbound query strings, response parsing and
// sanitisation. Nothing in this package may be "cleaned up" without evidence
// from the provider — see CONTRACT.md.
package ipcenter

import (
	"crypto/sha1"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

var ErrQtyRequired = errors.New("qty or nominal is required for bebas products")

// BuildSignature returns base64url(SHA1(prefix|member|product|dest|refID|pin|password[|nominal])).
func BuildSignature(prefix, memberID, product, dest, refID, pin, password string, nominal *int64, includeNominal bool) string {
	template := prefix + "|" + memberID + "|" + product + "|" + dest + "|" + refID + "|" + pin + "|" + password
	if includeNominal && nominal != nil {
		template += "|" + strconv.FormatInt(*nominal, 10)
	}
	return b64URLSHA1(template)
}

// BuildCheckBalanceSignature returns base64url(SHA1(prefix|CheckBalance|member|pin|password)).
// This template is required by Otomax-style /balance endpoints.
func BuildCheckBalanceSignature(prefix, memberID, pin, password string) string {
	return b64URLSHA1(prefix + "|CheckBalance|" + memberID + "|" + pin + "|" + password)
}

// BuildTicketSignature returns base64url(SHA1(prefix|ticket|member|pin|password|amount)).
func BuildTicketSignature(prefix, memberID, pin, password string, amount int64) string {
	return b64URLSHA1(prefix + "|ticket|" + memberID + "|" + pin + "|" + password + "|" + strconv.FormatInt(amount, 10))
}

func b64URLSHA1(template string) string {
	sum := sha1.Sum([]byte(template))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// ParseNominalToInt reproduces the legacy behaviour: stringify, strip every
// non-digit, then require > 0. "15.000" -> 15000, "15000.5" -> 15005.
func ParseNominalToInt(v any) *int64 {
	var s string
	switch x := v.(type) {
	case nil:
		return nil
	case string:
		s = x
	case float64:
		s = strconv.FormatFloat(x, 'f', -1, 64)
	case float32:
		s = strconv.FormatFloat(float64(x), 'f', -1, 64)
	case int:
		s = strconv.Itoa(x)
	case int64:
		s = strconv.FormatInt(x, 10)
	case jsonNumber:
		s = x.String()
	default:
		s = fmt.Sprint(x)
	}
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return nil
	}
	n, err := strconv.ParseInt(b.String(), 10, 64)
	if err != nil || n <= 0 {
		return nil
	}
	return &n
}

// jsonNumber avoids importing encoding/json just for the interface type.
type jsonNumber interface{ String() string }

// QtyFromNominal converts a nominal to provider qty.
func QtyFromNominal(nominal int64, qtyIsNominal bool, unit int64) int64 {
	if qtyIsNominal {
		return nominal
	}
	if unit < 1 {
		unit = 1
	}
	return nominal / unit
}

// ResolveQty implements the provider qty rules. A nil raw value means "not
// provided" (derive or default); the string "none" explicitly omits qty.
func ResolveQty(raw any, nominal *int64, bebas, qtyIsNominal bool, unit int64) (*int64, error) {
	if s, ok := raw.(string); ok && strings.EqualFold(strings.TrimSpace(s), "none") {
		return nil, nil
	}
	if n := numberValue(raw); n > 0 {
		v := int64(n)
		return &v, nil
	}
	if bebas && nominal != nil {
		q := QtyFromNominal(*nominal, qtyIsNominal, unit)
		if q > 0 {
			return &q, nil
		}
	}
	if !bebas {
		v := int64(1)
		return &v, nil
	}
	return nil, ErrQtyRequired
}

func numberValue(raw any) float64 {
	switch x := raw.(type) {
	case float64:
		return x
	case float32:
		return float64(x)
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case string:
		n, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		if err != nil {
			return 0
		}
		return n
	default:
		return 0
	}
}
