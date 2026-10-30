package ipcenter

import (
	"strings"
	"testing"
)

func TestRedactRaw(t *testing.T) {
	raw := "R1.081234 SUKSES. SN/Ref: SN-ABC. M-Bal: 100.000 - 50.000 = 50.000 memberID=AG1 sign=SECRET"
	got := RedactRaw(raw)
	for _, forbidden := range []string{"SN-ABC", "100.000", "50.000", "AG1", "SECRET"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("redacted raw still contains %q: %q", forbidden, got)
		}
	}
	if !strings.Contains(got, "SN/Ref: [REDACTED]") {
		t.Errorf("missing SN redaction marker: %q", got)
	}
}

func TestRedactRawHRGAndInject(t *testing.T) {
	if got := RedactRaw("HRG:10000@081234"); strings.Contains(got, "10000") {
		t.Errorf("HRG not redacted: %q", got)
	}
	if got := RedactRaw("pesan#Inject script"); strings.Contains(got, "script") {
		t.Errorf("inject tail not removed: %q", got)
	}
	if got := RedactRaw("lihat http://host/trx?memberID=1&sign=x sekarang"); strings.Contains(got, "memberID=1") {
		t.Errorf("trx URL not redacted: %q", got)
	}
}

func TestRedactFieldBalances(t *testing.T) {
	cases := []string{
		"#123456789 GAME100.081234567890 SUKSES. SN/Ref: SN-ABC123. Saldo 100.000 - 25.000 = 75.000 @22/02 09:46:48",
		"#123456789 GAME100.081234567890 akan diproses @09:46. Saldo 100.000 - 25.000 = 75.000",
		"#T555666777 PULSA10.081234567893 pd 12-06 23:40:12 gagal, dibatalkan. Saldo refund 10.000. Sal 80.000",
	}
	for _, raw := range cases {
		got := RedactRaw(raw)
		for _, forbidden := range []string{"SN-ABC123", "100.000", "25.000", "75.000", "10.000", "80.000"} {
			if strings.Contains(got, forbidden) {
				t.Errorf("raw still contains %q: %q", forbidden, got)
			}
		}
		if !strings.Contains(got, "gagal") && !strings.Contains(got, "SUKSES") && !strings.Contains(got, "akan diproses") {
			t.Errorf("redaction removed classification text: %q", got)
		}
	}
}

func TestRedactRawForClientKeepsSN(t *testing.T) {
	raw := "R1.081234 SUKSES. SN/Ref: SN-ABC. M-Bal: 100.000 - 50.000 = 50.000 memberID=AG1 sign=SECRET"
	got := RedactRawForClient(raw)
	if !strings.Contains(got, "SN/Ref: SN-ABC") {
		t.Errorf("client raw lost SN: %q", got)
	}
	if strings.Contains(got, "[REDACTED]") {
		t.Errorf("client raw should drop balance marker: %q", got)
	}
	for _, forbidden := range []string{"100.000", "50.000", "AG1", "SECRET"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("client raw still contains %q: %q", forbidden, got)
		}
	}
}

func TestSanitizeTransactionKeepsSNButHidesSecrets(t *testing.T) {
	full := TransactionResult{
		OK: true, StatusCode: 200, RefID: "AVN1", Product: "P", Dest: "D",
		Category: "success",
		Raw:      "R1.081 SUKSES. SN/Ref: SN-ABC. M-Bal: 1 - 1 = 0",
		Provider: ProviderResult{
			State: "SUCCESS", Code: "SUKSES", SN: "SN-ABC",
			Raw: "R1.081 SUKSES. SN/Ref: SN-ABC. M-Bal: 1 - 1 = 0",
		},
		Balance: &Balance{Raw: "0", Remaining: 0},
	}
	got := SanitizeTransaction(full, false)
	if got.ProviderResult.SN != "SN-ABC" {
		t.Errorf("sn = %q", got.ProviderResult.SN)
	}
	for _, s := range []string{got.Raw, got.ProviderResult.Raw} {
		if !strings.Contains(s, "SN-ABC") {
			t.Errorf("client raw lost SN: %q", s)
		}
		if strings.Contains(s, "M-Bal: 1 - 1 = 0") {
			t.Errorf("client raw leaked balance: %q", s)
		}
	}
	if got.Balance != nil {
		t.Errorf("callbacks must not include balanceInfo")
	}
	withBalance := SanitizeTransaction(full, true)
	if withBalance.Balance == nil {
		t.Errorf("API response must include balanceInfo")
	}
}

func TestRedactRawForClientDropsBalanceClause(t *testing.T) {
	raw := "#265710969 CEKHIDREG.085727035336 SUKSES. SN/Ref: SN-LIVE. Saldo 100.000 - 10.000 = 90.000 @30/10 00:52:30"
	want := "#265710969 CEKHIDREG.085727035336 SUKSES. SN/Ref: SN-LIVE. @30/10 00:52:30"
	if got := RedactRawForClient(raw); got != want {
		t.Fatalf("raw = %q, want %q", got, want)
	}
}
