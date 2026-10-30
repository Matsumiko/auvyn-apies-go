# 14 — Deployment

## Ringkasan

- Satu binary Go, tanpa runtime Node.
- Satu instance per database.
- Log ke stdout/journald.
- Reverse proxy menangani TLS dan (idealnya) allowlist `/report`.
- systemd menjalankan proses sebagai user non-root.

## Build

```bash
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o auvyn ./cmd/auvyn
```

Verifikasi:

```bash
./auvyn version
go test ./...
```

## User dan direktori

```bash
useradd --system --home /opt/auvyn-apies --shell /usr/sbin/nologin auvyn || true
install -d -o auvyn -g auvyn /opt/auvyn-apies /opt/auvyn-apies/data
install -m 0755 auvyn /opt/auvyn-apies/auvyn
install -m 0600 -o auvyn -g auvyn .env /opt/auvyn-apies/.env
```

Jangan menjalankan importer sebagai root jika service berjalan sebagai `auvyn`; file DB harus dimiliki user service:

```bash
sudo -u auvyn env DB_PATH=/opt/auvyn-apies/data/auvyn.db \
  /opt/auvyn-apies/auvyn import-json /path/to/legacy/db.json
```

## Environment

Service **tidak** membaca `.env` otomatis. systemd memakai `EnvironmentFile=`.

Minimal:

```env
HOST=127.0.0.1
PORT=5882
CENTER_URL=http://203.0.113.10:6969
MEMBER_ID=AG000001
PIN=0000
PASSWORD=change-me
SIGN_PREFIX=OtomaX
AUVYN_SECRET=change-me-long-random
ADMIN_SECRET=...
ADMIN_PIN=...
ADMIN_KEYS=...
DATA_DIR=/opt/auvyn-apies/data
REPORT_ALLOW_IPS=203.0.113.10/32
```

Referensi lengkap: [04 — Konfigurasi](04-configuration.md).

### HOST dan akses provider

- Default `HOST=127.0.0.1`: aman untuk app di belakang reverse proxy.
- Jika provider memanggil `IP_VPS:PORT` secara langsung (bukan domain), app harus listen `0.0.0.0` **dan** firewall membatasi port hanya untuk IP provider.
- Jangan membuka port app ke publik tanpa allowlist.

## systemd

Contoh unit tersedia di `deploy/auvyn-apies.service`:

```ini
[Unit]
Description=Auvyn Apies - IP Center bridge
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=auvyn
Group=auvyn
WorkingDirectory=/opt/auvyn-apies
EnvironmentFile=/opt/auvyn-apies/.env
ExecStart=/opt/auvyn-apies/auvyn
Restart=on-failure
RestartSec=3
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/opt/auvyn-apies/data
LimitNOFILE=4096

[Install]
WantedBy=multi-user.target
```

Pasang:

```bash
install -m 0644 deploy/auvyn-apies.service /etc/systemd/system/
systemctl daemon-reload
systemctl enable --now auvyn-apies
systemctl status auvyn-apies
journalctl -u auvyn-apies -f
```

Jika backup ditulis ke direktori di luar `data/`, jalankan backup sebagai root/cron, bukan dari service.

## Reverse proxy dan TLS

Contoh vhost:

```nginx
server {
    listen 443 ssl;
    http2 on;
    server_name api.example.com;

    ssl_certificate     /etc/ssl/example.com.pem;
    ssl_certificate_key /etc/ssl/example.com.key;

    client_max_body_size 1m;

    location = /report {
        allow 203.0.113.10;
        deny all;

        proxy_pass http://127.0.0.1:5882;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }

    location / {
        proxy_pass http://127.0.0.1:5882;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

Poin penting:

- `location = /report` harus ada di **setiap** vhost yang mem-proxy service.
- `TRUSTED_PROXY_CIDRS` harus mencakup alamat proxy (`127.0.0.1/32` default).
- Jangan menaruh `/report` di belakang CDN yang mengubah sumber IP tanpa konfigurasi IP asli yang benar.
- Batasi body `/report` dan tambahkan rate limit bila perlu.

## Firewall

Contoh UFW:

```bash
ufw default deny incoming
ufw allow 22/tcp
ufw allow 443/tcp
# Jika provider memanggil port langsung:
ufw allow from 203.0.113.10 to any port 5882 proto tcp
```

## Journald

```bash
install -d /etc/systemd/journald.conf.d
install -m 0644 deploy/journald.conf.d/auvyn-apies.conf /etc/systemd/journald.conf.d/
systemctl restart systemd-journald
journalctl --disk-usage
journalctl --vacuum-size=500M
```

## Backup

Cron harian:

```cron
15 2 * * * DB_PATH=/opt/auvyn-apies/data/auvyn.db BACKUP_DIR=/opt/auvyn-apies/backup KEEP_DAYS=14 /opt/auvyn-apies/deploy/backup.sh >> /var/log/auvyn-backup.log 2>&1
```

Pastikan direktori backup memiliki izin ketat dan tidak berada di filesystem yang sama jika memungkinkan.

## Upgrade

1. Build binary baru.
2. Backup DB.
3. Ganti binary secara atomik:
   ```bash
   install -m 0755 auvyn.new /opt/auvyn-apies/auvyn
   ```
4. `systemctl restart auvyn-apies`.
5. Cek `/readyz`, `/version`, dan log startup.
6. Cek `/admin/v1/stats` untuk stale intent/dead delivery.

Skema saat ini idempotent (`CREATE TABLE IF NOT EXISTS`). Perubahan kolom memerlukan migrasi eksplisit; lihat [15 — Pengembangan](15-development.md).

## Rollback

1. Hentikan service.
2. Kembalikan binary versi sebelumnya.
3. Jika migrasi data tidak kompatibel, restore backup DB.
4. Start service dan verifikasi.

Simpan binary versi sebelumnya dengan tanggal/versi.

## Cutover dari Node

Ringkas:

1. Pastikan guard `/report` di nginx sudah aktif.
2. Siapkan DB baru dan impor client/webhook/transaksi.
3. Jalankan Go di port terpisah untuk shadow test.
4. Switch port/service; jangan menjalankan Node dan Go bersamaan pada port yang sama.
5. Verifikasi report provider masuk dan callback keluar.
6. Simpan Node untuk rollback sampai yakin.

Detail: [16 — Migrasi](16-migration.md).

## Checklist produksi

- [ ] `.env` mode 0600, owner service.
- [ ] `REPORT_ALLOW_IPS` terisi IP provider yang diverifikasi.
- [ ] nginx `location = /report` allowlist.
- [ ] Port app dibatasi firewall.
- [ ] `HOST` sesuai pola akses provider.
- [ ] journald `SystemMaxUse` diatur.
- [ ] backup harian + uji restore.
- [ ] `TELEGRAM_SELFCHECK` dan keempat chat valid.
- [ ] `ALLOW_INSECURE_*`/`ALLOW_PRIVATE_WEBHOOKS` false.
- [ ] healthcheck `/readyz` dipantau.
- [ ] statistik stale/dead dipantau.

## Batasan deployment

- Jangan menjalankan dua instance pada DB yang sama.
- SQLite bukan solusi multi-region.
- Dispatcher outbox berjalan di proses yang sama; scaling horizontal memerlukan arsitektur berbeda.

## Dokumen terkait

- Operasional: [13 — Operasional](13-operations.md)
- Keamanan: [12 — Keamanan](12-security.md)
- Migrasi: [16 — Migrasi](16-migration.md)
