package ipcenter

import (
	"regexp"
	"strings"
)

type ProviderResult struct {
	State     string `json:"state"`
	Code      string `json:"code"`
	TrxID     string `json:"trxid"`
	Tujuan    string `json:"tujuan"`
	SN        string `json:"sn"`
	Duplicate bool   `json:"duplicate"`
	Raw       string `json:"raw"`
}

type Balance struct {
	Raw       string `json:"raw"`
	Remaining int64  `json:"remaining"`
}

type otomaxPattern struct {
	code      string
	state     string
	re        *regexp.Regexp
	duplicate bool
}

// Order is part of the provider contract.
var otomaxPatterns = []otomaxPattern{
	{code: "PENDING_AKAN_DIPROSES", state: "PENDING", re: regexp.MustCompile(`(?i)R(?<trxid>\d+).*\.(?<tujuan>.+) akan diproses`)},
	{code: "PENDING_MENUNGGU_SEBELUMNYA", state: "PENDING", re: regexp.MustCompile(`(?i)R(?<trxid>\d+).*\.(?<tujuan>.+) @`)},
	{code: "SUKSES", state: "SUCCESS", re: regexp.MustCompile(`(?i)R(?<trxid>\d+).*\.(?<tujuan>.+) SUKSES.*SN\/Ref:\s*(?<sn>.+)\. M-Bal`)},
	{code: "SUKSES_SUDAH_PERNAH", state: "SUCCESS", duplicate: true, re: regexp.MustCompile(`(?i)R#(?<trxid>\d+).*\.(?<tujuan>.+) sdh.*SN\/Ref:\s*(?<sn>.+)\. M-Bal`)},
	{code: "GAGAL_TUJUAN_SALAH", state: "FAILED", re: regexp.MustCompile(`(?i)R#(?<trxid>\d+).*\.(?<tujuan>.+) GAGAL\. Nomor tujuan salah\.`)},
	{code: "GAGAL_TIMEOUT", state: "FAILED", re: regexp.MustCompile(`(?i)R(?<trxid>\d+).*\.(?<tujuan>\d+) GAGAL karena timeout`)},
	{code: "GAGAL_SALAH_KODE", state: "FAILED", re: regexp.MustCompile(`(?i)R(?<trxid>\d+).*\.(?<tujuan>\d+) GAGAL, salah`)},
	{code: "GAGAL_BLACKLIST", state: "FAILED", re: regexp.MustCompile(`(?i)R#(?<trxid>\d+).*\.(?<tujuan>\d+) sdh pernah jam .*status Nomor Blacklist`)},
	{code: "GAGAL_PRODUK_GANGGUAN", state: "FAILED", re: regexp.MustCompile(`(?i)R(?<trxid>\d+).*\.(?<tujuan>.+) GAGAL\. Produk sedang gangguan\.`)},
	{code: "GAGAL_SALDO_TIDAK_CUKUP", state: "FAILED", re: regexp.MustCompile(`(?i)R(?<trxid>\d+).*\.(?<tujuan>\d+) GAGAL\. Saldo tidak cukup`)},
	{code: "GAGAL_DIKARENAKAN", state: "FAILED", re: regexp.MustCompile(`(?i)R(?<trxid>\d+).*\.(?<tujuan>\d+) GAGAL dikarenakan`)},
	// Inject format (some providers prepend "Inject ...").
	{code: "INJECT_GAGAL", state: "FAILED", re: regexp.MustCompile(`(?i)\bInject\b[\s\S]*\bGAGAL\b`)},
	{code: "INJECT_SUKSES", state: "SUCCESS", re: regexp.MustCompile(`(?i)\bInject\b[\s\S]*\bSUKSES\b`)},
	// Additional Otomax format observed in field deployments:
	//   #<trxid> <PRODUCT>.<dest> SUKSES. SN/Ref: <sn>. Saldo ...
	//   #<trxid> <PRODUCT>.<dest> akan diproses @hh:mm. Saldo ...
	//   [<ref>] <PRODUCT>.<dest> GAGAL ... / #T<trxid> ... gagal, dibatalkan
	{code: "PENDING_AKAN_DIPROSES", state: "PENDING", re: regexp.MustCompile(`(?i)^\s*#(?<trxid>\d+)[a-z]*\s+(?<product>[A-Z0-9]+)\.(?<tujuan>\d+)\s+akan diproses`)},
	{code: "SUKSES", state: "SUCCESS", re: regexp.MustCompile(`(?i)^\s*#(?<trxid>\d+)[a-z]*\s+(?<product>[A-Z0-9]+)\.(?<tujuan>\d+)\s+SUKSES\.\s*SN/Ref:\s*(?<sn>[^.\s]+)`)},
	{code: "GAGAL_DIBATALKAN", state: "FAILED", re: regexp.MustCompile(`(?is)^\s*#T(?<trxid>\d+)[a-z]*\s+(?<product>[A-Z0-9]+)\.(?<tujuan>\d+).*?gagal,\s*dibatalkan`)},
	{code: "GAGAL", state: "FAILED", re: regexp.MustCompile(`(?i)^\s*(?:'[^']*'\s*)?(?<product>[A-Z0-9]+)\.(?<tujuan>\d+)\s+GAGAL`)},
	{code: "GAGAL", state: "FAILED", re: regexp.MustCompile(`(?i)(?<product>[A-Z0-9]+)\.(?<tujuan>\d+)\s+GAGAL`)},
	{code: "CEK_TIDAK_ADA_DATA", state: "NOT_FOUND", re: regexp.MustCompile(`(?i)TIDAK ADA transaksi Tujuan "(?<tujuan>\d+)" pada tgl (?<tanggal>\d{2}\/\d{2}\/\d{2})\. Tidak ada data\.`)},
	{code: "CEK_SUKSES", state: "SUCCESS", re: regexp.MustCompile(`(?im)\b(?<kode>[A-Z0-9]+)\s+\S*\.(?<tujuan>\d+)\s+\d{2}:\d{2}\s+Sukses\s+SN:\s*(?<sn>.+)$`)},
}

var (
	balanceFull    = regexp.MustCompile(`(?i)M-Bal\s*:\s*([\d.,]+)\s*[–\-]\s*([\d.,]+)\s*=\s*([\d.,]+)`)
	balanceSimple  = regexp.MustCompile(`(?i)M-Bal\s*:\s*([\d.,]+)`)
	saldoFull      = regexp.MustCompile(`(?i)Saldo\s+([\d.,]+)\s*[–\-]\s*([\d.,]+)\s*=\s*([\d.,]+)`)
	saldoSimple    = regexp.MustCompile(`(?i)Saldo\s+([\d.,]+)`)
	ticketPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\bTICKET\s*[:#]?\s*([A-Za-z0-9-]{4,})`),
		regexp.MustCompile(`(?i)\bTIKET\s*[:#]?\s*([A-Za-z0-9-]{4,})`),
		regexp.MustCompile(`(?i)\bKODE\s*[:#]?\s*([A-Za-z0-9-]{4,})`),
	}
)

// ParseMessage applies the ordered Otomax patterns.
func ParseMessage(message string) ProviderResult {
	text := strings.TrimSpace(message)
	for _, p := range otomaxPatterns {
		m := p.re.FindStringSubmatch(text)
		if m == nil {
			continue
		}
		return ProviderResult{
			State:     p.state,
			Code:      p.code,
			TrxID:     group(p.re, m, "trxid"),
			Tujuan:    group(p.re, m, "tujuan"),
			SN:        group(p.re, m, "sn"),
			Duplicate: p.duplicate,
			Raw:       text,
		}
	}
	return ProviderResult{State: "UNKNOWN", Code: "UNKNOWN", Raw: text}
}

func group(re *regexp.Regexp, m []string, name string) string {
	i := re.SubexpIndex(name)
	if i < 0 || i >= len(m) {
		return ""
	}
	return m[i]
}

// ClassifyTransaction mirrors the legacy fallback rules.
func ClassifyTransaction(rawText string, httpStatus int) string {
	if httpStatus != 200 {
		return "failed"
	}
	parsed := ParseMessage(rawText)
	switch parsed.State {
	case "SUCCESS":
		return "success"
	case "FAILED", "NOT_FOUND":
		return "failed"
	case "PENDING":
		return "pending"
	}
	t := strings.ToLower(rawText)
	switch {
	case strings.Contains(t, "gagal"), strings.Contains(t, "failed"), strings.Contains(t, "reject"):
		return "failed"
	case (strings.Contains(t, "akan diproses") || strings.Contains(t, "menunggu jawaban") ||
		strings.Contains(t, "sedang diproses") || strings.Contains(t, "proses")) &&
		!strings.Contains(t, "sukses") && !strings.Contains(t, "berhasil"):
		return "pending"
	case strings.Contains(t, "sukses"), strings.Contains(t, "berhasil"), strings.Contains(t, "ok"):
		return "success"
	default:
		return "pending"
	}
}

// ClassifyReport gives explicit failure text precedence over the numeric
// status code. This is an intentional v2 fix: status=20 + "GAGAL" is failed.
func ClassifyReport(message, status string) string {
	m := strings.ToLower(message)
	st := strings.TrimSpace(status)
	switch {
	case strings.Contains(m, "gagal"), strings.Contains(m, "failed"),
		strings.Contains(m, "error"), strings.Contains(m, "reject"):
		return "failed"
	case st == "20", strings.Contains(m, "sukses"), strings.Contains(m, "success"):
		return "success"
	}
	parsed := ParseMessage(message)
	switch parsed.State {
	case "SUCCESS":
		return "success"
	case "FAILED", "NOT_FOUND":
		return "failed"
	}
	return "pending"
}

// ParseBalance extracts the remaining M-Bal / saldo from a provider message.
func ParseBalance(rawText string) *Balance {
	if strings.TrimSpace(rawText) == "" {
		return nil
	}
	var remaining string
	if m := balanceFull.FindStringSubmatch(rawText); m != nil {
		remaining = m[3]
	} else if m := balanceSimple.FindStringSubmatch(rawText); m != nil {
		remaining = m[1]
	} else if m := saldoFull.FindStringSubmatch(rawText); m != nil {
		remaining = m[3]
	} else if m := saldoSimple.FindStringSubmatch(rawText); m != nil {
		remaining = m[1]
	}
	if remaining == "" {
		return nil
	}
	var b strings.Builder
	for _, r := range remaining {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return nil
	}
	var n int64
	for _, r := range b.String() {
		n = n*10 + int64(r-'0')
	}
	if n == 0 && b.String() != "0" {
		return nil
	}
	return &Balance{Raw: remaining, Remaining: n}
}

// ParseTicket is best-effort: the provider ticket format is not documented.
func ParseTicket(rawText string) string {
	for _, re := range ticketPatterns {
		if m := re.FindStringSubmatch(rawText); m != nil {
			return strings.TrimRight(m[1], ".,;:")
		}
	}
	return ""
}
