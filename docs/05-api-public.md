# 05 — API publik

API ini dipakai backend pemilik produk. Endpoint berada di `/api/*` dan **tidak** menerima secret lewat query string:

- `/api/transaction` **wajib** memakai kredensial client.
- `/api/ping`, `/api/balance`, dan `/api/ticket-mbal` memakai `x-auvyn-secret` operasional.

- Content-Type request: `application/json`
- Batas body: 128 KB
- Timeout provider: 20 detik; handler: 25 detik
- Envelope error legacy: `{"ok":false,"error":"CODE","message":"..."}`
- Kategori respons: `success`, `pending`, `failed`

## Autentikasi

### Transaksi (wajib per client)

Gunakan tiga kredensial client yang dibuat lewat admin/bot:

```http
Authorization: Bearer <client_secret>
X-Client-Pin: <client_pin>
X-Client-Keys: <client_keys>
```

Dengan autentikasi client, `meta.client_id` diisi otomatis dari client yang login. Jika body mengirim `meta.client_id` yang berbeda, request ditolak `403 FORBIDDEN`.

`refID` juga terikat ke client: client lain tidak bisa membaca/mereplay transaksi dengan `refID` yang sama.

### Endpoint operasional / legacy

```http
x-auvyn-secret: <AUVYN_SECRET>
```

Berlaku untuk `/api/ping`, `/api/balance`, dan `/api/ticket-mbal`. **Tidak berlaku** untuk `/api/transaction`.

Gagal:

```json
{"ok":false,"error":"UNAUTHORIZED","message":"missing x-client-secret / x-client-pin / x-client-keys"}
```

## `POST /api/ping`

Request body opsional.

```json
{"ok":true,"message":"pong","time":"2026-01-01T00:00:00.000000000Z"}
```

## `POST /api/transaction`

Mengirim transaksi ke provider.

### Request

| Field | Tipe | Wajib | Keterangan |
|---|---|---|---|
| `product` | string | ya | Kode produk. |
| `dest` | string | ya | Nomor tujuan / ID tujuan. |
| `qty` | number/string/null | tidak | Angka > 0, `"none"`, atau null. |
| `refID` | string | tidak | ID idempotensi. Dibuat acak bila kosong. |
| `nominal` | number/string | kondisional | Wajib untuk produk bebas nominal. |
| `meta` | object | tidak | Diteruskan ke callback. Saat memakai auth client, `meta.client_id` diisi otomatis; jika diisi manual harus sama dengan client yang login. |

Catatan `qty`:

- angka > 0 → dikirim `floor(angka)`;
- `"none"` → parameter `qty` dihilangkan;
- kosong + produk bebas + `nominal` → diturunkan dari nominal;
- kosong + produk non-bebas → `1`.

Catatan `nominal`:

- untuk produk bebas: wajib, tidak dikirim ke provider; `qty` mewakili nominal;
- untuk produk non-bebas: opsional, harus angka positif bila diisi;
- parsing membuang semua non-digit (`"15.000"` → `15000`).

### Contoh

```http
POST /api/transaction
Content-Type: application/json
Authorization: Bearer <client_secret>
X-Client-Pin: <client_pin>
X-Client-Keys: <client_keys>

{
  "product": "PULSA10",
  "dest": "081234567890",
  "qty": 1,
  "refID": "AVN-DEMO-1",
  "meta": {"orderId": "INV-1"}
}
```

### Respons sukses

```json
{
  "ok": true,
  "statusCode": 200,
  "refID": "AVN-DEMO-1",
  "product": "PULSA10",
  "dest": "081234567890",
  "qty": 1,
  "nominal": null,
  "category": "success",
  "receivedAt": "2026-01-01T00:00:00.000000000Z",
  "meta": {"orderId": "INV-1", "client_id": "avn_cl_example"},
  "providerResult": {
    "state": "SUCCESS",
    "code": "SUKSES",
    "trxid": "12345",
    "tujuan": "081234567890",
    "sn": "SN-ABC",
    "duplicate": false,
    "raw": "R12345.081234567890 SUKSES. SN/Ref: SN-ABC"
  },
  "raw": "R12345.081234567890 SUKSES. SN/Ref: SN-ABC",
  "balanceInfo": {"raw": "90.000", "remaining": 90000}
}
```

Catatan:

- `ok` mengikuti keberhasilan pemanggilan provider, bukan kategori transaksi.
- `providerResult.sn` berisi SN/Ref asli untuk transaksi milik client.
- `raw` mempertahankan `SN/Ref` untuk client; klausa saldo/`M-Bal` dibuang, sedangkan `sign`, `memberID`, dan URL transaksi diredaksi.
- Log dan notifikasi Telegram tetap meredaksi SN.
- `balanceInfo` hanya ada bila saldo berhasil diparse dari respons provider.
- `qty`/`nominal` dapat `null`.

### Semantik idempotensi

- `refID` final yang dikirim ulang → `200` replay dari ledger, provider **tidak** dipanggil lagi.
- `refID` yang masih diproses (intent tanpa hasil provider) → `409 IN_PROGRESS`.
- `refID` sudah ada tetapi `product`/`dest` berbeda → `409 DUPLICATE_REF`.
- `refID` baru → provider dipanggil.

### Timeout provider

Jika provider timeout atau koneksi putus:

- API mengembalikan `502 CENTER_ERROR`;
- intent tetap disimpan (provider mungkin sudah menerima request);
- retry dengan `refID` yang sama menghasilkan `409 IN_PROGRESS`;
- rekonsiliasi manual melalui tabel `transactions` dan `provider_reports`.

Jangan mengulang dengan `refID` baru sebelum yakin provider tidak memproses transaksi pertama.

### Error

| Status | Code | Penyebab |
|---|---|---|
| 400 | `BAD_REQUEST` | JSON invalid, `product`/`dest` kosong, nominal bebas tidak ada, nominal invalid |
| 401 | `UNAUTHORIZED` | kredensial client salah/kosong |
| 409 | `DUPLICATE_REF` / `IN_PROGRESS` | konflik `refID` |
| 502 | `CENTER_ERROR` | provider gagal dihubungi |
| 500 | `INTERNAL_ERROR` | database gagal |

## `GET|POST /api/transaction/status`

Membaca status transaksi dari ledger, tanpa memanggil provider.

### Request

```http
GET /api/transaction/status?refID=AVN-DEMO-1
Authorization: Bearer <client_secret>
X-Client-Pin: <client_pin>
X-Client-Keys: <client_keys>
```

atau:

```http
POST /api/transaction/status
Content-Type: application/json
Authorization: Bearer <client_secret>
X-Client-Pin: <client_pin>
X-Client-Keys: <client_keys>

{"refID":"AVN-DEMO-1"}
```

`refId` diterima sebagai alias. Untuk POST, `refID` di body diprioritaskan; query dipakai bila body kosong.

Transaksi hanya bisa dibaca oleh client pemiliknya. `refID` milik client lain menghasilkan `404 NOT_FOUND`, bukan `403`, agar keberadaan transaksi tidak bocor.

### Respons

```json
{
  "ok": true,
  "refID": "AVN-DEMO-1",
  "product": "PULSA10",
  "dest": "081234567890",
  "qty": 1,
  "nominal": null,
  "category": "success",
  "providerResult": {
    "state": "SUCCESS",
    "code": "SUKSES",
    "trxid": "12345",
    "tujuan": "081234567890",
    "sn": "SN-ABC",
    "duplicate": false,
    "raw": "R12345.081234567890 SUKSES. SN/Ref: SN-ABC"
  },
  "meta": {"orderId": "INV-1"},
  "createdAt": "2026-01-01T00:00:00.000000000Z",
  "updatedAt": "2026-01-01T00:00:01.000000000Z",
  "finalAt": "2026-01-01T00:00:01.000000000Z"
}
```

- `category` adalah status ledger: `pending`, `success`, atau `failed`.
- `finalAt` bernilai `null` selama belum final.
- `providerResult.sn` berisi SN/Ref asli untuk client pemilik; klausa saldo dibuang dan kredensial di `raw` tetap diredaksi.
- `GET`/`POST` sama-sama memakai kredensial client.

### Error

| Status | Code | Penyebab |
|---|---|---|
| 400 | `BAD_REQUEST` | `refID` kosong |
| 401 | `UNAUTHORIZED` | kredensial client tidak lengkap/salah |
| 404 | `NOT_FOUND` | transaksi tidak ada atau milik client lain |

## `POST /api/balance`

Cek saldo provider. Body opsional.

### Request

```json
{"memberId": "AG000001", "meta": {"source": "manual"}}
```

`memberId` kosong → memakai `MEMBER_ID` dari environment.

### Respons

```json
{
  "ok": true,
  "statusCode": 200,
  "memberId": "AG000001",
  "balance": 90000,
  "balanceRaw": "90.000",
  "providerResult": {
    "state": "UNKNOWN",
    "code": "UNKNOWN",
    "trxid": "",
    "tujuan": "",
    "sn": "",
    "duplicate": false,
    "raw": "Saldo 100.000 - 10.000 = 90.000"
  },
  "meta": {"source": "manual"},
  "receivedAt": "2026-01-01T00:00:00.000000000Z"
}
```

- `balance` null bila pola saldo tidak ditemukan.
- `balanceRaw` string saldo asli yang diparse.
- Callback legacy `balance.check` dikirim lewat outbox (tidak ada webhook managed untuk event ini).

## `POST /api/ticket-mbal`

Membuat tiket M-Bal.

### Request

| Field | Tipe | Wajib |
|---|---|---|
| `amount` | number/string | ya, > 0 |
| `memberId` | string | tidak |
| `meta` | object | tidak |

### Respons

```json
{
  "ok": true,
  "statusCode": 200,
  "memberId": "AG000001",
  "ticket": "",
  "amount": 30000,
  "providerResult": {
    "state": "UNKNOWN",
    "code": "UNKNOWN",
    "trxid": "",
    "tujuan": "",
    "sn": "",
    "duplicate": false,
    "raw": "*Jika Transfer Via EDC WAJIB Konfirmasi... Silakan Transfer Rp. [nominal] ... [rekening provider]"
  },
  "meta": {},
  "receivedAt": "2026-01-01T00:00:00.000000000Z"
}
```

Provider dapat membalas dengan **instruksi transfer deposit**, bukan ID tiket. Dalam kondisi ini:

- `ticket` berisi string kosong dan itu **bukan kegagalan**;
- `amount` adalah nominal yang diminta, sedangkan nominal transfer dapat lebih besar karena biaya admin dan hanya tercantum di `providerResult.raw`;
- `providerResult.raw` dapat memuat nominal transfer dan nomor rekening provider; jangan kirim mentah ke frontend publik.

Parser tiket bersifat best-effort: mencari `TICKET`, `TIKET`, atau `KODE` diikuti nilai alfanumerik. Bila provider tidak memakai format itu, `ticket` kosong dan instruksi tetap tersedia di `raw`.

## Endpoint health

| Endpoint | Auth | Respons |
|---|---|---|
| `GET /` | tidak | `{ok,name,message,time}` |
| `GET /healthz` | tidak | `{ok,name,message,time}` |
| `GET /readyz` | tidak | `{ok}` atau 503 bila database tidak siap |
| `GET /version` | tidak | `{ok,name,version}` |

## Kode error publik

| Code | Arti |
|---|---|
| `BAD_REQUEST` | input tidak valid |
| `UNAUTHORIZED` | secret salah |
| `DUPLICATE_REF` | `refID` dipakai dengan payload berbeda |
| `IN_PROGRESS` | `refID` masih diproses |
| `CENTER_ERROR` | provider gagal/timeout |
| `INTERNAL_ERROR` | kesalahan database/internal |
| `NOT_FOUND` | route tidak ada |
| `SERVER_MISCONFIGURED` | secret server belum diisi |

## Dokumen terkait

- Protokol keluar: [09 — Protokol provider](09-provider-protocol.md)
- Callback: [10 — Webhook](10-webhooks.md)
- Troubleshooting: [17 — Troubleshooting](17-troubleshooting.md)
