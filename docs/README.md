# Dokumentasi Auvyn Apies v2

Dokumentasi ini menjelaskan service bridge IP Center (Otomax-style) yang ditulis dalam Go. Versi service mengikuti `internal/version` dan dapat dilihat dari `GET /version`.

Dokumentasi ditulis dalam Bahasa Indonesia. Semua contoh memakai nilai dummy:

- IP provider: `203.0.113.10`
- Domain: `example.com`
- Nomor tujuan: `081234567890`
- Secret: `change-me`

---

## Peta dokumentasi

| Dokumen | Isi |
|---|---|
| [01 — Ikhtisar](01-overview.md) | Masalah yang diselesaikan, fitur, alur transaksi |
| [02 — Arsitektur](02-architecture.md) | Komponen, boundary, request lifecycle, keputusan desain |
| [03 — Mulai cepat](03-getting-started.md) | Build, konfigurasi minimum, menjalankan, request pertama |
| [04 — Konfigurasi](04-configuration.md) | Semua environment variable, default, validasi startup |
| [05 — API publik](05-api-public.md) | `/api/*`: transaksi, saldo, tiket, ping |
| [06 — API admin](06-api-admin.md) | `/admin/v1/*`, legacy alias, pagination, stats |
| [07 — API client](07-api-client.md) | `/client/v1/*`, self-service webhook |
| [08 — Report provider](08-report-ingestion.md) | `/report`, allowlist IP, state machine, konflik |
| [09 — Protokol provider](09-provider-protocol.md) | Signature, URL keluar, parser Otomax, bebas nominal |
| [10 — Webhook](10-webhooks.md) | Event, HMAC, retry, dead-letter, payload sanitasi |
| [11 — Database](11-database.md) | Skema SQLite, transaksi, outbox, retensi, backup |
| [12 — Keamanan](12-security.md) | Trust boundary, kontrol, rahasia, residual risk |
| [13 — Operasional](13-operations.md) | Log, health, stats, retensi, alert, runbook |
| [14 — Deployment](14-deployment.md) | systemd, nginx, journald, firewall, cutover, rollback |
| [15 — Pengembangan](15-development.md) | Struktur kode, test, menambah pola/endpoint, CI |
| [16 — Migrasi](16-migration.md) | Impor `db.json`, backfill log, verifikasi cutover |
| [17 — Troubleshooting](17-troubleshooting.md) | Gejala, penyebab, diagnosis, solusi |
| [18 — Kode error](18-error-codes.md) | Katalog kode error per surface |
| [19 — Glosarium](19-glossary.md) | Istilah domain dan teknis |
| [20 — Protokol Otomax](20-otomax-protocol.md) | Ringkasan protokol Otomax/IP Center + pemetaan ke project |
| [21 — Format WhatsApp bot center](21-whatsapp-bot-center.md) | Format perintah WhatsApp bot center (dokumentasi operasional) |

Dokumen pengikat di root repository:

- `CONTRACT.md` — kontrak protokol yang beku dan perubahan perilaku v2
- `SECURITY.md` — model keamanan ringkas
- `README.md` — ringkasan proyek dan quick start

---

## Konvensi

### Format data

- JSON memakai `snake_case` untuk resource admin/client v1.
- API publik legacy memakai `camelCase` (`refID`, `statusCode`) untuk kompatibilitas.
- Waktu ditulis dalam RFC3339 UTC, misalnya `2026-01-01T00:00:00.000000000Z`.
- Uang dalam rupiah integer (`int64`), tanpa desimal.

### Envelope

API admin/client v1:

```json
{"data": {}, "meta": {}}
```

Error admin/client v1:

```json
{"error": {"code": "BAD_REQUEST", "message": "..."}}
```

API publik dan legacy tetap memakai envelope lama:

```json
{"ok": false, "error": "BAD_REQUEST", "message": "..."}
```

### Penomoran HTTP

| Status | Arti umum |
|---|---|
| 200 | sukses |
| 201 | resource dibuat |
| 204 | sukses tanpa body |
| 400 | input tidak valid |
| 401 | autentikasi gagal |
| 403 | terautentikasi tetapi tidak berwenang |
| 404 | resource tidak ditemukan |
| 409 | konflik state |
| 429 | rate limit (biasanya di reverse proxy) |
| 500 | kesalahan internal |
| 502 | provider tidak dapat dihubungi / respons gagal |

### Placeholder

`203.0.113.10` adalah alamat dokumentasi (TEST-NET-3), bukan alamat nyata. Ganti dengan IP egress provider yang diverifikasi dari log report.

---

## Mulai dari mana?

- Ingin menjalankan: [03 — Mulai cepat](03-getting-started.md)
- Ingin integrasi backend: [05 — API publik](05-api-public.md)
- Ingin mengelola client/webhook: [06 — API admin](06-api-admin.md)
- Ingin menangani callback provider: [08 — Report provider](08-report-ingestion.md)
- Ingin deploy: [14 — Deployment](14-deployment.md)
- Ada error: [17 — Troubleshooting](17-troubleshooting.md)
