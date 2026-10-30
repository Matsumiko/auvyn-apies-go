# 17 — Troubleshooting

## Diagnosis cepat

```bash
# proses dan log
systemctl status auvyn-apies
journalctl -u auvyn-apies -n 100 --no-pager

# health
curl -s http://127.0.0.1:5882/healthz
curl -s http://127.0.0.1:5882/readyz
curl -s http://127.0.0.1:5882/version

# statistik
curl -s http://127.0.0.1:5882/admin/v1/stats \
  -H "Authorization: Bearer $ADMIN_SECRET" \
  -H "x-admin-pin: $ADMIN_PIN" \
  -H "x-admin-keys: $ADMIN_KEYS"

# database
sqlite3 /opt/auvyn-apies/data/auvyn.db "PRAGMA integrity_check;"
sqlite3 /opt/auvyn-apies/data/auvyn.db "SELECT status,COUNT(*) FROM deliveries GROUP BY 1;"
sqlite3 /opt/auvyn-apies/data/auvyn.db "SELECT ref_id,accepted,reason,category,received_at FROM provider_reports ORDER BY id DESC LIMIT 20;"
```

## Startup

### Service menolak start: konfigurasi

| Pesan | Solusi |
|---|---|
| `CENTER_URL is required` | isi `CENTER_URL` |
| `MEMBER_ID/PIN/PASSWORD is required` | lengkapi kredensial provider |
| `AUVYN_SECRET is required` | isi secret API |
| `report guard is not configured` | isi `REPORT_ALLOW_IPS` atau `REPORT_TOKEN`, atau `ALLOW_INSECURE_REPORT=true` untuk lokal |
| `CALLBACK_TARGETS: ...` | perbaiki JSON array |
| `invalid IP or CIDR` | perbaiki `REPORT_ALLOW_IPS`/`TRUSTED_PROXY_CIDRS` |
| `invalid duration` | perbaiki `WEBHOOK_BACKOFF` |
| `EWALLET_BEBAS_UNIT must be >= 1` | perbaiki nilai |
| `RETENTION_DAYS must be >= 0` | perbaiki nilai |

### `bind: address already in use`

Port dipakai proses lain (termasuk Node lama).

```bash
ss -ltnp | grep 5882
```

Hentikan proses lama atau ubah `PORT`.

### Tidak bisa menulis database

- Pastikan direktori `DATA_DIR` ada dan dimiliki user service.
- Impor sebagai user service, bukan root.
- Cek permission `0600` pada file DB.

## Autentikasi

| Gejala | Penyebab |
|---|---|
| `401 UNAUTHORIZED` pada `/api/transaction` | `client_secret`/PIN/keys salah atau tidak lengkap |
| `401 UNAUTHORIZED` pada `/api/ping`, `/api/balance`, `/api/ticket-mbal` | `x-auvyn-secret` salah/kosong |
| `401` pada `/admin/v1/*` | Bearer/`x-admin-secret`, PIN, atau KEYS salah |
| `500 SERVER_MISCONFIGURED` admin | `ADMIN_SECRET`/`ADMIN_PIN`/`ADMIN_KEYS` kosong |
| `401` pada `/client/v1/*` | secret/PIN/KEYS client salah atau client dihapus |

Bearer harus format `Authorization: Bearer <secret>`.

## Transaksi

### `502 CENTER_ERROR`

Provider tidak dapat dihubungi, timeout, atau respons gagal dibaca.

1. Cek `CENTER_URL` dan konektivitas.
2. Cek log `transaction failed; intent kept`.
3. Jangan mengulang dengan `refID` baru.
4. Rekonsiliasi setelah provider pulih.

### `409 IN_PROGRESS`

Ada intent `refID` yang belum mendapat hasil provider. Bisa karena provider timeout atau proses berhenti di tengah.

- Cek transaksi di DB.
- Rekonsiliasi dengan provider.
- Jangan membuat `refID` baru untuk transaksi yang sama.

### `409 DUPLICATE_REF`

`refID` sudah final dengan `product`/`dest` berbeda, atau sudah ada.

- Gunakan `refID` unik untuk transaksi baru.
- Untuk replay, kirim payload yang sama; transaksi final akan dikembalikan.

### Transaksi selalu `pending`

- Provider memang mengembalikan pending.
- Pola respons tidak dikenali sehingga fallback memilih pending.
- Report final belum diterima.

Cek log respons provider (tanpa raw sensitif) dan `provider_code` di DB.

### Produk bebas nominal ditolak

- `nominal` wajib untuk produk yang terdaftar di `EWALLET_BEBAS_PRODUCTS`.
- `qty: "none"` tidak dapat dipakai untuk produk bebas; qty harus diturunkan dari nominal atau diisi angka.

## Report

### `403 FORBIDDEN source IP not allowed`

- IP report tidak ada di `REPORT_ALLOW_IPS`.
- Di belakang proxy, `TRUSTED_PROXY_CIDRS` salah sehingga IP proxy yang terlihat.
- Provider memakai IP baru.

Ambil IP sebenarnya dari log nginx/provider, verifikasi, lalu perbarui allowlist dan firewall.

### `403 FORBIDDEN invalid report token`

Token tidak cocok. Cek `REPORT_TOKEN`, query `key`/`token`, atau header `X-Report-Token`.

### `200 OK` tetapi transaksi tidak berubah

Cek `provider_reports.reason`:

| Reason | Arti | Tindakan |
|---|---|---|
| `unknown_ref` | refID tidak ada di ledger | cek ID atau backfill log |
| `missing_ref` | report tanpa refID | perbaiki pengirim |
| `dest_mismatch` | tujuan berbeda | bandingkan nomor, cek format |
| `duplicate` | kategori sama sudah final | tidak perlu tindakan |
| `conflict` | final berbeda | rekonsiliasi/refund |
| `applied` | transaksi berubah | normal |

### Report dari domain publik

Jika `GET https://domain/report?...` mengembalikan `200 OK`, berarti reverse proxy membuka path tersebut. Tambahkan `location = /report` allowlist di vhost. Jangan hanya mengandalkan firewall port.

### Konflik status

Provider mengirim final berbeda (mis. success lalu failed). Service tidak menimpa. Simpan bukti, cek refund, dan selesaikan manual.

## Callback

### `Callback error` / `Callback not ok`

- Target tidak dapat dihubungi, DNS, TLS, atau non-2xx.
- Cek `deliveries.last_error`, `response_status`, `attempts`.
- Pastikan URL https dan tidak mengarah ke IP privat (kecuali opt-in).
- Setelah target pulih, delivery dead tidak otomatis dikirim ulang.

### Signature callback tidak cocok

Pastikan:

- HMAC dihitung atas **raw body** (bukan JSON hasil parse ulang);
- string yang di-HMAC adalah `timestamp + "." + body`;
- secret yang dipakai adalah secret saat event dikirim (rotasi dapat mengubah);
- encoding hex lowercase;
- timestamp dari header yang sama.

### Event ganda

- Konsumen harus idempotent berdasarkan `x-auvyn-event-id` atau `refID` + event.
- Service sudah melakukan dedupe internal; duplikat eksternal biasanya berasal dari retry setelah timeout di sisi konsumen.

### Webhook ditolak

- `https` wajib; `http` butuh `ALLOW_INSECURE_WEBHOOKS=true`.
- IP privat/loopback butuh `ALLOW_PRIVATE_WEBHOOKS=true`.
- DNS harus resolve; hostname `localhost` ditolak.

## Notifikasi

### `telegram chat not found`

- Chat ID salah atau bot belum masuk chat.
- `TELEGRAM_SELFCHECK=true` akan mencatat peringatan saat start.
- Perbaiki environment lalu restart.
- Uji `getChat`/`sendMessage` manual.

### Notifikasi tidak masuk tetapi tidak ada error

- Token/chat ID kategori kosong.
- Bot diblokir.
- Log dedupe mencegah pesan error berulang; tunggu 5 menit atau restart.

## Database

### `database is locked`

- Pastikan hanya satu instance.
- Cek proses yang memegang DB.
- `busy_timeout` 5 detik; request yang lebih lama gagal.
- Restart service bila perlu.

### Database membesar

- Transaksi tidak dipangkas (by design).
- Cek `RETENTION_DAYS` dan log prune.
- Backup/arsipkan transaksi lama sesuai kebijakan.
- WAL yang besar akan di-checkpoint saat prune/start.

### `PRAGMA integrity_check` gagal

- Hentikan service.
- Restore backup terakhir.
- Jangan menulis ke DB yang korup.

## Disk dan log

### Disk penuh

```bash
journalctl --disk-usage
du -sh /opt/auvyn-apies/data /opt/auvyn-apies/backup
journalctl --vacuum-size=500M
```

Pastikan `SystemMaxUse` journald dan retensi backup diatur.

### Log terlalu ramai

- `ACCESS_LOG=false`.
- Pastikan error yang berulang tidak berasal dari satu target mati.
- Telegram error sudah di-dedupe 5 menit.
- Delivery gagal tetap diretry sesuai backoff; dead setelah batas.

## Provider

### Semua respons `UNKNOWN`

Format provider belum dikenali. Tambahkan pola baru di parser, test dengan contoh sintetis, dan update dokumentasi. Jangan hanya mengandalkan fallback teks karena `trxid`/`SN`/`tujuan` akan kosong.

### Saldo tidak terparse

- Format bukan `Saldo a - b = c` atau `M-Bal`.
- `balance` null tetapi `providerResult.raw` tersedia (redacted).
- Tambahkan pola saldo jika format baru.

### Tiket kosong

Parser tiket best-effort. Periksa format `TICKET`/`TIKET`/`KODE`; jika berbeda, tambahkan pola.

## Checklist eskalasi

Saat melaporkan bug, sertakan:

- versi (`GET /version`);
- waktu kejadian (UTC);
- `refID`;
- kategori/status;
- potongan log yang sudah diredaksi;
- hasil `GET /admin/v1/stats`;
- langkah reproduksi;
- apakah memakai proxy/firewall.

Jangan pernah mengirim PIN, password, secret, token, SN, atau data pelanggan.

## Dokumen terkait

- Operasional: [13 — Operasional](13-operations.md)
- Report: [08 — Report provider](08-report-ingestion.md)
- Webhook: [10 — Webhook](10-webhooks.md)
