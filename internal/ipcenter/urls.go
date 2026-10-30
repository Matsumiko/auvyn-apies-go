package ipcenter

import (
	"strconv"
	"strings"
)

type pair struct{ k, v string }

// encodeComponent matches JavaScript encodeURIComponent for the characters
// that matter here. url.QueryEscape differs (space -> '+', escapes !'()*),
// which would change the wire format.
func encodeComponent(s string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	b.Grow(len(s))
	for i := range len(s) {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9',
			c == '-', c == '_', c == '.', c == '!', c == '~', c == '*', c == '\'', c == '(', c == ')':
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&0x0f])
		}
	}
	return b.String()
}

func buildQuery(pairs []pair) string {
	parts := make([]string, 0, len(pairs))
	for _, p := range pairs {
		if p.v == "" {
			continue
		}
		parts = append(parts, encodeComponent(p.k)+"="+encodeComponent(p.v))
	}
	return strings.Join(parts, "&")
}

// TransactionURL builds the frozen /trx URL. mode bebas sends credentials in
// the query and never sends nominal; non-bebas sends nominal when present.
func TransactionURL(base string, bebas bool, memberID, product, dest string, qty *int64, refID, pin, password, sign string, nominal *int64) string {
	qtyStr := ""
	if qty != nil {
		qtyStr = strconv.FormatInt(*qty, 10)
	}
	if bebas {
		q := buildQuery([]pair{
			{"memberID", memberID},
			{"product", product},
			{"dest", dest},
			{"pin", pin},
			{"qty", qtyStr},
			{"refID", refID},
			{"password", password},
			{"sign", sign},
		})
		return base + "/trx?" + q
	}
	if qtyStr == "" {
		qtyStr = "1"
	}
	var nominalStr string
	if nominal != nil {
		nominalStr = strconv.FormatInt(*nominal, 10)
	}
	q := buildQuery([]pair{
		{"product", product},
		{"dest", dest},
		{"qty", qtyStr},
		{"refID", refID},
		{"memberID", memberID},
		{"sign", sign},
		{"nominal", nominalStr},
	})
	return base + "/trx?" + q
}

func BalanceURL(base, memberID, pin, password, sign string) string {
	q := buildQuery([]pair{
		{"memberID", memberID},
		{"memberid", memberID},
		{"pin", pin},
		{"password", password},
		{"sign", sign},
	})
	return base + "/balance?" + q
}

func TicketURL(base, memberID string, amount int64, sign string) string {
	q := buildQuery([]pair{
		{"cmd", "ticket"},
		{"memberid", memberID},
		{"amount", strconv.FormatInt(amount, 10)},
		{"sign", sign},
	})
	return base + "/?" + q
}
