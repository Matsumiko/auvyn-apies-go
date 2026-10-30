# 09 — Protokol provider

Dokumen ini menjelaskan protokol **keluar** ke IP Center. Bagian ini beku: perubahan hanya boleh dilakukan bila ada capture/konfirmasi provider.

## Ringkasan

- HTTP GET tanpa body.
- Signature SHA1 → base64url.
- Query dibangun manual dengan urutan dan casing yang dipertahankan.
- Timeout 20 detik.
- `User-Agent: auvyn-apies/2.0`.
- Status HTTP non-200 tetap dibaca dan diklasifikasikan, bukan langsung dianggap error jaringan.

## Signature

```text
prefix   = SIGN_PREFIX (default "ENGINE")
template = prefix | memberId | product | dest | refID | pin | password
sign     = base64url(sha1(template))
```

- `base64url`: `+` → `-`, `/` → `_`, padding `=` dibuang.
- `nominal` ditambahkan ke template hanya bila `SIGN_INCLUDE_NOMINAL=true` **dan** nominal ada **dan** transaksi bukan produk bebas.
- Signature cek saldo: `prefix | CheckBalance | memberId | pin | password`.
- Signature tiket: `prefix | ticket | memberId | pin | password | amount`.

Test vector:

| Kasus | Input | Expected |
|---|---|---|
| TRX | `ENGINE`, `AG000001`, `PULSA10`, `081234567890`, `AVN1750000000000`, `0000`, `Secret123` | `iYvFqEokeut7u5lFWG60-MH1OWc` |
| TRX + nominal 15000 | sama + `\|15000` | `LIQfj8k2qTHiEGHUpy0y8-84jPY` |
| Saldo | `ENGINE|CheckBalance|AG000001|0000|Secret123` | `xsqtqJgRRgn0Eirf-6KCnKNL01o` |
| Tiket | `ENGINE|ticket|AG000001|0000|Secret123|50000` | `K7rt5-vC7HGg9iSeerIE0gjF_lE` |

Vector ini diuji di `internal/ipcenter/sign_test.go`.

## Endpoint keluar

| Operasi | URL | Parameter (urutan dipertahankan) |
|---|---|---|
| TRX non-bebas | `{CENTER_URL}/trx?…` | `product`, `dest`, `qty`, `refID`, `memberID`, `sign`, `nominal` (opsional) |
| TRX bebas | `{CENTER_URL}/trx?…` | `memberID`, `product`, `dest`, `pin`, `qty`, `refID`, `password`, `sign` |
| Cek saldo | `{CENTER_URL}/balance?…` | `memberID`, `memberid`, `pin`, `password`, `sign` |
| Tiket M-Bal | `{CENTER_URL}/?…` | `cmd=ticket`, `memberid`, `amount`, `sign` |

Catatan:

- `/balance` mengirim **dua** parameter ID: `memberID` dan `memberid`.
- `/balance` memakai signature template `prefix|CheckBalance|memberId|pin|password`; PIN/password tetap dikirim di query.
- Signature tiket memakai template `prefix|ticket|memberId|pin|password|amount`.
- Tiket memakai `memberid` huruf kecil dan signature template tiket (`prefix|ticket|memberId|pin|password|amount`).
- Parameter bernilai kosong/null dihilangkan.

### Encoding

Query memakai encoder kompatibel `encodeURIComponent`, bukan `url.QueryEscape`:

- spasi → `%20` (bukan `+`);
- `!'()*~` tidak di-escape;
- kunci tidak diurutkan.

## Mode produk bebas nominal

Produk dianggap bebas bila kodenya ada di `EWALLET_BEBAS_PRODUCTS` (case-insensitive).

- Nominal **tidak dikirim** ke provider.
- `qty` mewakili nominal:
  - `EWALLET_BEBAS_QTY_IS_NOMINAL=true` → `qty = nominal`;
  - `false` → `qty = floor(nominal / EWALLET_BEBAS_UNIT)`, unit minimal 1.
- Signature tidak memasukkan nominal.
- `qty` atau `nominal` wajib ada.

Aturan `qty`:

| Input | Perilaku |
|---|---|
| angka > 0 | dikirim `floor(angka)` |
| `"none"` | parameter dihilangkan |
| kosong + bebas + nominal | diturunkan dari nominal |
| kosong + non-bebas | `1` |

Quirk kompatibilitas: `ParseNominalToInt` membuang semua non-digit. `"15.000"` → `15000`; `15000.5` → `150005`; `-5` → `5`. Jangan diperbaiki tanpa keputusan bisnis.

## Parsing respons

Urutan pola penting; pola pertama yang cocok menang. Semua case-insensitive.

### Pola lama

| # | Code | State | Regex |
|---|---|---|---|
| 1 | `PENDING_AKAN_DIPROSES` | PENDING | `R(?<trxid>\d+).*\.(?<tujuan>.+) akan diproses` |
| 2 | `PENDING_MENUNGGU_SEBELUMNYA` | PENDING | `R(?<trxid>\d+).*\.(?<tujuan>.+) @` |
| 3 | `SUKSES` | SUCCESS | `R(?<trxid>\d+).*\.(?<tujuan>.+) SUKSES.*SN\/Ref:\s*(?<sn>.+)\. M-Bal` |
| 4 | `SUKSES_SUDAH_PERNAH` | SUCCESS (duplicate) | `R#(?<trxid>\d+).*\.(?<tujuan>.+) sdh.*SN\/Ref:\s*(?<sn>.+)\. M-Bal` |
| 5 | `GAGAL_TUJUAN_SALAH` | FAILED | `R#(?<trxid>\d+).*\.(?<tujuan>.+) GAGAL\. Nomor tujuan salah\.` |
| 6 | `GAGAL_TIMEOUT` | FAILED | `R(?<trxid>\d+).*\.(?<tujuan>\d+) GAGAL karena timeout` |
| 7 | `GAGAL_SALAH_KODE` | FAILED | `R(?<trxid>\d+).*\.(?<tujuan>\d+) GAGAL, salah` |
| 8 | `GAGAL_BLACKLIST` | FAILED | `R#(?<trxid>\d+).*\.(?<tujuan>\d+) sdh pernah jam .*status Nomor Blacklist` |
| 9 | `GAGAL_PRODUK_GANGGUAN` | FAILED | `R(?<trxid>\d+).*\.(?<tujuan>.+) GAGAL\. Produk sedang gangguan\.` |
| 10 | `GAGAL_SALDO_TIDAK_CUKUP` | FAILED | `R(?<trxid>\d+).*\.(?<tujuan>\d+) GAGAL\. Saldo tidak cukup` |
| 11 | `GAGAL_DIKARENAKAN` | FAILED | `R(?<trxid>\d+).*\.(?<tujuan>\d+) GAGAL dikarenakan` |
| 12 | `CEK_TIDAK_ADA_DATA` | NOT_FOUND | `TIDAK ADA transaksi Tujuan "(?<tujuan>\d+)" pada tgl (?<tanggal>\d{2}\/\d{2}\/\d{2})\. Tidak ada data\.` |
| 13 | `CEK_SUKSES` | SUCCESS | `\b(?<kode>[A-Z0-9]+)\s+\S*\.(?<tujuan>\d+)\s+\d{2}:\d{2}\s+Sukses\s+SN:\s*(?<sn>.+)$` (multiline) |

### Pola tambahan (format lapangan)

Diletakkan setelah pola 1–11, sebelum CEK:

| Code | State | Regex |
|---|---|---|
| `INJECT_GAGAL` | FAILED | `\bInject\b[\s\S]*\bGAGAL\b` |
| `INJECT_SUKSES` | SUCCESS | `\bInject\b[\s\S]*\bSUKSES\b` |
| `PENDING_AKAN_DIPROSES` | PENDING | `^\s*#(?<trxid>\d+)[a-z]*\s+(?<product>[A-Z0-9]+)\.(?<tujuan>\d+)\s+akan diproses` |
| `SUKSES` | SUCCESS | `^\s*#(?<trxid>\d+)[a-z]*\s+(?<product>[A-Z0-9]+)\.(?<tujuan>\d+)\s+SUKSES\.\s*SN/Ref:\s*(?<sn>[^.\s]+)` |
| `GAGAL_DIBATALKAN` | FAILED | `^\s*#T(?<trxid>\d+)[a-z]*\s+(?<product>[A-Z0-9]+)\.(?<tujuan>\d+).*?gagal,\s*dibatalkan` |
| `GAGAL` | FAILED | `^\s*(?:'[^']*'\s*)?(?<product>[A-Z0-9]+)\.(?<tujuan>\d+)\s+GAGAL` |
| `GAGAL` | FAILED | `(?<product>[A-Z0-9]+)\.(?<tujuan>\d+)\s+GAGAL` (deskripsi produk boleh mendahului) |

Contoh format lapangan:

```text
#123456789q GAME100.081234567890 SUKSES. SN/Ref: SN-ABC. Saldo 100.000 - 25.000 = 75.000 @22/02 09:46:48
#123456789 GAME100.081234567890 akan diproses @09:46. Saldo 100.000 - 25.000 = 75.000
GAME50.081234567890 GAGAL. . Saldo 150.000 @08:23
Paket Data Contoh 10GB - 30Hari GAME50.081234567890 GAGAL. Nomor tujuan salah.
#T555666777 PULSA10.081234567893 pd 12-06 23:40:12 gagal, dibatalkan. Saldo refund 10.000. Sal 80.000
```

Parser mengisi `state`, `code`, `trxid`, `tujuan`, `sn`, dan `duplicate` sesuai pola. Pada deployment lama, format lapangan tidak cocok dengan pola lama sehingga seluruh detail provider tercatat `UNKNOWN`; klasifikasi masih benar lewat fallback teks, tetapi `trxid`/`sn`/`tujuan` kosong.

## Fallback klasifikasi transaksi

1. HTTP status ≠ 200 → `failed`.
2. State `SUCCESS` → `success`; `FAILED`/`NOT_FOUND` → `failed`; `PENDING` → `pending`.
3. Teks mengandung `gagal|failed|reject` → `failed`.
4. Teks mengandung `akan diproses|menunggu jawaban|sedang diproses|proses` dan bukan `sukses|berhasil` → `pending`.
5. Teks mengandung `sukses|berhasil|ok` → `success`.
6. Selain itu → `pending`.

## Parsing saldo

Urutan:

1. `M-Bal: a - b = c` → `c`;
2. `M-Bal: a` → `a`;
3. `Saldo a - b = c` → `c`;
4. `Saldo a` → `a`.
5. Tidak ada → `null`.

Angka diambil dengan membuang non-digit.

## Parsing tiket

Best-effort, mencari:

- `TICKET: nilai`
- `TIKET: nilai`
- `KODE: nilai`

Nilai harus alfanumerik minimal 4 karakter. Bila tidak ditemukan, `ticket` kosong.

Provider yang dipakai project ini membalas tiket dengan instruksi transfer deposit (nominal transfer, rekening, catatan admin), bukan string `TICKET: ...`. Karena itu `ticket` kosong adalah respons normal untuk format tersebut; periksa `providerResult.raw`. Jangan menganggapnya sebagai kegagalan transaksi.

Signature tiket (`prefix|ticket|memberId|pin|password|amount`) sudah diverifikasi diterima provider pada uji `amount=30000`.

## Error jaringan dan redaksi

- Error `*url.Error` di-strip dari URL sebelum dilog/dikembalikan, karena query memuat PIN, password, dan signature.
- Respons API memakai pesan generik `failed to call center`.
- URL provider tidak pernah ditulis utuh ke log; hanya scheme/host/path.

## Kebijakan perubahan

Perubahan pada signature, URL, urutan parameter, atau pola parsing hanya boleh bila ada:

1. capture request/response asli;
2. konfirmasi provider;
3. test vector baru.

Tanpa ketiganya, perlakukan sebagai bug.

## Dokumen terkait

- [20 — Protokol Otomax](20-otomax-protocol.md) — ringkasan protokol Otomax/IP Center dan pemetaan ke implementasi project.

- Kontrak beku: `CONTRACT.md`
- Test: `internal/ipcenter/*_test.go`
- Report: [08 — Report provider](08-report-ingestion.md)
