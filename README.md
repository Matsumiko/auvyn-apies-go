# Auvyn Apies (Go)

Bridge transaksi IP Center (Otomax-style) versi Go. Menggantikan implementasi Node lama dengan protokol provider yang **identik** (prefix signature, urutan parameter, parsing Otomax), ditambah ledger SQLite, outbox webhook, dan pengamanan `/report` yang tidak menuntut perubahan di sisi provider.

Dokumentasi lengkap per bagian: [`docs/`](docs/README.md).

Dokumen pengikat:

- [`CONTRACT.md`](CONTRACT.md) — protokol provider yang beku + kontrak API/callback + perubahan v2.
- [`SECURITY.md`](SECURITY.md) — model keamanan dan residual risk.

## Kenapa rewrite

| Masalah di versi Node | Perbaikan di v2 |
|---|---|
| Pending transaksi di memori, hilang saat restart | `transactions` + `provider_reports` di SQLite |
| `/report` terbuka untuk siapa pun | allowlist IP + `refID` acak + pencocokan tujuan |
| `invoice.paid` bisa terkirim 2× | `events.dedupe_key UNIQUE` + outbox |
| Callback fire-and-forget, tanpa retry | retry berjenjang + dead-letter + alert |
| SN bocor di log, kredensial di log | SN tersedia untuk client pemilik; log/Telegram meredaksi SN, `M-Bal`, `sign`, URL |
| `status=20` + `GAGAL` dianggap sukses | teks `GAGAL` menang |
| `/api/balance` & `/api/ticket-mbal` tidak mengembalikan nilainya | kontrak respons diperbaiki |
| `/report` terbuka lewat domain nginx meski port sudah di-allowlist UFW | app menolak start tanpa `REPORT_ALLOW_IPS`/`REPORT_TOKEN` |
| Format provider lapangan (`#trxid PRODUCT.dest SUKSES…`, `Saldo`) tidak terparse | pola lapangan ditambahkan; `SN/Ref` tersedia untuk client, `Saldo` diredaksi |
| Chat Telegram sistem salah → alert hilang tanpa terlihat | isi `TG_SYSTEM_CHAT_ID`, self-check saat start, log error di-rate-limit |
| Log/journal tumbuh tanpa batas | stdout + journald cap, prune outbox/audit otomatis |

## Build

```bash
go build -o auvyn ./cmd/auvyn
go test ./...
go vet ./...
```

Binary statis (driver SQLite pure Go, tanpa cgo):

```bash
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o auvyn ./cmd/auvyn
```

## Konfigurasi

Salin `.env.example` menjadi `.env` lalu ekspor. Service tidak membaca `.env` otomatis; gunakan systemd `EnvironmentFile` atau `export $(cat .env | xargs)`.

Wajib: `CENTER_URL`, `MEMBER_ID`, `PIN`, `PASSWORD`, `AUVYN_SECRET`.

| Variabel | Default | Keterangan |
|---|---|---|
| `HOST` | `127.0.0.1` | Bind loopback; expose lewat reverse proxy |
| `PORT` | `5882` | |
| `CENTER_URL` | — | Base URL IP Center |
| `MEMBER_ID`, `PIN`, `PASSWORD` | — | Kredensial agen |
| `SIGN_PREFIX` | `ENGINE` | Prefix signature provider |
| `AUVYN_SECRET` | — | Auth operasional `/api/ping`, `/api/balance`, `/api/ticket-mbal` |
| `ADMIN_SECRET`, `ADMIN_PIN`, `ADMIN_KEYS` | — | Auth `/admin/*` |
| `DATA_DIR` | `./data` | |
| `DB_PATH` | `$DATA_DIR/auvyn.db` | |
| `CALLBACK_URL`, `CALLBACK_SECRET` | — / `AUVYN_SECRET` | Legacy callback |
| `CALLBACK_TARGETS` | — | JSON array `[{"name","url","secret"}]` |
| `MANAGED_WEBHOOKS_ENABLED` | `true` | Webhook per client |
| `CALLBACK_TIMEOUT_MS` | `15000` | |
| `DELIVERY_MAX_ATTEMPTS` | `6` | |
| `WEBHOOK_BACKOFF` | `1m,5m,30m,2h,6h,24h` | |
| `REPORT_TOKEN` | — | Token opsional di query `?key=`/header |
| `REPORT_ALLOW_IPS` | — | CIDR/IP provider, pisahkan koma |
| `ALLOW_INSECURE_REPORT` | `false` | Izinkan `/report` tanpa guard (development saja; start gagal bila false dan guard kosong) |
| `TRUSTED_PROXY_CIDRS` | `127.0.0.1/32,::1/128` | Sumber `X-Real-IP` yang dipercaya |
| `BALANCE_LOW_LIMIT` | `50000` | |
| `EWALLET_BEBAS_PRODUCTS` | — | Daftar kode produk bebas nominal |
| `EWALLET_BEBAS_QTY_IS_NOMINAL` | `true` | |
| `EWALLET_BEBAS_UNIT` | `1000` | |
| `SIGN_INCLUDE_NOMINAL` | `false` | |
| `ALLOW_INSECURE_WEBHOOKS` | `false` | Izinkan `http://` |
| `ALLOW_PRIVATE_WEBHOOKS` | `false` | Izinkan IP privat (SSRF) |
| `LOG_LEVEL` | `info` | |
| `TG_*_BOT_TOKEN`, `TG_*_CHAT_ID` | — | Notifikasi Telegram |
| `TG_*_THREAD_ID` | kosong | `message_thread_id` untuk grup dengan Topics |
| `TG_BALANCE_THREAD_ID` | kosong | Topic khusus alert saldo |
| `TELEGRAM_SELFCHECK` | `true` | Verifikasi bot/chat saat start |
| `TG_ADMIN_BOT_TOKEN` | — | Token bot admin (opsional; bot terpisah) |
| `TG_ADMIN_CHAT_IDS` | — | Private chat ID admin, pisahkan koma |
| `TG_ADMIN_API_BASE` | `https://api.telegram.org` | Basis API Telegram |
| `ACCESS_LOG` | `true` | Log akses per request (path saja, tanpa query) |
| `RETENTION_DAYS` | `365` | Retensi outbox & audit report; transaksi tidak dihapus. `0` = nonaktif |

## API

`POST /api/transaction` **wajib** memakai kredensial client (`Authorization: Bearer <client_secret>`, `X-Client-Pin`, `X-Client-Keys`). `x-auvyn-secret` hanya untuk `/api/ping`, `/api/balance`, `/api/ticket-mbal`. Query string tidak didukung.

| Endpoint | Body | Respons |
|---|---|---|
| `POST /api/ping` | — | `{ok,message,time}` |
| `POST /api/transaction` | `product`, `dest`, `qty?`, `refID?`, `nominal?`, `meta?` | payload transaksi tersanitasi + `balanceInfo` |
| `GET /api/transaction/status?refID=` | — | status ledger client-scoped |
| `POST /api/transaction/status` | `refID` atau `refId` | status ledger client-scoped |
| `POST /api/balance` | `memberId?`, `meta?` | `{ok,statusCode,memberId,balance,balanceRaw,providerResult,meta,receivedAt}` |
| `POST /api/ticket-mbal` | `amount`, `memberId?`, `meta?` | `{ok,statusCode,memberId,ticket,amount,providerResult,meta,receivedAt}`; `ticket` dapat kosong bila provider hanya memberi instruksi transfer |

`refID` dibuat acak (`AVN` + 64-bit base32, total 16 karakter) bila tidak dikirim. `refID` duplikat yang sudah final dikembalikan sebagai replay `200` tanpa memanggil provider; placeholder yang masih diproses atau payload berbeda menghasilkan `409`.

Jika provider timeout / koneksi putus, API mengembalikan `502` tetapi **intent tetap disimpan** (karena provider mungkin sudah menerima request). Retry dengan `refID` yang sama akan menghasilkan `409 IN_PROGRESS`, bukan charge kedua. Rekonsiliasi manual lewat `transactions` / `provider_reports`.

### Admin & client API (v1)

Auth admin:

```http
Authorization: Bearer <ADMIN_SECRET>
X-Admin-Pin: <ADMIN_PIN>
X-Admin-Keys: <ADMIN_KEYS>
```

Auth client:

```http
Authorization: Bearer <client_secret>
X-Client-Pin: <client_pin>
X-Client-Keys: <client_keys>
```

Header `x-admin-secret` / `x-client-secret` lama tetap diterima (deprecated).

Konvensi:

- resource-oriented, ID di path, verb HTTP menentukan aksi;
- sukses: `{"data": ...}` (+ `meta` untuk list); error: `{"error":{"code","message"}}`;
- list memakai `?limit=` (default 50, max 200) dan `?offset=` dengan `meta.total/limit/offset`;
- `POST` create → `201` + `Location`; `DELETE` → `204`; validasi → `400`; auth → `401/403`; hilang → `404`; konflik → `409`.

| Method | Path | Keterangan |
|---|---|---|
| GET | `/admin/v1/clients` | list client |
| POST | `/admin/v1/clients` | buat client (+ webhook opsional) |
| GET | `/admin/v1/clients/{client_id}` | detail client |
| DELETE | `/admin/v1/clients/{client_id}` | soft delete |
| GET | `/admin/v1/clients/{client_id}/webhooks` | webhook milik client |
| POST | `/admin/v1/clients/{client_id}/webhooks` | tambah webhook |
| POST | `/admin/v1/clients/{client_id}/callback-secret/rotate` | rotasi secret callback |
| GET | `/admin/v1/webhooks` | list semua webhook |
| GET | `/admin/v1/webhooks/{webhook_id}` | detail webhook |
| PATCH | `/admin/v1/webhooks/{webhook_id}` | ubah `url` dan/atau `events` |
| DELETE | `/admin/v1/webhooks/{webhook_id}` | soft delete webhook |
| GET | `/admin/v1/stats` | metrik operasional |
| GET | `/client/v1/me` | profil client |
| GET | `/client/v1/webhooks` | webhook milik sendiri |
| PATCH | `/client/v1/webhooks/{webhook_id}` | ubah `url`/`events` |
| DELETE | `/client/v1/webhooks/{webhook_id}` | hapus webhook sendiri |
| POST | `/client/v1/callback-secret/rotate` | rotasi secret callback |

Contoh create:

```json
{
  "client_name": "acme",
  "webhook": {"url": "https://example.com/cb", "events": ["invoice.paid", "invoice.failed"]}
}
```

`credentials` dan `callback_secret` hanya ditampilkan sekali pada respons create/rotate. `webhook` opsional; kalau tidak dikirim, buat webhook terpisah lewat endpoint webhooks.

**Legacy (deprecated, tetap jalan):** respons lama dipertahankan, ditandai header `Deprecation: true` dan `Link: <pengganti>; rel="successor-version"`.

| Legacy | Pengganti |
|---|---|
| `GET /admin/client/list-clients` | `GET /admin/v1/clients` |
| `GET /admin/client/list-webhook` | `GET /admin/v1/webhooks` |
| `POST /admin/client/add-clients` | `POST /admin/v1/clients` |
| `POST /admin/client/del-clients` | `DELETE /admin/v1/clients/{client_id}` |
| `POST /client/rotate/secret-callback` | `POST /client/v1/callback-secret/rotate` |
| `POST /client/rotate/url-callback` | `PATCH /client/v1/webhooks/{webhook_id}` |

Health: `GET /healthz`, `GET /readyz` (ping DB), `GET /version`.
Admin stats: `GET /admin/v1/stats` (juga tersedia di `/admin/stats` untuk kompatibilitas).

## `/report`

Provider memanggil URL yang sudah terpasang di sisi mereka; kita tidak bisa meminta header tambahan. Identitas report:

1. allowlist IP (`REPORT_ALLOW_IPS`) di app dan/atau reverse proxy;
2. `refID` yang tidak bisa ditebak (dibuat acak bila caller tidak mengirim);
3. kecocokan `dest` dengan record;
4. rate limit di reverse proxy.

**Secure by default:** service menolak start bila `REPORT_ALLOW_IPS` dan `REPORT_TOKEN` sama-sama kosong, kecuali `ALLOW_INSECURE_REPORT=true`. Ini mencegah pengalaman lapangan di mana port sudah di-allowlist UFW tetapi domain nginx tetap mem-proxy `/report` dari publik.

Contoh: `REPORT_ALLOW_IPS=203.0.113.10/32`. Tambahkan juga `location = /report { allow 203.0.113.10; deny all; }` di vhost nginx — lihat `deploy/nginx-report-example.conf`.

Respons selalu `200 OK` (kecuali IP/token ditolak) agar provider tidak retry storm. `refID` asing/konflik dicatat di `provider_reports` dan di-alert.

## Telegram admin bot

Bot admin opsional berjalan di proses yang sama dan memakai long polling, tanpa endpoint HTTP tambahan. Isi `TG_ADMIN_BOT_TOKEN` dan `TG_ADMIN_CHAT_IDS` (private chat saja).

```text
/start
/menu
/status
/balance
/clients [limit]
/client <client_id>
/client_add <name> | <url|-> | [events]
/client_del <client_id> confirm
/rotate <client_id> confirm
/webhooks [client_id]
/webhook_add <client_id> <url> [events]
/webhook_set <webhook_id> <url|-> [events]
/webhook_del <webhook_id> confirm
/trx <refID>
```

Perintah destruktif wajib `confirm`. Saat start bot memasang command menu dan menu button Telegram; `/start`/`/menu` menampilkan menu inline bertingkat. Menu Clients/Webhooks punya daftar 10 per halaman, pencarian ID, tambah/ubah/hapus, dan konfirmasi inline; Status/Saldo/Bantuan langsung menampilkan hasil. Gunakan token bot terpisah dari bot notifikasi; bot memanggil `deleteWebhook` saat start. Detail di [docs/13-operations.md](docs/13-operations.md#telegram-admin-bot).

## Logging & retention

- Log ditulis ke **stdout** (JSON), bukan file yang tumbuh di disk. systemd/journald yang merotasi — pasang `deploy/journald.conf.d/auvyn-apies.conf` (`SystemMaxUse=500M`).
- Request access log hanya mencatat path, bukan query string, dan bisa dimatikan dengan `ACCESS_LOG=false`.
- Tidak ada raw provider/SN/kredensial di log; error `*url.Error` di-strip tanpa URL (token/pin tidak ikut).
- Telegram: pesan di-escape, dipotong, error “chat not found” dicatat sekali per 5 menit; self-check saat start memverifikasi token/chat.
- `RETENTION_DAYS` menjalankan prune harian: delivery `delivered`/`dead`, event tanpa delivery, dan `provider_reports` lama. **Tabel `transactions` tidak pernah dihapus.**
- Setelah prune, WAL di-checkpoint agar file `-wal` tidak membengkak.

## Callback

Legacy (`CALLBACK_URL`/`CALLBACK_TARGETS`) dan managed (per client) dikirim lewat outbox:

- `x-auvyn-event`, `x-auvyn-event-id`, `x-auvyn-timestamp`
- `x-auvyn-callback-secret` (kompatibilitas)
- `x-auvyn-signature: HMAC-SHA256(secret, timestamp + "." + body)`
- managed: `x-auvyn-webhook-id`, `x-auvyn-client-id`, `x-auvyn-client-name`

Event: `transaction.request`, `transaction.report`, `balance.check`, `balance.ticket`. Webhook client menerima `invoice.paid|failed|pending` sesuai kategori; event final di-dedupe per `refID` sehingga provider yang langsung final dan juga mengirim report tetap menghasilkan satu `invoice.paid`/`invoice.failed`.

## Import data lama

```bash
./auvyn import-json /path/to/data/db.json
./auvyn import-logs   /path/to/logs          # trx-pending.log, trx-success.log, trx-failed.log
```

Jalankan impor sebagai user service agar file DB tetap bisa ditulis:

```bash
sudo -u auvyn env DB_PATH=/opt/auvyn-apies/data/auvyn.db \
  /opt/auvyn-apies/auvyn import-json /path/to/legacy/data/db.json
sudo -u auvyn env DB_PATH=/opt/auvyn-apies/data/auvyn.db \
  /opt/auvyn-apies/auvyn import-logs /path/to/legacy/logs
```

- `import-json` menyalin client dan webhook beserta hash kredensial dan `secret_callback`.
- `import-logs` mem-backfill transaksi historis dari log JSONL Node, mem-parse ulang `raw` dengan pola lapangan (trxid/SN/dest), dan memakai status final yang menang. Baris report tanpa `product/dest` (mis. probe `refID` asing) dilewati.
- Impor idempotent: aman dijalankan ulang. Tanpa backfill ini, report yang telat datang setelah cutover untuk `refID` lama akan ditolak sebagai unknown.

## Cutover dari Node (pengalaman lapangan)

1. **Port**: provider bisa memanggil `IP:5899` langsung, bukan lewat domain. Go default bind `127.0.0.1`; untuk pola itu set `HOST=0.0.0.0` dan andalkan UFW (`allow from <provider-ip> to any port 5899`) atau tambahkan listener proxy di 5899. Jangan matikan Node sebelum report provider terbukti masuk ke Go.
2. **Guard**: isi `REPORT_ALLOW_IPS` dengan IP egress provider yang benar-benar terlihat di `report.log` (contoh: `203.0.113.10/32`). Jangan `0.0.0.0/0`.
3. **Nginx**: pasang `deploy/nginx-report-example.conf` di semua vhost yang mem-proxy ke service. Tanpa itu, `/report` tetap terbuka lewat domain meski UFW membatasi port.
4. **Data**: `import-json` lalu `import-logs` sebagai user service. Verifikasi `GET /admin/stats` (jumlah transaksi, stale intent, delivery).
5. **Retensi**: default 365 hari untuk outbox/audit. Set `RETENTION_DAYS=730` bila riwayat `provider_reports` ingin disimpan lebih lama.
6. **Rollback**: hentikan systemd Go, start PM2 Node lagi. Keduanya memakai port yang sama, jadi jangan dijalankan bersamaan.
7. **Shadow**: jalankan Go di port lain dengan salinan DB, replay report historis dari log, bandingkan kategori/callback sebelum switch.

## Deploy (systemd)

```bash
# user & directories
useradd --system --home /opt/auvyn-apies --shell /usr/sbin/nologin auvyn || true
install -d -o auvyn -g auvyn /opt/auvyn-apies /opt/auvyn-apies/data

# binary, unit, env
install -m 0755 auvyn /opt/auvyn-apies/auvyn
install -m 0644 deploy/auvyn-apies.service /etc/systemd/system/
install -m 0600 -o auvyn -g auvyn .env /opt/auvyn-apies/.env

# journald retention + nginx /report guard
install -d /etc/systemd/journald.conf.d
install -m 0644 deploy/journald.conf.d/auvyn-apies.conf /etc/systemd/journald.conf.d/
install -m 0644 deploy/nginx-report-example.conf /etc/nginx/snippets/auvyn-report.conf
# lalu include snippet di dalam server{} HTTPS yang mem-proxy ke service

systemctl daemon-reload
systemctl restart systemd-journald
systemctl enable --now auvyn-apies
journalctl -u auvyn-apies -f
```

Reverse proxy wajib menegakkan allowlist `/report`; contoh lengkap di `deploy/nginx-report-example.conf` dan `SECURITY.md`.

## Backup

```bash
DB_PATH=/opt/auvyn-apies/data/auvyn.db BACKUP_DIR=/opt/auvyn-apies/backup ./deploy/backup.sh
```

Tambahkan ke cron harian. `VACUUM INTO` aman dilakukan saat service berjalan.

## Catatan kompatibilitas

- Response `/api/balance` dan `/api/ticket-mbal` sengaja diperbaiki; konsumen lama yang membaca field non-standar harus disesuaikan.
- `/api/ticket-mbal` dapat mengembalikan `ticket: ""` jika provider membalas instruksi transfer deposit; nominal transfer dan rekening ada di `providerResult.raw`.
- `/report` menolak `refID` yang tidak ada di ledger.
- Callback mengirim signature baru di samping header secret lama; konsumen bisa migrasi bertahap.
- `?secret=` untuk `/api/*` dihapus.

## License

MIT — lihat [LICENSE](LICENSE).
