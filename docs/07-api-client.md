# 07 — API client

API client adalah self-service untuk pemilik webhook. Client hanya dapat melihat dan mengubah resource miliknya sendiri.

## Autentikasi

```http
Authorization: Bearer <client_secret>
X-Client-Pin: <client_pin>
X-Client-Keys: <client_keys>
```

Header lama `x-client-secret` tetap diterima sebagai pengganti Bearer. PIN dan KEYS selalu header terpisah.

Gagal autentikasi:

```json
{"error":{"code":"UNAUTHORIZED","message":"missing x-client-secret / x-client-pin / x-client-keys"}}
```

atau `invalid client credentials`.

## Konvensi

- Sukses: `{"data": ...}`.
- Error: `{"error":{"code","message"}}`.
- Waktu RFC3339 UTC.
- Tidak ada pagination pada daftar webhook client (jumlahnya kecil per client).

## Endpoint

| Method | Path | Keterangan |
|---|---|---|
| GET | `/client/v1/me` | profil client |
| GET | `/client/v1/webhooks` | webhook milik sendiri |
| PATCH | `/client/v1/webhooks/{webhook_id}` | ubah URL dan/atau events |
| DELETE | `/client/v1/webhooks/{webhook_id}` | hapus webhook sendiri |
| POST | `/client/v1/callback-secret/rotate` | rotasi secret callback |

## Profil

```http
GET /client/v1/me
```

```json
{
  "data": {
    "client_id": "avn_cl_example",
    "client_name": "acme",
    "status": "ACTIVE",
    "created_at": "2026-01-01T00:00:00.000000000Z",
    "callback_secret_rotated_at": "2026-01-01T00:00:00.000000000Z"
  }
}
```

Tidak pernah mengembalikan hash kredensial atau secret callback.

## List webhook

```http
GET /client/v1/webhooks
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
      "events": ["invoice.paid", "invoice.failed"],
      "created_at": "2026-01-01T00:00:00.000000000Z",
      "updated_at": "2026-01-01T00:00:00.000000000Z"
    }
  ],
  "meta": {"total": 1}
}
```

## Ubah webhook

```http
PATCH /client/v1/webhooks/{webhook_id}
Content-Type: application/json

{
  "url": "https://example.com/new-cb",
  "events": ["invoice.paid"]
}
```

Aturan:

- minimal salah satu dari `url` atau `events` harus ada;
- `url` harus valid dan lolos kebijakan SSRF;
- `events` boleh array atau string dipisah koma;
- `events: []` menghapus semua subscription;
- webhook harus milik client yang terautentikasi.

Respons `200` berisi objek webhook terbaru.

Jika webhook milik client lain → `403 FORBIDDEN`. Jika tidak ada/sudah dihapus → `404 NOT_FOUND`.

## Hapus webhook

```http
DELETE /client/v1/webhooks/{webhook_id}
```

Respons `204`. Delivery pending untuk webhook tersebut dibatalkan. Event yang sudah `delivered` tidak berubah.

## Rotasi secret callback

```http
POST /client/v1/callback-secret/rotate
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

Secret baru digunakan untuk pengiriman berikutnya. Simpan segera; tidak dapat diambil ulang. Secret lama tidak lagi dikirim.

## Legacy alias (deprecated)

Respons lama dipertahankan, dengan header:

```http
Deprecation: true
Link: </client/v1/me>; rel="successor-version"
```

| Legacy | Pengganti |
|---|---|
| `POST /client/rotate/secret-callback` | `POST /client/v1/callback-secret/rotate` |
| `POST /client/rotate/url-callback` | `PATCH /client/v1/webhooks/{webhook_id}` |

### Bentuk legacy

`rotate/secret-callback`:

```json
{
  "ok": true,
  "message": "Secret callback rotated",
  "client_id": "avn_cl_example",
  "secret_callback": {
    "value": "...",
    "length": 126,
    "format": "no_ambiguous_mixed",
    "displayed_once": true,
    "store_now": "Simpan secret ini sekarang. Tidak bisa diambil ulang."
  }
}
```

`rotate/url-callback`:

```json
{
  "ok": true,
  "message": "Webhook updated",
  "webhook": {
    "webhook_id": "avn_wh_example",
    "client_id": "avn_cl_example",
    "client_name": "acme",
    "url": "https://example.com/new-cb",
    "events": ["invoice.paid"],
    "created_at": "...",
    "updated_at": "..."
  }
}
```

Legacy `rotate/url-callback` membutuhkan `webhook_id` di body.

## Kode error

| Status | Code | Arti |
|---|---|---|
| 400 | `BAD_REQUEST` | JSON/URL/events tidak valid |
| 401 | `UNAUTHORIZED` | kredensial client salah |
| 403 | `FORBIDDEN` | webhook milik client lain |
| 404 | `NOT_FOUND` | webhook/client tidak ada |
| 500 | `INTERNAL_ERROR` | database/internal |

## Alur rekomendasi untuk client

1. Simpan `client_secret`, `client_pin`, `client_keys`, dan `callback_secret` dari admin.
2. Verifikasi signature callback dengan `callback_secret`.
3. Gunakan `PATCH` untuk mengubah URL/event, bukan membuat webhook baru.
4. Rotasi `callback_secret` bila diduga bocor.
5. Hapus webhook yang tidak dipakai agar event tidak menumpuk menjadi dead-letter.

## Dokumen terkait

- Verifikasi callback: [10 — Webhook](10-webhooks.md)
- API admin: [06 — API admin](06-api-admin.md)
- Keamanan: [12 — Keamanan](12-security.md)
