# 13 — Operasional

Dokumen ini menjelaskan cara memantau, merawat, dan menangani kegagalan service.

## Startup

Saat start, service:

1. memvalidasi environment (fail-fast);
2. membuka SQLite dan menjalankan skema idempotent;
3. menjalankan worker: dispatcher outbox, prune pertama (jika retensi aktif), dan self-check Telegram (jika aktif);
4. menulis log startup berisi versi, path DB, alamat, jumlah client/webhook, kategori transaksi, delivery pending/dead, dan retensi;
5. mulai menerima HTTP.

Jika konfigurasi wajib kosong, proses berhenti dengan pesan jelas, bukan menerima request yang pasti gagal.

## Log

- Format: JSON ke stdout.
- Setiap baris memiliki `time`, `level`, `msg`, dan atribut konteks.
- Access log: `method`, `path`, `status`, `durationMs`, `ip`.
- Query string tidak pernah dicatat.
- Raw provider, PIN, password, sign, SN, saldo, dan token tidak dicatat.
- Error provider di-strip dari URL.
- `ACCESS_LOG=false` menonaktifkan access log.

Contoh:

```json
{"time":"2026-01-01T00:00:00Z","level":"INFO","msg":"request","method":"POST","path":"/api/transaction","status":200,"durationMs":42,"ip":"10.0.0.5"}
```

### Rotasi

Service tidak menulis file log aplikasi. systemd/journald yang menyimpan dan merotasi. Contoh drop-in:

```ini
[Journal]
SystemMaxUse=500M
SystemMaxFileSize=50M
MaxRetentionSec=30day
```

Pasang di `/etc/systemd/journald.conf.d/auvyn-apies.conf`, lalu restart journald. Untuk membersihkan yang sudah ada:

```bash
journalctl --vacuum-size=500M
```

Jika berjalan di container, atur rotasi pada driver log.

## Health dan readiness

| Endpoint | Fungsi |
|---|---|
| `GET /healthz` | proses hidup |
| `GET /readyz` | database dapat di-ping |
| `GET /version` | versi service |

`/readyz` mengembalikan `503` bila database tidak siap. Gunakan untuk load balancer/systemd watchdog.

## Statistik

```http
GET /admin/v1/stats
Authorization: Bearer <ADMIN_SECRET>
X-Admin-Pin: ...
X-Admin-Keys: ...
```

Metrik penting:

- `transactionsByCategory`: distribusi status.
- `staleIntents`: transaksi pending > 15 menit tanpa hasil provider.
- `deliveries`: pending/sending/delivered/dead.
- `providerReports24h`, `rejectedReports24h`: volume dan penolakan.
- `dbSizeBytes`.
- `uptime_seconds`.

Pemantauan yang disarankan:

- `staleIntents` > 0 → selidiki provider/report.
- `deliveries.dead` naik → periksa webhook client.
- `rejectedReports24h` naik → kemungkinan salah IP/refID/serangan.
- `dbSizeBytes` naik terus → cek retensi/transaksi.
- `deliveries.pending` menumpuk → dispatcher/DB/network.

## Retensi dan prune

`RETENTION_DAYS` default 365. Prune berjalan saat startup dan setiap 24 jam:

- delivery `delivered`/`dead`;
- event tanpa delivery;
- `provider_reports` lama;
- WAL checkpoint.

Transaksi tidak dipangkas. Hasil prune dilog.

## Backup

Harian:

```bash
DB_PATH=/opt/auvyn-apies/data/auvyn.db \
BACKUP_DIR=/opt/auvyn-apies/backup \
KEEP_DAYS=14 \
/opt/auvyn-apies/deploy/backup.sh
```

`VACUUM INTO` aman saat service berjalan. Uji restore secara berkala.

## Graceful shutdown

SIGINT/SIGTERM:

1. HTTP server berhenti menerima request baru;
2. context dispatcher dibatalkan;
3. worker (dispatcher, maintenance, self-check) ditunggu;
4. database ditutup.

Delivery yang sedang dikirim dapat gagal dan akan diretry pada start berikutnya.

## Alert dan notifikasi

Kategori bot:

| Bot | Isi |
|---|---|
| SUCCESS | transaksi sukses |
| PENDING | transaksi pending |
| FAILED | transaksi gagal |
| SYSTEM | report, saldo rendah, dead-letter, error sistem |

- Notifikasi dikirim asynchronous.
- Isi diredaksi dan dipotong.
- `TELEGRAM_SELFCHECK=true` memverifikasi `getChat` saat start dan mencatat `telegram chat ok` per kategori.
- Grup dengan Topics: satu bot dapat dipakai untuk semua kategori, dengan `chat_id` grup yang sama dan `TG_*_THREAD_ID` berbeda per topik. `message_thread_id` dikirim hanya bila nilainya > 0.
- Error yang sama di-log maksimal sekali per 5 menit per key.
- Jika `TG_SYSTEM_CHAT_ID` salah, alert sistem hilang meski transaksi tetap berjalan.

## Telegram admin bot

Bot admin memakai long polling di proses yang sama; tidak perlu membuka endpoint HTTP baru.

Aktifkan dengan:

```env
TG_ADMIN_BOT_TOKEN=123:token-dari-BotFather
TG_ADMIN_CHAT_IDS=123456789
```

- Gunakan bot **terpisah** dari bot notifikasi. Saat start bot memanggil `deleteWebhook`; jangan pasang webhook pada token ini.
- Hanya **private chat** yang menerima perintah. Chat grup, chat tak terdaftar, dan pesan dari bot lain diabaikan.
- Chat ID dapat dilihat lewat `@userinfobot` atau `getUpdates` saat setup.
- Bot membaca/menulis SQLite langsung, jadi tidak memakai `ADMIN_SECRET`; lindungi token dan chat ID seperti kredensial admin.
- Jalankan hanya **satu instance** bot per token. Offset update disimpan di `DATA_DIR/telegram-admin.offset` agar command tidak diulang setelah restart.
- Perintah yang mengubah data wajib diakhiri `confirm`.
- Saat start bot memasang **command menu** Telegram dan **menu button** bertipe `commands` untuk setiap private chat admin.
- `/start` atau `/menu` menampilkan menu utama: Status, Saldo, Clients, Webhooks, Transaksi, Bantuan.
- Tombol Clients membuka submenu: daftar client (10 per halaman, tombol Sebelumnya/Berikutnya), cek client, tambah, hapus, dan rotate secret.
- Tombol Webhooks membuka submenu: daftar webhook (10 per halaman), cek, tambah, ubah, dan hapus.
- Tombol Status, Saldo, dan Bantuan langsung menampilkan hasil (satu aksi), dengan tombol `🏠 Menu` untuk kembali.
- Tombol Transaksi membuka pencarian `refID`.
- Aksi yang butuh input akan mengirim prompt. Kirim nilai pada pesan berikutnya, atau `/batal` untuk membatalkan.
- Hapus client/webhook dan rotate secret memakai tombol konfirmasi inline.

Perintah:

| Perintah | Fungsi |
|---|---|
| `/start` / `/menu` | menu tombol inline |
| `/status` | versi, uptime, client/webhook, transaksi, delivery, report 24 jam, ukuran DB |
| `/balance` | cek saldo provider dan status low balance |
| `/clients [limit]` | daftar client aktif |
| `/client <client_id>` | detail client + webhook |
| `/client_add <name> \| <url\|-> \| [events]` | buat client; `-` berarti tanpa webhook |
| `/client_del <client_id> confirm` | nonaktifkan client dan webhook-nya |
| `/rotate <client_id> confirm` | rotasi callback secret; secret lama langsung invalid |
| `/webhooks [client_id]` | daftar webhook |
| `/webhook_add <client_id> <url> [events]` | tambah webhook |
| `/webhook_set <webhook_id> <url\|-> [events]` | ubah URL/events; `-` mempertahankan URL |
| `/webhook_del <webhook_id> confirm` | nonaktifkan webhook |
| `/trx <refID>` | detail transaksi dari ledger |

Events yang diterima: `invoice.paid`, `invoice.failed`, `invoice.pending`, atau wildcard `invoice.*` untuk semua event. Di bot, `all` atau `*` juga diterjemahkan menjadi `invoice.*`. Jika kolom events kosong, default-nya tiga event invoice tersebut.

Kredensial client hanya ditampilkan sekali pada respons `/client_add`. Jangan meneruskan pesan bot ke chat lain.

## Runbook

### Provider tidak dapat dihubungi

Gejala: API `502 CENTER_ERROR`, log `transaction failed; intent kept`.

1. Cek konektivitas dan status provider.
2. Jangan membuat `refID` baru untuk transaksi yang sama.
3. Setelah provider pulih, rekonsiliasi transaksi `pending`/stale.
4. Jika provider memastikan tidak menerima, selesaikan manual melalui ledger.

### Report tidak mengubah transaksi

1. Cek `provider_reports` untuk `reason`.
2. `unknown_ref` → cek `refID`/backfill log.
3. `dest_mismatch` → bandingkan nomor provider vs ledger.
4. `duplicate`/`conflict` → status sudah final; konflik perlu rekonsiliasi.
5. Pastikan `REPORT_ALLOW_IPS`/token benar.

### Delivery dead

1. Lihat `deliveries.last_error` dan target.
2. Uji endpoint client secara manual.
3. Pastikan URL https, DNS, sertifikat, firewall.
4. Setelah client pulih, event dead tidak dikirim ulang otomatis; buat replay manual bila diperlukan.

### Database terkunci

Gejala: log SQLite `database is locked`, request 500.

1. Pastikan hanya satu instance berjalan.
2. Cek proses yang memegang file DB.
3. Jangan menjalankan `VACUUM` interaktif saat load tinggi.
4. Restart service bila lock persisten.

### Disk penuh

1. Cek `journalctl --disk-usage`.
2. `journalctl --vacuum-size=500M`.
3. Cek ukuran DB dan backup.
4. Pastikan rotasi backup berjalan.
5. Naikkan kapasitas bila pertumbuhan wajar.

### Telegram chat not found

1. Jalankan self-check/log.
2. Pastikan bot sudah di chat dan chat ID benar.
3. Perbarui environment dan restart.
4. Kirim pesan uji ke bot.

### Stale intent menumpuk

1. Lihat `GET /admin/v1/stats`.
2. Query transaksi pending:
   ```sql
   SELECT ref_id, product, dest, created_at FROM transactions
   WHERE category='pending' AND COALESCE(provider_state,'')=''
   ORDER BY created_at LIMIT 50;
   ```
3. Cek log request provider.
4. Rekonsiliasi dengan provider; jangan mengulang `refID` yang sama sebelum jelas.

## Kapasitas

- SQLite + satu dispatcher cocok untuk volume kecil/menengah satu VPS.
- Bottleneck utama: latensi provider dan callback, bukan CPU.
- Delivery diproses berurutan; endpoint yang lambat memperlambat antrean.
- Untuk multi-node, migrasikan ke Postgres dan dispatcher terdistribusi.

## Perintah harian

```bash
systemctl status auvyn-apies
journalctl -u auvyn-apies -f
curl -s http://127.0.0.1:5882/readyz
curl -s http://127.0.0.1:5882/version
sqlite3 /opt/auvyn-apies/data/auvyn.db "PRAGMA integrity_check;"
```

## Dokumen terkait

- Deployment: [14 — Deployment](14-deployment.md)
- Database: [11 — Database](11-database.md)
- Troubleshooting: [17 — Troubleshooting](17-troubleshooting.md)
