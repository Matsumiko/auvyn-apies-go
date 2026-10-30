# 16 — Migrasi

Dokumen ini menjelaskan migrasi dari implementasi Node lama (`data/db.json` dan log JSONL) ke service Go.

## Yang dimigrasikan

| Sumber | Perintah | Isi |
|---|---|---|
| `data/db.json` | `import-json` | client, hash kredensial, `secret_callback`, webhook, event subscription |
| `logs/trx-pending.log` | `import-logs` | transaksi yang pernah dibuat, termasuk yang belum final |
| `logs/trx-success.log` | `import-logs` | transaksi sukses + baris report sukses |
| `logs/trx-failed.log` | `import-logs` | transaksi gagal + baris report gagal |

Tidak dimigrasikan:

- riwayat `provider_reports` dari `report.log` (hanya audit; bisa dipertahankan di file lama);
- delivery/event callback lama (outbox baru mulai dari cutover);
- konfigurasi environment (disiapkan manual);
- binary/node_modules lama.

## Prasyarat

1. Backup `data/db.json` dan seluruh log.
2. Siapkan DB tujuan kosong.
3. Pastikan service Go berhenti saat impor (kecuali hanya membaca file legacy).
4. File DB harus dimiliki user service.

## Impor client dan webhook

```bash
sudo -u auvyn env DB_PATH=/opt/auvyn-apies/data/auvyn.db \
  /opt/auvyn-apies/auvyn import-json /path/to/legacy/data/db.json
```

Hasil:

```json
{"clients":2,"webhooks":2,"skipped":0}
```

Sifat:

- idempotent (`ON CONFLICT client_id/webhook_id DO NOTHING`);
- hash `secret_hash`, `pin_hash`, `keys_hash` kompatibel karena sama-sama SHA-256 base64;
- `secret_callback` disalin plaintext;
- webhook tanpa client valid dilewati;
- event subscription disalin.

Rotasi `secret_callback` setelah cutover bila nilai lama pernah terekspos.

## Impor transaksi dari log

```bash
sudo -u auvyn env DB_PATH=/opt/auvyn-apies/data/auvyn.db \
  /opt/auvyn-apies/auvyn import-logs /path/to/legacy/logs
```

Hasil:

```json
{"inserted":375,"merged":305,"skipped":3}
```

Cara kerja:

1. File diproses berurutan: `trx-pending.log`, `trx-success.log`, `trx-failed.log`.
2. Baris tanpa `refID`, `product`, atau `dest` dilewati (misalnya baris report dengan refID asing).
3. Baris request membuat transaksi.
4. Baris report dengan `refID` sama melakukan merge.
5. Status final menang atas pending.
6. Dua status final berbeda tidak saling menimpa.
7. `request_raw` lama dipertahankan; report tidak menimpa raw request.
8. `raw` diparse ulang dengan pola lapangan untuk mengisi `trxid` dan `SN`.
9. Impor idempotent; jalankan ulang aman.

Contoh query verifikasi:

```sql
SELECT category, COUNT(*) FROM transactions GROUP BY category;
SELECT COUNT(*), COUNT(DISTINCT ref_id), SUM(final_at IS NOT NULL) FROM transactions;
SELECT provider_state, provider_code, COUNT(*) FROM transactions GROUP BY 1,2;
SELECT ref_id, product, dest, category, provider_code, final_at
FROM transactions ORDER BY created_at DESC LIMIT 10;
```

Yang diharapkan:

- setiap `ref_id` unik;
- kategori hanya `pending`/`success`/`failed`;
- transaksi final memiliki `final_at`;
- transaksi historis memiliki `provider_code` yang bermakna (bukan `UNKNOWN`) bila formatnya dikenali.

## Verifikasi Data

```bash
sqlite3 /opt/auvyn-apies/data/auvyn.db <<'SQL'
SELECT COUNT(*) AS clients FROM clients WHERE status='ACTIVE';
SELECT COUNT(*) AS webhooks FROM webhooks WHERE status='ACTIVE';
SELECT category, COUNT(*) FROM transactions GROUP BY category;
SELECT status, COUNT(*) FROM deliveries GROUP BY status;
PRAGMA integrity_check;
SQL
```

Bandingkan jumlah client/webhook dengan `db.json` lama.

## Cutover

### 1. Shadow test

- Jalankan Go di port berbeda (misalnya 5883) dengan **salinan** DB.
- Set `REPORT_ALLOW_IPS`/`REPORT_TOKEN` sesuai.
- Replay report historis dari log ke endpoint shadow.
- Bandingkan kategori/callback dengan ekspektasi.
- Jangan arahkan provider ke shadow.

### 2. Siapkan produksi

- Pasang binary, systemd, `.env`.
- Pasang `location = /report` allowlist di semua vhost.
- Set `HOST`:
  - `127.0.0.1` jika hanya lewat reverse proxy;
  - `0.0.0.0` jika provider memanggil `IP:PORT` langsung, dengan firewall allowlist.
- Backup DB.

### 3. Switch

Urutan yang disarankan:

1. Hentikan service Node/PM2.
2. Jalankan service Go pada port yang sama.
3. Pastikan `/healthz` dan `/readyz` OK.
4. Kirim satu transaksi uji.
5. Tunggu report provider dan verifikasi callback.
6. Cek `/admin/v1/stats` (stale, dead, rejected).
7. Pantau log minimal satu siklus transaksi.

Jangan menjalankan Node dan Go bersamaan pada port yang sama atau pada DB yang sama.

### 4. Verifikasi pasca-cutover

- Report masuk dari IP yang diizinkan.
- `provider_reports` bertambah dengan reason `applied`.
- `transactions` berubah ke final.
- Callback legacy/managed diterima dan HMAC valid.
- Tidak ada `UNKNOWN` baru pada format provider yang sudah dikenal.
- Telegram sistem valid.
- Tidak ada `dead` delivery baru.

### 5. Rollback

- Hentikan Go.
- Jalankan Node lama dengan konfigurasi lama.
- Kembalikan DB lama bila perlu.
- Catat report/transaksi yang terjadi selama jendela cutover untuk rekonsiliasi.

## Setelah cutover

- Rotasi `secret_callback` client yang nilainya pernah terekspos.
- Hapus client uji lama.
- Set `RETENTION_DAYS` sesuai kebijakan audit (default 365).
- Simpan log lama minimal sampai rekonsiliasi selesai.
- Jadwalkan backup dan uji restore.
- Hapus akses/berkas Node lama setelah masa rollback berakhir.

## Jebakan umum

| Masalah | Penyebab | Solusi |
|---|---|---|
| Report historis ditolak `unknown_ref` | `import-logs` belum dijalankan | impor log, lalu ulangi report |
| Client lama tidak bisa auth | hash tidak cocok | pastikan `db.json` yang benar; hash harus SHA-256 base64 |
| Webhook lama tidak terkirim | URL http/private diblokir | set opt-in khusus atau ubah URL |
| Transaksi final tertimpa | impor dua final berbeda | importer mempertahankan final pertama; cek konflik |
| DB tidak bisa ditulis service | impor dijalankan sebagai root | `chown auvyn:auvyn` atau impor sebagai user service |
| Report provider putus | Go bind loopback padahal provider akses langsung | set `HOST=0.0.0.0` + firewall |
| `/report` publik | nginx belum diberi location guard | pasang guard |

## Dokumen terkait

- Deployment: [14 — Deployment](14-deployment.md)
- Database: [11 — Database](11-database.md)
- Troubleshooting: [17 — Troubleshooting](17-troubleshooting.md)
