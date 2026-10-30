package ipcenter

import "testing"

func TestParseMessagePatterns(t *testing.T) {
	cases := []struct {
		name      string
		message   string
		wantState string
		wantCode  string
		wantTuj   string
		wantSN    string
		duplicate bool
	}{
		{"pending akan diproses", "R12345.081234567890 akan diproses", "PENDING", "PENDING_AKAN_DIPROSES", "081234567890", "", false},
		{"pending menunggu", "R12345.081234567890 @", "PENDING", "PENDING_MENUNGGU_SEBELUMNYA", "081234567890", "", false},
		{"sukses", "R12345.081234567890 SUKSES. SN/Ref: SN-ABC. M-Bal: 10.000 - 1.000 = 9.000", "SUCCESS", "SUKSES", "081234567890", "SN-ABC", false},
		{"sukses sudah pernah", "R#12345.081234567890 sdh SUKSES. SN/Ref: SN-ABC. M-Bal: 10.000", "SUCCESS", "SUKSES_SUDAH_PERNAH", "081234567890", "SN-ABC", true},
		{"gagal tujuan", "R#12345.081234567890 GAGAL. Nomor tujuan salah.", "FAILED", "GAGAL_TUJUAN_SALAH", "081234567890", "", false},
		{"gagal timeout", "R12345.081234567890 GAGAL karena timeout", "FAILED", "GAGAL_TIMEOUT", "081234567890", "", false},
		{"gagal salah kode", "R12345.081234567890 GAGAL, salah kode", "FAILED", "GAGAL_SALAH_KODE", "081234567890", "", false},
		{"gagal blacklist", "R#12345.081234567890 sdh pernah jam 10:00 status Nomor Blacklist", "FAILED", "GAGAL_BLACKLIST", "081234567890", "", false},
		{"gagal gangguan", "R12345.081234567890 GAGAL. Produk sedang gangguan.", "FAILED", "GAGAL_PRODUK_GANGGUAN", "081234567890", "", false},
		{"gagal saldo", "R12345.081234567890 GAGAL. Saldo tidak cukup", "FAILED", "GAGAL_SALDO_TIDAK_CUKUP", "081234567890", "", false},
		{"gagal dikarenakan", "R12345.081234567890 GAGAL dikarenakan gangguan", "FAILED", "GAGAL_DIKARENAKAN", "081234567890", "", false},
		{"field pending", "#123456789 GAME100.081234567890 akan diproses @09:46. Saldo 100.000 - 25.000 = 75.000", "PENDING", "PENDING_AKAN_DIPROSES", "081234567890", "", false},
		{"field success", "#123456789 GAME100.081234567890 SUKSES. SN/Ref: SN-ABC123. Saldo 100.000 - 25.000 = 75.000 @22/02 09:46:48 R#ref", "SUCCESS", "SUKSES", "081234567890", "SN-ABC123", false},
		{"field success suffix", "#987654321q PULSA10.081234567891 SUKSES. SN/Ref: SN-XYZ789. Saldo 50.000 - 10.000 = 40.000", "SUCCESS", "SUKSES", "081234567891", "SN-XYZ789", false},
		{"field pending suffix", "#987654321q PULSA10.081234567891 akan diproses @16:26. Saldo 50.000 - 10.000 = 40.000", "PENDING", "PENDING_AKAN_DIPROSES", "081234567891", "", false},
		{"field failed", "GAME50.081234567892 GAGAL. . Saldo 150.000 @08:23 R#ref", "FAILED", "GAGAL", "081234567892", "", false},
		{"field failed quoted", "'R#ref_1' GAME50.081234567892  GAGAL. Saldo tidak cukup/Terkena LImit. Hrg 100.000, Saldo 150.000 Limit 0, proses 0.", "FAILED", "GAGAL", "081234567892", "", false},
		{"field failed description", "Paket Data Contoh 10GB - 30Hari PULSA10.081234567890 GAGAL. Nomor tujuan salah. Sal 5.000 @09:46 R#ref", "FAILED", "GAGAL", "081234567890", "", false},
		{"field cancelled", "#T555666777 PULSA10.081234567893 pd 12-06 23:40:12 gagal, dibatalkan. Saldo refund 10.000. Sal 80.000", "FAILED", "GAGAL_DIBATALKAN", "081234567893", "", false},
		{"inject success", "Inject 08123 SUKSES SN: ABC", "SUCCESS", "INJECT_SUKSES", "", "", false},
		{"inject failed", "Inject 08123 GAGAL", "FAILED", "INJECT_GAGAL", "", "", false},
		{"cek tidak ada data", `TIDAK ADA transaksi Tujuan "081234567890" pada tgl 01/02/25. Tidak ada data.`, "NOT_FOUND", "CEK_TIDAK_ADA_DATA", "081234567890", "", false},
		{"cek sukses", "ABC123 .081234567890 10:20 Sukses SN: XYZ-1", "SUCCESS", "CEK_SUKSES", "081234567890", "XYZ-1", false},
		{"unknown", "pesan tidak dikenal", "UNKNOWN", "UNKNOWN", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseMessage(tc.message)
			if got.State != tc.wantState || got.Code != tc.wantCode {
				t.Fatalf("state/code = %s/%s want %s/%s", got.State, got.Code, tc.wantState, tc.wantCode)
			}
			if got.Tujuan != tc.wantTuj {
				t.Errorf("tujuan = %q want %q", got.Tujuan, tc.wantTuj)
			}
			if got.SN != tc.wantSN {
				t.Errorf("sn = %q want %q", got.SN, tc.wantSN)
			}
			if got.Duplicate != tc.duplicate {
				t.Errorf("duplicate = %v want %v", got.Duplicate, tc.duplicate)
			}
		})
	}
}

func TestClassifyTransaction(t *testing.T) {
	cases := []struct {
		raw    string
		status int
		want   string
	}{
		{"R1.081 SUKSES. SN/Ref: X. M-Bal: 1", 200, "success"},
		{"R1.081 akan diproses", 200, "pending"},
		{"R1.081 GAGAL. Nomor tujuan salah.", 200, "failed"},
		{"apa saja", 500, "failed"},
		{"transaksi sedang diproses", 200, "pending"},
		{"OK", 200, "success"},
		{"tidak jelas", 200, "pending"},
	}
	for _, tc := range cases {
		if got := ClassifyTransaction(tc.raw, tc.status); got != tc.want {
			t.Errorf("%q/%d = %s want %s", tc.raw, tc.status, got, tc.want)
		}
	}
}

func TestClassifyReport(t *testing.T) {
	cases := []struct {
		message string
		status  string
		want    string
	}{
		{"R1.081 SUKSES. SN/Ref: X. M-Bal: 1", "20", "success"},
		{"R1.081 GAGAL. Nomor tujuan salah.", "20", "failed"}, // explicit failure wins
		{"R1.081 akan diproses", "1", "pending"},
		{"R1.081 GAGAL karena timeout", "", "failed"},
		{"R1.081 SUKSES. SN/Ref: X. M-Bal: 1", "", "success"},
		{"tidak dikenal", "", "pending"},
	}
	for _, tc := range cases {
		if got := ClassifyReport(tc.message, tc.status); got != tc.want {
			t.Errorf("%q status=%q = %s want %s", tc.message, tc.status, got, tc.want)
		}
	}
}

func TestParseBalance(t *testing.T) {
	cases := []struct {
		raw  string
		want int64
		null bool
	}{
		{"M-Bal: 10.000 - 1.000 = 9.000", 9000, false},
		{"M-Bal: 77.827.622", 77827622, false},
		{"Saldo 100.000 - 1.000 = 99.000", 99000, false},
		{"Saldo 5.000", 5000, false},
		{"tidak ada saldo", 0, true},
	}
	for _, tc := range cases {
		got := ParseBalance(tc.raw)
		if tc.null {
			if got != nil {
				t.Errorf("%q: got %+v want nil", tc.raw, got)
			}
			continue
		}
		if got == nil || got.Remaining != tc.want {
			t.Errorf("%q: got %+v want %d", tc.raw, got, tc.want)
		}
	}
}

func TestParseTicket(t *testing.T) {
	cases := map[string]string{
		"TICKET: TICK-999. M-Bal: 10.000": "TICK-999",
		"Tiket: ABC123":                   "ABC123",
		"Kode: XYZ99 sukses":              "XYZ99",
		"tidak ada":                       "",
	}
	for raw, want := range cases {
		if got := ParseTicket(raw); got != want {
			t.Errorf("%q = %q want %q", raw, got, want)
		}
	}
}
