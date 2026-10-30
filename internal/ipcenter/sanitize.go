package ipcenter

import (
	"regexp"
	"strings"
	"time"
)

var (
	reHRG      = regexp.MustCompile(`(?i)HRG:[^@]+@`)
	reMBal     = regexp.MustCompile(`(?i)\s*M-?Bal\s*:[^@#]+`)
	reInject   = regexp.MustCompile(`(?is)#Inject[\s\S]*$`)
	reMemberID = regexp.MustCompile(`(?i)memberID=\w+`)
	reSign     = regexp.MustCompile(`(?i)sign=[^&\s]+`)
	reTrxURL   = regexp.MustCompile(`(?i)https?://[^\s]+/trx\?[^\s]+`)
	reSNRef    = regexp.MustCompile(`(?i)SN/Ref:\s*[^.\n]*`)
	reSN       = regexp.MustCompile(`(?i)\bSN:\s*[^\s.]+`)
	reSaldo    = regexp.MustCompile(`(?i)\bSaldo\s+(?:refund\s+)?[\d.,]+\s*[–-]\s*[\d.,]+\s*=\s*[\d.,]+`)
	reSaldoOne = regexp.MustCompile(`(?i)\bSaldo\s+(?:refund\s+)?[\d.,]+`)
	reSal      = regexp.MustCompile(`(?i)\bSal\s+[\d.,]+`)
	reSpaces   = regexp.MustCompile(`\s{2,}`)
	reTime     = regexp.MustCompile(`@(\d{1,2}/\d{1,2}\s+)?\d{1,2}:\d{2}(:\d{2})?`)
)

// RedactRaw removes provider secrets and identifiers from a raw message before
// it reaches logs or other untrusted sinks. Serial numbers are redacted too.
func RedactRaw(rawText string) string {
	return redactRaw(rawText, true)
}

// RedactRawForClient keeps the serial number because the owning client needs
// it to fulfil the order. Credentials and balances are still removed.
func RedactRawForClient(rawText string) string {
	return redactRaw(rawText, false)
}

func redactRaw(rawText string, hideSN bool) string {
	t := rawText
	t = reHRG.ReplaceAllString(t, "@")
	t = reMBal.ReplaceAllString(t, "")
	t = reInject.ReplaceAllString(t, "")
	t = reMemberID.ReplaceAllString(t, "memberID=[REDACTED]")
	t = reSign.ReplaceAllString(t, "sign=[REDACTED]")
	t = reTrxURL.ReplaceAllString(t, "[REDACTED_URL]")
	if hideSN {
		t = reSNRef.ReplaceAllString(t, "SN/Ref: [REDACTED]")
		t = reSN.ReplaceAllString(t, "SN: [REDACTED]")
	}
	// Production messages expose balances as "Saldo a - b = c", "Saldo refund x",
	// or "Sal x". Logs keep a marker; client payloads drop the clause entirely.
	balanceMarker := ""
	if hideSN {
		balanceMarker = "[REDACTED]"
	}
	t = reSaldo.ReplaceAllString(t, balanceMarker)
	t = reSaldoOne.ReplaceAllString(t, balanceMarker)
	t = reSal.ReplaceAllString(t, balanceMarker)
	if times := reTime.FindAllString(t, -1); len(times) > 0 {
		last := times[len(times)-1]
		if idx := strings.LastIndex(t, last); idx != -1 {
			t = t[:idx+len(last)]
		}
	}
	t = reSpaces.ReplaceAllString(t, " ")
	return strings.TrimSpace(t)
}

type SanitizedProvider struct {
	State     string `json:"state"`
	Code      string `json:"code"`
	TrxID     string `json:"trxid"`
	Tujuan    string `json:"tujuan"`
	SN        string `json:"sn"`
	Duplicate bool   `json:"duplicate"`
	Raw       string `json:"raw"`
}

type SanitizedTransaction struct {
	OK             bool              `json:"ok"`
	StatusCode     int               `json:"statusCode"`
	RefID          string            `json:"refID"`
	Product        string            `json:"product"`
	Dest           string            `json:"dest"`
	Qty            *int64            `json:"qty"`
	Nominal        *int64            `json:"nominal"`
	Category       string            `json:"category"`
	ReceivedAt     time.Time         `json:"receivedAt"`
	Meta           map[string]any    `json:"meta"`
	ProviderResult SanitizedProvider `json:"providerResult"`
	Raw            string            `json:"raw"`
	Balance        *Balance          `json:"balanceInfo,omitempty"`
}

// SanitizeTransaction strips sign/centerUrl/memberID and redacts raw. Balance
// is opt-in because callbacks historically excluded it, while /api responses
// need it.
func SanitizeTransaction(full TransactionResult, includeBalance bool) SanitizedTransaction {
	out := SanitizedTransaction{
		OK:         full.OK,
		StatusCode: full.StatusCode,
		RefID:      full.RefID,
		Product:    full.Product,
		Dest:       full.Dest,
		Qty:        full.Qty,
		Nominal:    full.Nominal,
		Category:   full.Category,
		ReceivedAt: full.ReceivedAt,
		Meta:       full.Meta,
		ProviderResult: SanitizedProvider{
			State:     full.Provider.State,
			Code:      full.Provider.Code,
			TrxID:     full.Provider.TrxID,
			Tujuan:    full.Provider.Tujuan,
			SN:        full.Provider.SN,
			Duplicate: full.Provider.Duplicate,
			Raw:       RedactRawForClient(full.Provider.Raw),
		},
		Raw: RedactRawForClient(full.Raw),
	}
	if includeBalance && full.Balance != nil {
		out.Balance = full.Balance
	}
	return out
}
