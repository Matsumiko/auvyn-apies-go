package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadLogRowsAndRowToTransaction(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "trx-success.log")
	content := `{"level":"info","message":{"refID":"REF-1","product":"PULSA10","dest":"0812","qty":1,"nominal":15000,"category":"success","providerResult":{"state":"SUCCESS","code":"SUKSES","trxid":"123","sn":"SN-1"},"raw":"#123 PULSA10.0812 SUKSES","meta":{"client_id":"c1","orderId":"INV-1"},"receivedAt":"2026-10-29T09:26:52Z"}}
{"level":"info","message":{"refID":"","product":"x"}}
not-json
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	rows, err := readLogRows(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d", len(rows))
	}
	txn, ok := rowToTransaction(rows[0])
	if !ok {
		t.Fatal("valid row rejected")
	}
	if txn.RefID != "REF-1" || txn.Category != "success" || txn.ClientID != "c1" ||
		txn.SN != "SN-1" || txn.Qty == nil || *txn.Qty != 1 || txn.Nominal == nil || *txn.Nominal != 15000 {
		t.Fatalf("unexpected transaction: %+v", txn)
	}
	if txn.CreatedAt.IsZero() {
		t.Error("receivedAt not parsed")
	}
	if _, ok := rowToTransaction(rows[1]); ok {
		t.Error("row without refID must be skipped")
	}
}
