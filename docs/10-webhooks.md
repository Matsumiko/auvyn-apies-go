# 10 — Webhook

Service mengirim event keluar melalui pola **outbox**: handler menulis event dan delivery ke database, dispatcher mengirimkannya. Tidak ada pengiriman langsung dari request handler.

## Dua jalur callback

| Jalur | Konfigurasi | Event header | Penerima |
|---|---|---|---|
| Legacy | `CALLBACK_URL`, `CALLBACK_TARGETS`, `CALLBACK_WEB_*`, `CALLBACK_BOT_*`, `CALLBACK_THIRD_*` | `transaction.request`, `transaction.report`, `balance.check`, `balance.ticket` | Satu atau beberapa URL dari environment |
| Managed | Client + webhook di database | `invoice.paid`, `invoice.failed`, `invoice.pending` | Webhook milik client tertentu |

Keduanya dapat aktif bersamaan.

## Routing managed

- Ditentukan dari client yang terautentikasi lewat kredensial client. `meta.client_id` diisi otomatis; jika dikirim manual harus cocok.
- `client_id` juga dapat dikirim sebagai `meta.clientId`/`meta.clientID`; semuanya dinormalisasi.
- Tanpa `client_id`, managed webhook tidak dikirim. Broadcast ke semua client **tidak** diimplementasikan.
- Webhook hanya menerima event yang disubscribe, termasuk wildcard `invoice.*` atau `transaction.*`.

## Pemetaan event

| Sumber | Kategori | Event managed |
|---|---|---|
| request transaksi | pending | `invoice.pending` |
| request transaksi | success | `invoice.paid` |
| request transaksi | failed | `invoice.failed` |
| report | success | `invoice.paid` |
| report | failed | `invoice.failed` |
| report | pending | `invoice.pending` |
| cek saldo | — | tidak ada managed |
| tiket M-Bal | — | tidak ada managed |

Event final di-dedupe per `refID` dan kategori, sehingga provider yang langsung final dan juga mengirim report tetap menghasilkan satu `invoice.paid`/`invoice.failed`.

## Header

```http
POST <webhook-url>
Content-Type: application/json
x-auvyn-event: invoice.paid
x-auvyn-event-id: 123
x-auvyn-callback-secret: <secret>
x-auvyn-timestamp: 1767225600
x-auvyn-signature: <hex hmac>
x-auvyn-webhook-id: avn_wh_example
x-auvyn-client-id: avn_cl_example
x-auvyn-client-name: acme
```

- Legacy menerima `x-auvyn-event`, `x-auvyn-event-id`, `x-auvyn-timestamp`, `x-auvyn-callback-secret`, `x-auvyn-signature`.
- Managed menambahkan `x-auvyn-webhook-id`, `x-auvyn-client-id`, `x-auvyn-client-name`.

`x-auvyn-callback-secret` dipertahankan untuk kompatibilitas. Verifikasi yang disarankan adalah signature.

## Signature

```text
signature = hex( HMAC-SHA256( secret, timestamp + "." + raw_body ) )
```

- `timestamp` = Unix seconds dari header `x-auvyn-timestamp`.
- `secret` = `CALLBACK_SECRET` (legacy) atau `secret_callback` client (managed).
- `raw_body` = byte body persis seperti diterima, bukan hasil parsing ulang.

### Verifikasi Go

```go
func verify(secret, timestamp string, body []byte, signature string) bool {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(signature))
}
```

### Verifikasi Node.js

```js
const crypto = require('crypto');
const expected = crypto
  .createHmac('sha256', secret)
  .update(timestamp + '.' + rawBody)
  .digest('hex');
crypto.timingSafeEqual(Buffer.from(expected), Buffer.from(signature));
```

### Verifikasi PHP

```php
$expected = hash_hmac('sha256', $timestamp . '.' . $rawBody, $secret);
hash_equals($expected, $signature);
```

Terapkan juga toleransi waktu (misalnya 5 menit) terhadap `x-auvyn-timestamp` untuk mencegah replay.

## Payload

Payload transaksi adalah hasil sanitasi:

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
  "meta": {"orderId": "INV-1"},
  "providerResult": {
    "state": "SUCCESS",
    "code": "SUKSES",
    "trxid": "12345",
    "tujuan": "081234567890",
    "sn": "SN-ABC",
    "duplicate": false,
    "raw": "R12345.081234567890 SUKSES. SN/Ref: SN-ABC"
  },
  "raw": "R12345.081234567890 SUKSES. SN/Ref: SN-ABC"
}
```

Managed menambahkan:

```json
{"event": "invoice.paid", "client_id": "avn_cl_example", "client_name": "acme"}
```

Dihilangkan/diredaksi:

- `sign`, `centerUrl`, `memberID`
- `providerResult.sn` → SN/Ref asli untuk client pemilik.
- `raw` mempertahankan `SN/Ref`/`SN:` untuk client; klausa `Saldo`/`Sal`/`M-Bal` dibuang, sedangkan `HRG`, `sign`, `memberID`, URL `/trx` diredaksi.
- Log dan notifikasi Telegram tetap meredaksi SN.
- `balanceInfo` tidak dimasukkan ke callback (meskipun ada di respons API publik)

## Retry dan dead-letter

| Status | Arti |
|---|---|
| `pending` | menunggu jadwal kirim |
| `sending` | sedang dikirim |
| `delivered` | target membalas 2xx |
| `dead` | gagal setelah batas percobaan atau resource dihapus |

- Percobaan pertama segera.
- Backoff default: `1m,5m,30m,2h,6h,24h`.
- Setelah `DELIVERY_MAX_ATTEMPTS` (default 6) → `dead`, log error, alert Telegram sistem.
- Semua respons non-2xx (termasuk 4xx) dianggap gagal dan diretry.
- Redirect diikuti oleh HTTP client; hasil akhir menentukan sukses.
- Delivery `sending` yang tertinggal saat proses berhenti diklaim ulang saat start.
- Menghapus webhook/client membatalkan delivery `pending`/`sending` yang belum terkirim.

## Timeout dan urutan

- Timeout per pengiriman: `CALLBACK_TIMEOUT_MS` (default 15 detik).
- Dispatcher memproses delivery berdasarkan ID secara berurutan; tidak ada jaminan urutan global antar-target.
- Satu event dapat memiliki beberapa delivery (legacy + managed).

## Keamanan URL

Saat webhook dibuat/diubah dan saat pengiriman:

- skema harus `https`, kecuali `ALLOW_INSECURE_WEBHOOKS=true`;
- hostname di-resolve;
- alamat loopback, privat, link-local, multicast, unspecified ditolak kecuali `ALLOW_PRIVATE_WEBHOOKS=true`;
- `localhost` ditolak secara eksplisit.

Ini mencegah SSRF ke layanan internal. DNS rebinding tetap mungkin secara teori; gunakan egress firewall untuk pertahanan tambahan.

## Contoh webhook lokal untuk development

```bash
python3 -m http.server 9999
```

Set:

```env
ALLOW_INSECURE_WEBHOOKS=true
ALLOW_PRIVATE_WEBHOOKS=true
```

Buat client dengan webhook `http://127.0.0.1:9999/cb`. Jangan gunakan kombinasi ini di produksi.

## Checklist integrasi

- [ ] Simpan `callback_secret` dengan aman.
- [ ] Verifikasi HMAC atas raw body dan timestamp.
- [ ] Batasi toleransi timestamp.
- [ ] Idempotensi berdasarkan `x-auvyn-event-id` atau `refID` + event.
- [ ] Balas 2xx cepat; lakukan pemrosesan berat secara asynchronous.
- [ ] Jangan menaruh secret di URL webhook.
- [ ] Tangani event duplikat (provider/report retry).

## Dokumen terkait

- API client: [07 — API client](07-api-client.md)
- Outbox/database: [11 — Database](11-database.md)
- Keamanan: [12 — Keamanan](12-security.md)
