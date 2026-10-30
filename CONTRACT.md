# CONTRACT — Auvyn Apies

Dokumentasi lengkap: [`docs/`](docs/README.md).

Dokumen ini memisahkan **permukaan yang tidak boleh kita ubah** (protokol provider) dari **permukaan yang kita kontrol** (API internal, callback, penyimpanan). Rewrite ke Go wajib mengikuti dokumen ini.

Aturan utama: **request keluar ke IP Center bersifat beku.** Prefix signature, nama/urutan parameter, casing, path, method, dan pola parsing Otomax tidak boleh "dirapikan" tanpa bukti dari provider.

Sumber kebenaran saat ini: implementasi Go di `internal/ipcenter` (signature, URL, parser, sanitizer), `internal/httpapi` (API, report, admin/client), dan `internal/webhook` (callback). Dokumen ini merangkum perilaku tersebut; jika berbeda, kode dan test vector menang sampai ada capture provider yang membuktikan sebaliknya.

---

## 1. Batas Perubahan

| Area | Status | Boleh diubah? |
|---|---|---|
| Signature (algoritma, template, encoding) | **FROZEN** | Tidak, kecuali provider mengonfirmasi |
| Path & query `/trx`, `/balance`, `/?cmd=ticket` | **FROZEN** | Tidak |
| Nama & casing parameter (`memberID` vs `memberid`) | **FROZEN** | Tidak |
| Pola parsing Otomax & urutannya | **FROZEN** | Tidak (hanya boleh ditambah, bukan diubah) |
| Timeout 20 detik ke center | Semi-frozen | Boleh dinaikkan, jangan diturunkan tanpa uji |
| API internal `/api/*`, `/admin/*`, `/client/*` | Kita kontrol | Boleh, dengan versi & dokumentasi |
| Callback keluar | Kita kontrol, konsumen terikat | Boleh dengan masa transisi |
| Storage (JSON → SQLite) | Internal | Bebas |
| `/report` (masuk dari provider) | Semi-frozen | Boleh diperketat, jangan sampai report sah ditolak |

---

## 2. Protokol Provider (FROZEN)

### 2.1 Endpoint keluar

Semua request adalah **HTTP GET tanpa body dan tanpa header khusus**.

| Operasi | URL | Parameter (urutan & casing persis) |
|---|---|---|
| Transaksi non-bebas | `{CENTER_URL}/trx?…` | `product`, `dest`, `qty`, `refID`, `memberID`, `sign`, `nominal` (opsional) |
| Transaksi bebas nominal | `{CENTER_URL}/trx?…` | `memberID`, `product`, `dest`, `pin`, `qty`, `refID`, `password`, `sign` |
| Cek saldo | `{CENTER_URL}/balance?…` | `memberID`, `memberid`, `pin`, `password`, `sign` |
| Tiket M-Bal | `{CENTER_URL}/?…` | `cmd=ticket`, `memberid`, `amount`, `sign` |

Catatan yang sering terlewat:

- `/balance` mengirim **dua** parameter ID: `memberID` **dan** `memberid`. Pertahankan keduanya.
- `/balance` memakai signature template `prefix|CheckBalance|memberId|pin|password`. Parameter `memberID`, `memberid`, `pin`, dan `password` tetap dikirim.
- Signature tiket memakai template `prefix|ticket|memberId|pin|password|amount`.
- Tiket memakai `memberid` huruf kecil dan `sign` dari template tiket (`prefix|ticket|memberId|pin|password|amount`).
- Parameter dengan nilai `null`/`undefined`/string kosong **dihilangkan** dari query (contoh: `nominal` tidak dikirim kalau kosong).
- Query dibangun manual dengan urutan di atas; jangan ganti ke encoder yang mengurutkan kunci.

### 2.2 Signature

```
prefix   = SIGN_PREFIX (default "ENGINE")
template = prefix | memberId | product | dest | refID | pin | password
sign     = base64url( sha1(template) )
```

- `base64url` = base64 dengan `+`→`-`, `/`→`_`, dan padding `=` dibuang.
- Nominal hanya masuk template bila `SIGN_INCLUDE_NOMINAL=true` **dan** nominal ada.
- Pada transaksi **bebas**, nominal tidak pernah masuk signature (walaupun `SIGN_INCLUDE_NOMINAL=true`), karena nilainya tidak dikirim ke provider.
- Signature cek saldo: `prefix | CheckBalance | memberId | pin | password`.
- Signature tiket: `prefix | ticket | memberId | pin | password | amount`.

Test vector (wajib ada di unit test Go dan harus identik):

| Kasus | Input | Expected `sign` |
|---|---|---|
| TRX | `ENGINE`, `AG000001`, `PULSA10`, `081234567890`, `AVN1750000000000`, `0000`, `Secret123` | `iYvFqEokeut7u5lFWG60-MH1OWc` |
| TRX + nominal 15000 | sama + `\|15000` | `LIQfj8k2qTHiEGHUpy0y8-84jPY` |
| Saldo / tiket | `ENGINE`, `AG000001`, `0000`, `Secret123` | `yT5C3r-ccaz2LuvQfgeLImXj_Lo` |

### 2.3 Mode produk bebas nominal

- Produk dianggap bebas bila kodenya persis (case-insensitive) ada di `EWALLET_BEBAS_PRODUCTS`.
- **Nominal tidak dikirim.** `qty` mewakili nominal:
  - `EWALLET_BEBAS_QTY_IS_NOMINAL=true` → `qty = nominal`
  - `false` → `qty = floor(nominal / EWALLET_BEBAS_UNIT)` dengan unit minimal 1
- Untuk produk bebas, `qty` atau `nominal` wajib ada; kalau tidak, request ditolak sebelum keluar.
- Untuk produk non-bebas, `qty` default `1`; `nominal` opsional dan ikut dikirim bila ada.

Aturan `qty` yang berlaku sekarang:

| Input `qty` | Perilaku |
|---|---|
| angka > 0 | dikirim `floor(angka)` |
| `"none"` / `null` | dihilangkan dari query |
| kosong + bebas + nominal | diturunkan dari nominal |
| kosong + non-bebas | `1` |

Quirk yang sengaja dipertahankan: `parseNominalToInt` membuang semua non-digit, sehingga `"15000.50"` menjadi `1500050`. Jangan perbaiki di jalur provider tanpa keputusan bisnis eksplisit.

### 2.4 Parsing respons Otomax

Urutan pola **penting**; pola pertama yang cocok menang. Semua case-insensitive.

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

**2.4b Pola tambahan (diverifikasi terhadap deployment Otomax di lapangan).** Pola ini dievaluasi setelah pola 1–11 dan sebelum CEK. Format live memakai `#<trxid>`, `SUKSES. SN/Ref:`, `Saldo`, dan `pd … gagal, dibatalkan`:

| Code | State | Regex |
|---|---|---|
| `INJECT_GAGAL` | FAILED | `\bInject\b[\s\S]*\bGAGAL\b` |
| `INJECT_SUKSES` | SUCCESS | `\bInject\b[\s\S]*\bSUKSES\b` |
| `PENDING_AKAN_DIPROSES` | PENDING | `^\s*#(?<trxid>\d+)[a-z]*\s+(?<product>[A-Z0-9]+)\.(?<tujuan>\d+)\s+akan diproses` |
| `SUKSES` | SUCCESS | `^\s*#(?<trxid>\d+)[a-z]*\s+(?<product>[A-Z0-9]+)\.(?<tujuan>\d+)\s+SUKSES\.\s*SN/Ref:\s*(?<sn>[^.\s]+)` |
| `GAGAL_DIBATALKAN` | FAILED | `^\s*#T(?<trxid>\d+)[a-z]*\s+(?<product>[A-Z0-9]+)\.(?<tujuan>\d+).*?gagal,\s*dibatalkan` |
| `GAGAL` | FAILED | `^\s*(?:'[^']*'\s*)?(?<product>[A-Z0-9]+)\.(?<tujuan>\d+)\s+GAGAL` |
| `GAGAL` | FAILED | `(?<product>[A-Z0-9]+)\.(?<tujuan>\d+)\s+GAGAL` (deskripsi produk boleh mendahului) |

Konsekuensi: Pada deployment lama, seluruh respons dapat tercatat `UNKNOWN` karena format live tidak cocok dengan pola 1–13. Klasifikasi tetap benar lewat fallback teks, tetapi `trxid`/`sn`/`tujuan` kosong. Pola di atas memperbaiki itu tanpa mengubah request keluar.

Fallback klasifikasi bila tidak ada pola yang cocok:

1. HTTP status ≠ 200 → `failed`
2. state `SUCCESS` → `success`; `FAILED`/`NOT_FOUND` → `failed`; `PENDING` → `pending`
3. teks mengandung `gagal|failed|reject` → `failed`
4. teks mengandung `akan diproses|menunggu jawaban|sedang diproses|proses` dan bukan `sukses|berhasil` → `pending`
5. teks mengandung `sukses|berhasil|ok` → `success`
6. selain itu → `pending`

### 2.5 Parsing saldo

Urutan: `M-Bal: a - b = c` → ambil `c`; else `M-Bal: a`; else `Saldo: a - b = c` → `c`; else `Saldo: a`. Angka diambil dengan membuang semua non-digit. Tidak ada pola → `null`.

### 2.6 Jebakan porting Node → Go

| Risiko | Tindakan |
|---|---|
| `url.Values.Encode()` mengurutkan kunci | Bangun query manual, pertahankan urutan |
| `url.QueryEscape` mengubah spasi jadi `+`; `encodeURIComponent` jadi `%20` | Pakai encoder kompatibel `encodeURIComponent` |
| Go mengirim `User-Agent: Go-http-client/1.1` | Observasi apakah provider peduli; jika ya, set eksplisit |
| Redirect | Node fetch follow; Go GET default follow. Set batas dan dokumentasikan |
| Charset respons | Baca sebagai UTF-8, sama seperti `res.text()` |
| Status non-200 | Jangan langsung error; klasifikasikan `failed` seperti sekarang |
| Named regex `(?<name>…)` | Didukung Go 1.22+ (sudah diverifikasi) |

---

## 3. `/report` — Masuk dari Provider

Provider memanggil URL report yang sudah terpasang di sisi mereka; **kita tidak bisa meminta mereka menambah header/token**.

Input yang diterima sekarang:

- Method: apa saja (GET/POST/PUT/…)
- `refid` atau `refID` (query atau body)
- `message` (query atau body)
- `status` (query atau body)

Respons: `200 text/plain` body `OK`.

Klasifikasi report saat ini:

1. `status == "20"` **atau** message mengandung `SUKSES|Sukses` → `success`
2. status terisi, bukan `"1"`, dan message mengandung `GAGAL|FAIL|ERROR` → `failed`
3. jika tidak, pakai parser Otomax
4. selain itu → `pending`

> Bug yang diketahui: aturan 1 membuat `status=20` + pesan `GAGAL` tetap `success`. Di v2, pesan eksplisit `GAGAL` harus menang. Ini perubahan perilaku yang disengaja.

Batasan v2 yang disepakati:

- Hanya menerima report dari IP provider (allowlist di reverse proxy) — lihat `SECURITY.md`.
- `refID` wajib ada di ledger; yang tidak dikenal ditolak dan di-alert.
- Harus idempotent: report berulang tidak menghasilkan event ganda.
- Konflik status final dicatat, tidak menimpa diam-diam.
- Body `text/plain` juga diparse (sekarang diabaikan).

---

## 4. API Internal (Kita Kontrol, Versi v2)

Auth `/api/transaction` dan `/api/transaction/status`: wajib kredensial client (`Bearer client_secret` + `X-Client-Pin` + `X-Client-Keys`). `/api/ping`, `/api/balance`, `/api/ticket-mbal`: `x-auvyn-secret`. Query `?secret=` tidak didukung.

| Endpoint | Request | Response v2 |
|---|---|---|
| `POST /api/ping` | — | `{ok,message,time}` |
| `POST /api/transaction` | `product`, `dest`, `qty?`, `refID?`, `nominal?`, `meta?` | payload transaksi tersanitasi + `balanceInfo` |
| `POST /api/balance` | `memberId?`, `meta?` | `{ok, statusCode, memberId, balance, balanceRaw, providerResult, meta, receivedAt}` |
| `POST /api/ticket-mbal` | `amount`, `memberId?`, `meta?` | `{ok, statusCode, memberId, ticket, amount, providerResult, meta, receivedAt}` |

Perubahan dari implementasi sekarang: `balance` **wajib** mengembalikan saldo. Untuk `ticket`, provider dapat membalas instruksi transfer deposit tanpa ID tiket; `ticket` boleh kosong dan instruksi lengkap ada di `providerResult.raw`.

Admin & client (v1, resource-oriented):

- `GET/POST /admin/v1/clients`
- `GET/DELETE /admin/v1/clients/{client_id}`
- `GET/POST /admin/v1/clients/{client_id}/webhooks`
- `POST /admin/v1/clients/{client_id}/callback-secret/rotate`
- `GET/PATCH/DELETE /admin/v1/webhooks/{webhook_id}`
- `GET /client/v1/me`, `GET /client/v1/webhooks`
- `PATCH/DELETE /client/v1/webhooks/{webhook_id}`
- `POST /client/v1/callback-secret/rotate`

Envelope v1: `{"data":...}` / `{"error":{"code","message"}}`, pagination `limit`/`offset`, `201`+`Location` untuk create, `204` untuk delete.

Legacy verb-style routes tetap dilayani sebagai alias deprecated (respons lama, header `Deprecation`/`Link`).

---

## 5. Callback Keluar (Konsumen Terikat)

Event internal: `transaction.request`, `transaction.report`, `balance.check`, `balance.ticket`.

Event turunan untuk webhook client: `invoice.paid`, `invoice.failed`, `invoice.pending`, diturunkan dari kategori hasil transaksi:

- request yang masih `pending` → `invoice.pending`;
- request yang langsung final → `invoice.paid`/`invoice.failed`;
- report hanya mengirim `invoice.*` bila kategori tersebut belum pernah dikirim untuk `refID` itu (`dedupe_key = invoice:<kategori>:<refID>`).

Dengan begitu provider yang langsung final dan juga mengirim report tetap menghasilkan tepat satu event final.

Header sekarang:

- `content-type: application/json`
- `x-auvyn-callback-secret: <secret>`
- `x-auvyn-event: <event>`
- `x-auvyn-webhook-id` (managed)
- `x-auvyn-client-id`, `x-auvyn-client-name` (managed)

Payload: hasil `sanitizeForCallback` + (`event`, `client_id`, `client_name` untuk managed). Field yang dibuang: `sign`, `centerUrl`, `memberID`, `amount`, `balanceInfo`, `isDuplicate`.

Perubahan v2 (dengan masa transisi dual-send):

1. `invoice.*` dipetakan dari kategori: pending untuk request yang belum final, paid/failed bila sudah final; event final di-dedupe per `refID`.
2. Tambah `event_id` unik dan `X-Auvyn-Signature: HMAC-SHA256(timestamp + "." + body)`.
3. Retry berjenjang + dead-letter, bukan fire-and-forget.
4. Header secret lama tetap dikirim sampai semua konsumen pindah.

Event matching yang ada: exact atau wildcard `prefix.*`. Broadcast tanpa `client_id` tidak diimplementasikan (dan jangan diimplementasikan).

---

## 6. Perubahan Perilaku v2 (Disengaja)

| # | Sebelum | Sesudah |
|---|---|---|
| 1 | `/report` tanpa auth | Allowlist IP + `refID` wajib dikenal |
| 2 | `status=20` + `GAGAL` → success | Pesan `GAGAL` menang |
| 3 | Balance/tiket tidak mengembalikan nilainya | Kontrak diperbaiki |
| 4 | `invoice.paid` bisa 2× per transaksi | Satu event final per transisi (idempotent) |
| 5 | Pending in-memory, hilang saat restart | Ledger SQLite |
| 6 | `refID = AVN + Date.now()` (bisa ditebak, bisa tabrakan) | `AVN` + 64-bit acak base32, **panjang sama** (13 karakter) agar aman terhadap batas provider |
| 7 | Secret boleh di query | Hanya header |
| 8 | SN tidak tersedia untuk client | `providerResult.sn` dan `raw` client memuat SN; log tetap diredaksi |
| 9 | Body `text/plain` diabaikan | Diparse |
| 10 | Tanpa retry | Outbox + retry + alert |

Nomor 6 penting: panjang `refID` dipertahankan karena batas kolom provider tidak diketahui. Jangan memperpanjang tanpa capture/konfirmasi.

---

## 7. Golden Test Wajib Sebelum Cutover

1. Signature: 3 test vector di atas identik byte-per-byte.
2. URL builder: TRX bebas, TRX non-bebas, balance (dua casing), tiket — string query persis.
3. Parser: minimal satu contoh per pola 1–13 + pesan tidak dikenal + pesan kosong.
4. Klasifikasi fallback: 6 aturan.
5. Parse saldo: 4 varian + null.
6. Report: query GET, form POST, JSON POST, text/plain, duplikat, `refID` asing, status konflik.
7. Sanitizer log: `HRG`, `M-Bal`, `#Inject`, `memberID`, `sign`, `SN/Ref`, URL `/trx`. Sanitizer client: SN dipertahankan, klausa saldo dibuang, kredensial diredaksi.
8. Callback: routing per `meta.client_id`, wildcard event, tanpa `client_id` tidak broadcast, idempotensi, retry.
9. Auth: `/report` dari IP di luar allowlist ditolak, `refID` asing ditolak, secret query ditolak.
10. Import data: jumlah client/webhook dari `data/db.json` sama setelah migrasi.

---

## 8. Kebijakan Perubahan Protokol Provider

Perubahan pada Bagian 2 hanya boleh dilakukan bila ada:

1. Capture request/response asli dari provider (log atau dokumentasi resmi), dan
2. Konfirmasi provider, dan
3. Test vector baru yang ditambahkan ke suite.

Tanpa ketiganya, perlakukan sebagai bug.
