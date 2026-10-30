package ipcenter

import "testing"

func TestTransactionURLNonBebas(t *testing.T) {
	nom := int64(15000)
	qty := int64(1)
	got := TransactionURL("http://center:6969", false, "AG000001", "PULSA10", "081234567890",
		&qty, "AVN1", "0000", "Secret123", "SIGN", &nom)
	want := "http://center:6969/trx?product=PULSA10&dest=081234567890&qty=1&refID=AVN1&memberID=AG000001&sign=SIGN&nominal=15000"
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestTransactionURLNonBebasNoNominal(t *testing.T) {
	got := TransactionURL("http://c", false, "M", "P", "D", nil, "R", "1", "P", "S", nil)
	want := "http://c/trx?product=P&dest=D&qty=1&refID=R&memberID=M&sign=S"
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestTransactionURLBebas(t *testing.T) {
	qty := int64(15000)
	got := TransactionURL("http://c", true, "M", "DANABEBAS", "081", &qty, "R", "1", "P", "S", nil)
	want := "http://c/trx?memberID=M&product=DANABEBAS&dest=081&pin=1&qty=15000&refID=R&password=P&sign=S"
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestBalanceURL(t *testing.T) {
	got := BalanceURL("http://c", "M", "1", "P", "S")
	want := "http://c/balance?memberID=M&memberid=M&pin=1&password=P&sign=S"
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestTicketURL(t *testing.T) {
	got := TicketURL("http://c", "M", 50000, "S")
	want := "http://c/?cmd=ticket&memberid=M&amount=50000&sign=S"
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestEncodeMatchesEncodeURIComponent(t *testing.T) {
	// url.QueryEscape would produce "+" for space and escape !'()*, which
	// changes the provider wire format.
	got := encodeComponent("a b!*'()~")
	want := "a%20b!*'()~"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
