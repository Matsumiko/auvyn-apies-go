package ipcenter

import (
	"errors"
	"testing"
)

func TestBuildSignatureVectors(t *testing.T) {
	nominal := int64(15000)
	cases := []struct {
		name string
		got  string
		want string
	}{
		{
			name: "trx",
			got: BuildSignature("OtomaX", "AG000001", "PULSA10", "081234567890",
				"AVN1750000000000", "0000", "Secret123", nil, false),
			want: "8_Zele0sMVhYb4yL4fwgjZkZXJU",
		},
		{
			name: "trx with nominal",
			got: BuildSignature("OtomaX", "AG000001", "PULSA10", "081234567890",
				"AVN1750000000000", "0000", "Secret123", &nominal, true),
			want: "12YsOt5QUNbCFIpIq1Za_jksP4k",
		},
		{
			name: "check balance",
			got:  BuildCheckBalanceSignature("OtomaX", "AG000001", "0000", "Secret123"),
			want: "2AIDNKCrNfMWyluKvxfIvKFOVuA",
		},
		{
			name: "ticket",
			got:  BuildTicketSignature("OtomaX", "AG000001", "0000", "Secret123", 50000),
			want: "OXWJYU5f9GCVqBPopmPiybHLvpw",
		},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s: got %q want %q", tc.name, tc.got, tc.want)
		}
	}
}

func TestParseNominalToInt(t *testing.T) {
	cases := []struct {
		in   any
		want *int64
	}{
		{"15.000", ptr(15000)},
		{15000.5, ptr(150005)}, // legacy quirk: non-digits are stripped
		{15000.0, ptr(15000)},
		{"", nil},
		{nil, nil},
		{-5, ptr(5)}, // legacy quirk: the minus sign is stripped
		{"abc", nil},
	}
	for _, tc := range cases {
		got := ParseNominalToInt(tc.in)
		if tc.want == nil {
			if got != nil {
				t.Errorf("%v: got %d want nil", tc.in, *got)
			}
			continue
		}
		if got == nil || *got != *tc.want {
			t.Errorf("%v: got %v want %d", tc.in, got, *tc.want)
		}
	}
}

func TestResolveQty(t *testing.T) {
	nom := int64(15000)
	cases := []struct {
		name    string
		raw     any
		nominal *int64
		bebas   bool
		isNom   bool
		unit    int64
		want    *int64
		wantErr bool
	}{
		{name: "none", raw: "none", want: nil},
		{name: "numeric floors", raw: 2.7, want: ptr(2)},
		{name: "bebas from nominal", raw: nil, nominal: &nom, bebas: true, isNom: true, unit: 1000, want: &nom},
		{name: "bebas unit", raw: nil, nominal: &nom, bebas: true, isNom: false, unit: 1000, want: ptr(15)},
		{name: "non bebas default", raw: nil, bebas: false, want: ptr(1)},
		{name: "bebas missing", raw: nil, bebas: true, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveQty(tc.raw, tc.nominal, tc.bebas, tc.isNom, tc.unit)
			if tc.wantErr {
				if !errors.Is(err, ErrQtyRequired) {
					t.Fatalf("got err %v want ErrQtyRequired", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if tc.want == nil {
				if got != nil {
					t.Fatalf("got %d want nil", *got)
				}
				return
			}
			if got == nil || *got != *tc.want {
				t.Fatalf("got %v want %d", got, *tc.want)
			}
		})
	}
}

func ptr(v int64) *int64 { return &v }
