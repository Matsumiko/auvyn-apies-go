# 06 — API admin

API admin mengelola client dan webhook. Versi resource-oriented berada di `/admin/v1/*`; path lama tetap tersedia sebagai alias deprecated.

Untuk operasi cepat dari ponsel, tersedia [Telegram admin bot](13-operations.md#telegram-admin-bot) dengan allowlist private chat dan konfirmasi untuk perintah destruktif. Bot memakai store langsung, bukan endpoint HTTP ini.

## Autentikasi

```http
Authorization: Bearer <ADMIN_SECRET>
X-Admin-Pin: <ADMIN_PIN>
X-Admin-Keys: <ADMIN_KEYS>
```

Header lama `x-admin-secret` tetap diterima sebagai pengganti Bearer:

```http
x-admin-secret: <ADMIN_SECRET>
x-admin-pin: <ADMIN_PIN>
x-admin-keys: <ADMIN_KEYS>
```

Jika kredensial admin belum dikonfigurasi, endpoint mengembalikan `500 SERVER_MISCONFIGURED`. Jika salah, `401 UNAUTHORIZED`.

## Konvensi v1

- Sukses: `{"data": ...}`; list menambahkan `meta`.
- Error: `{"error":{"code":"...","message":"..."}}`.
- Create: `201` + header `Location`.
- Delete: `204` tanpa body.
- Pagination: `?limit=` (default 50, maks 200), `?offset=` (default 0).
- List meta: `{"total":N,"limit":L,"offset":O}`.
- Waktu: RFC3339 UTC.
- Resource client memakai `created_at`; resource webhook memakai `created_at`/`updated_at`.

## Endpoint

| Method | Path | Keterangan |
|---|---|---|
| GET | `/admin/v1/clients` | list client aktif |
| POST | `/admin/v1/clients` | buat client (+ webhook opsional) |
| GET | `/admin/v1/clients/{client_id}` | detail client |
| DELETE | `/admin/v1/clients/{client_id}` | soft delete client + webhook |
| GET | `/admin/v1/clients/{client_id}/webhooks` | webhook milik client |
| POST | `/admin/v1/clients/{client_id}/webhooks` | tambah webhook |
| POST | `/admin/v1/clients/{client_id}/callback-secret/rotate` | rotasi secret callback |
| GET | `/admin/v1/webhooks` | list webhook aktif |
| GET | `/admin/v1/webhooks/{webhook_id}` | detail webhook |
| PATCH | `/admin/v1/webhooks/{webhook_id}` | ubah `url`/`events` |
| DELETE | `/admin/v1/webhooks/{webhook_id}` | soft delete webhook |
| GET | `/admin/v1/stats` | statistik operasional |

## List client

```http
GET /admin/v1/clients?limit=50&offset=0
```

```json
{
  "data": [
    {
      "client_id": "avn_cl_example",
      "client_name": "acme",
      "status": "ACTIVE",
      "created_at": "2026-01-01T00:00:00.000000000Z"
    }
  ],
  "meta": {"total": 1, "limit": 50, "offset": 0}
}
```

`limit` harus 1–200; `offset` >= 0. Nilai invalid → `400 BAD_REQUEST`.

## Buat client

```http
POST /admin/v1/clients
Content-Type: application/json

{
  "client_name": "acme",
  "webhook": {
    "url": "https://example.com/cb",
    "events": ["invoice.paid", "invoice.failed", "invoice.pending"]
  }
}
```

`webhook` opsional. Bila `events` kosong/tidak dikirim, default tiga event invoice.

Respons `201 Created`:

```json
{
  "data": {
    "client": {
      "client_id": "avn_cl_example",
      "client_name": "acme",
      "status": "ACTIVE",
      "created_at": "2026-01-01T00:00:00.000000000Z",
      "callback_secret_rotated_at": "2026-01-01T00:00:00.000000000Z"
    },
    "credentials": {
      "client_secret": "avn_sec_...",
      "client_pin": "avn_pin_...",
      "client_keys": "avn_key_..."
    },
    "callback_secret": {
      "value": "126-character-secret",
      "length": 126,
      "displayed_once": true
    },
    "webhook": {
      "webhook_id": "avn_wh_example",
      "client_id": "avn_cl_example",
      "client_name": "acme",
      "url": "https://example.com/cb",
      "status": "ACTIVE",
      "events": ["invoice.paid", "invoice.failed", "invoice.pending"],
      "created_at": "2026-01-01T00:00:00.000000000Z",
      "updated_at": "2026-01-01T00:00:00.000000000Z"
    }
  },
  "meta": {"displayed_once": true}
}
```

**Simpan `credentials` dan `callback_secret` sekarang.** Nilai plaintext tidak dapat diambil ulang; secret callback hanya bisa dirotasi.

Validasi:

- `client_name` wajib;
- `webhook.url` wajib bila objek webhook dikirim;
- URL harus `https` kecuali `ALLOW_INSECURE_WEBHOOKS=true`;
- URL privat/loopback ditolak kecuali `ALLOW_PRIVATE_WEBHOOKS=true`;
- `events` boleh array atau string dipisah koma.

## Detail, delete client

```http
GET /admin/v1/clients/{client_id}
```

```json
{"data":{"client_id":"...","client_name":"acme","status":"ACTIVE","created_at":"..."}}
```

```http
DELETE /admin/v1/clients/{client_id}
```

Respons `204`. Client dan webhook ditandai `DELETED`; delivery yang masih `pending`/`sending` untuk client tersebut dibatalkan (`dead`). Data transaksi tetap disimpan.

Client yang sudah dihapus → `404 NOT_FOUND`.

## Webhook milik client

```http
GET /admin/v1/clients/{client_id}/webhooks
```

```json
{
  "data": [
    {
      "webhook_id": "avn_wh_example",
      "client_id": "avn_cl_example",
      "client_name": "acme",
      "url": "https://example.com/cb",
      "status": "ACTIVE",
      "events": ["invoice.paid"],
      "created_at": "...",
      "updated_at": "..."
    }
  ],
  "meta": {"total": 1}
}
```

Endpoint ini tidak memakai pagination; mengembalikan webhook aktif milik client.

### Tambah webhook

```http
POST /admin/v1/clients/{client_id}/webhooks
Content-Type: application/json

{"url":"https://example.com/cb","events":["invoice.paid"]}
```

Respons `201` + `Location: /admin/v1/webhooks/{webhook_id}`.

Client harus aktif; jika tidak → `404`/`409`.

## Webhook

### List

```http
GET /admin/v1/webhooks?limit=50&offset=0
```

```json
{
  "data": [ { "...": "webhook object" } ],
  "meta": {"total": 5, "limit": 50, "offset": 0}
}
```

### Detail

```http
GET /admin/v1/webhooks/{webhook_id}
```

### Update

```http
PATCH /admin/v1/webhooks/{webhook_id}
Content-Type: application/json

{"url":"https://example.com/new-cb","events":["invoice.paid","invoice.failed"]}
```

Minimal salah satu dari `url` atau `events` harus ada. `events: []` menghapus semua subscription.

### Delete

```http
DELETE /admin/v1/webhooks/{webhook_id}
```

Respons `204`. Delivery pending untuk webhook tersebut dibatalkan.

## Rotasi secret callback

```http
POST /admin/v1/clients/{client_id}/callback-secret/rotate
```

```json
{
  "data": {
    "client_id": "avn_cl_example",
    "callback_secret": "126-character-secret"
  },
  "meta": {"displayed_once": true}
}
```

Secret lama langsung tidak valid untuk pengiriman berikutnya. Delivery yang sedang berjalan dapat memakai secret baru karena secret dibaca saat pengiriman.

## Statistik

```http
GET /admin/v1/stats
```

```json
{
  "data": {
    "version": "2.0.0",
    "uptime_seconds": 3600,
    "stats": {
      "transactionsByCategory": {"pending": 1, "success": 10, "failed": 2},
      "staleIntents": 0,
      "events": 25,
      "deliveries": {"pending": 0, "delivered": 24, "dead": 1},
      "providerReports24h": 13,
      "rejectedReports24h": 0,
      "clients": 2,
      "webhooks": 3,
      "dbSizeBytes": 245760
    }
  }
}
```

Arti:

- `staleIntents`: transaksi `pending` tanpa hasil provider lebih dari 15 menit.
- `deliveries`: jumlah per status.
- `rejectedReports24h`: report ditolak/konflik dalam 24 jam.
- `dbSizeBytes`: ukuran file database utama.

## Legacy alias (deprecated)

Respons lama dipertahankan. Setiap respons menyertakan:

```http
Deprecation: true
Link: </admin/v1/clients>; rel="successor-version"
```

| Legacy | Pengganti |
|---|---|
| `GET /admin/client/list-clients` | `GET /admin/v1/clients` |
| `GET /admin/client/list-webhook` | `GET /admin/v1/webhooks` |
| `POST /admin/client/add-clients` | `POST /admin/v1/clients` |
| `POST /admin/client/del-clients` | `DELETE /admin/v1/clients/{client_id}` |
| `GET /admin/stats` | `GET /admin/v1/stats` (envelope berbeda) |

### Bentuk legacy

`list-clients`:

```json
{"ok":true,"clients":[{"client_id":"...","client_name":"acme","status":"ACTIVE","registered_at":"..."}]}
```

`list-webhook`:

```json
{"ok":true,"webhooks":[{"webhook_id":"...","client_id":"...","client_name":"acme","url":"https://example.com/cb","events":["invoice.paid"],"created_at":"..."}]}
```

`add-clients`:

```json
{
  "ok": true,
  "message": "Client registered",
  "client": {
    "client_id": "...",
    "client_name": "acme",
    "status": "ACTIVE",
    "registered_at": "...",
    "client_secret": "...",
    "client_pin": "...",
    "client_keys": "..."
  },
  "webhook": {"webhook_id": "...", "url": "https://example.com/cb", "events": ["invoice.paid"]},
  "secret_callback": {"value": "...", "length": 126, "format": "no_ambiguous_mixed", "displayed_once": true, "store_now": "..."}
}
```

`del-clients`:

```json
{"ok":true,"message":"Client deleted"}
```

## Kode error

| Status | Code | Arti |
|---|---|---|
| 400 | `BAD_REQUEST` | JSON/URL/events/pagination tidak valid |
| 401 | `UNAUTHORIZED` | kredensial admin salah |
| 403 | `FORBIDDEN` | resource milik client lain (client API) |
| 404 | `NOT_FOUND` | client/webhook tidak ada atau sudah dihapus |
| 409 | `NOT_ACTIVE` | client/webhook tidak aktif |
| 500 | `SERVER_MISCONFIGURED` | kredensial admin belum diisi |
| 500 | `INTERNAL_ERROR` | database/internal |

## Dokumen terkait

- API client: [07 — API client](07-api-client.md)
- Webhook: [10 — Webhook](10-webhooks.md)
- Keamanan: [12 — Keamanan](12-security.md)
