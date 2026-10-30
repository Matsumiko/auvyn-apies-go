# 04 — Konfigurasi

Service membaca konfigurasi dari environment. File `.env` **tidak** dibaca otomatis; gunakan `EnvironmentFile=` pada systemd, `docker --env-file`, atau `export`.

Nilai kredensial dan secret dibaca apa adanya (whitespace dipertahankan). Nilai lain di-trim.

## Referensi environment variable

### Service

| Variable | Default | Wajib | Keterangan |
|---|---|---|---|
| `HOST` | `127.0.0.1` | tidak | Alamat bind. Gunakan `0.0.0.0` hanya jika provider memanggil port secara langsung dan firewall membatasi. |
| `PORT` | `5882` | tidak | Port HTTP, 1–65535. |
| `LOG_LEVEL` | `info` | tidak | `debug`, `info`, `warn`, `error`. |
| `ACCESS_LOG` | `true` | tidak | Log satu baris per request (path tanpa query). |

### Provider

| Variable | Default | Wajib | Keterangan |
|---|---|---|---|
| `CENTER_URL` | — | **ya** | Base URL IP Center, tanpa trailing slash. |
| `MEMBER_ID` | — | **ya** | ID agen di provider. |
| `PIN` | — | **ya** | PIN agen. |
| `PASSWORD` | — | **ya** | Password provider. |
| `SIGN_PREFIX` | `ENGINE` | tidak | Prefix signature, case-sensitive. Ikuti provider. |
| `SIGN_INCLUDE_NOMINAL` | `false` | tidak | Tambahkan nominal ke template signature untuk transaksi non-bebas. |
| `EWALLET_BEBAS_PRODUCTS` | kosong | tidak | Daftar kode produk nominal-bebas, dipisah koma; pencocokan case-insensitive. |
| `EWALLET_BEBAS_QTY_IS_NOMINAL` | `true` | tidak | Jika true, `qty = nominal`; jika false, `qty = floor(nominal/unit)`. |
| `EWALLET_BEBAS_UNIT` | `1000` | tidak | Unit pembagi qty, minimal 1. |

### Autentikasi API

| Variable | Default | Wajib | Keterangan |
|---|---|---|---|
| `AUVYN_SECRET` | — | **ya** | Secret operasional untuk `/api/ping`, `/api/balance`, `/api/ticket-mbal`. |
| `ADMIN_SECRET` | kosong | tidak | Secret admin. Endpoint admin mengembalikan 500 bila kosong. |
| `ADMIN_PIN` | kosong | tidak | Faktor kedua admin. |
| `ADMIN_KEYS` | kosong | tidak | Faktor ketiga admin. |

Admin baru juga menerima `Authorization: Bearer <ADMIN_SECRET>`; PIN dan KEYS tetap header terpisah.

### Storage

| Variable | Default | Wajib | Keterangan |
|---|---|---|---|
| `DATA_DIR` | `./data` | tidak | Direktori data. |
| `DB_PATH` | `$DATA_DIR/auvyn.db` | tidak | Path SQLite. |
| `RETENTION_DAYS` | `365` | tidak | Umur outbox selesai dan audit report sebelum dipangkas. `0` = nonaktif. Transaksi tidak pernah dipangkas. |

### Callback legacy

| Variable | Default | Wajib | Keterangan |
|---|---|---|---|
| `CALLBACK_URL` | kosong | tidak | Satu URL callback legacy. |
| `CALLBACK_SECRET` | `AUVYN_SECRET` | tidak | Secret untuk callback legacy. |
| `CALLBACK_TARGETS` | kosong | tidak | JSON array `[{"name","url","secret"}]`. |
| `CALLBACK_WEB_URL` / `CALLBACK_WEB_SECRET` | kosong | tidak | Target tambahan bernama `web-worker`. |
| `CALLBACK_BOT_URL` / `CALLBACK_BOT_SECRET` | kosong | tidak | Target tambahan bernama `bot-worker`. |
| `CALLBACK_THIRD_URL` / `CALLBACK_THIRD_SECRET` | kosong | tidak | Target tambahan bernama `third-worker`. |
| `MANAGED_WEBHOOKS_ENABLED` | `true` | tidak | Aktifkan webhook per client. |
| `CALLBACK_TIMEOUT_MS` | `15000` | tidak | Timeout per pengiriman callback. |
| `DELIVERY_MAX_ATTEMPTS` | `6` | tidak | Jumlah percobaan sebelum dead-letter. |
| `WEBHOOK_BACKOFF` | `1m,5m,30m,2h,6h,24h` | tidak | Daftar durasi backoff; dipakai berurutan. |
| `ALLOW_INSECURE_WEBHOOKS` | `false` | tidak | Izinkan URL webhook `http://`. |
| `ALLOW_PRIVATE_WEBHOOKS` | `false` | tidak | Izinkan webhook ke IP privat/loopback/link-local. |

### Report inbound

| Variable | Default | Wajib | Keterangan |
|---|---|---|---|
| `REPORT_ALLOW_IPS` | kosong | salah satu guard | Daftar IP/CIDR provider, dipisah koma. |
| `REPORT_TOKEN` | kosong | salah satu guard | Token opsional; diterima lewat `?key=`, `?token=`, atau `X-Report-Token`. |
| `ALLOW_INSECURE_REPORT` | `false` | tidak | Izinkan start tanpa guard. Hanya untuk development. |
| `TRUSTED_PROXY_CIDRS` | `127.0.0.1/32,::1/128` | tidak | Peer yang boleh menentukan IP lewat `X-Real-IP`/`X-Forwarded-For`. |

**Validasi:** service gagal start bila `REPORT_ALLOW_IPS` dan `REPORT_TOKEN` sama-sama kosong dan `ALLOW_INSECURE_REPORT=false`.

### Limit dan notifikasi

| Variable | Default | Wajib | Keterangan |
|---|---|---|---|
| `BALANCE_LOW_LIMIT` | `50000` | tidak | Alert saldo rendah; `0` menonaktifkan. |
| `TG_SUCCESS_BOT_TOKEN` / `TG_SUCCESS_CHAT_ID` | kosong | tidak | Bot transaksi sukses. |
| `TG_PENDING_BOT_TOKEN` / `TG_PENDING_CHAT_ID` | kosong | tidak | Bot transaksi pending. |
| `TG_FAILED_BOT_TOKEN` / `TG_FAILED_CHAT_ID` | kosong | tidak | Bot transaksi gagal. |
| `TG_SYSTEM_BOT_TOKEN` / `TG_SYSTEM_CHAT_ID` | kosong | tidak | Alert sistem, report, dead-letter. |
| `TG_SUCCESS_THREAD_ID` | `0` | tidak | Topic ID untuk grup forum. |
| `TG_PENDING_THREAD_ID` | `0` | tidak | Topic ID untuk grup forum. |
| `TG_FAILED_THREAD_ID` | `0` | tidak | Topic ID untuk grup forum. |
| `TG_SYSTEM_THREAD_ID` | `0` | tidak | Topic ID untuk grup forum. |
| `TG_BALANCE_THREAD_ID` | `0` | tidak | Topic khusus alert saldo; fallback ke `TG_SYSTEM_THREAD_ID`. |
| `TELEGRAM_SELFCHECK` | `true` | tidak | Verifikasi token/chat saat start. |
| `TG_ADMIN_BOT_TOKEN` | kosong | tidak | Token bot admin; kosong = bot admin nonaktif. Gunakan bot terpisah dari bot notifikasi. |
| `TG_ADMIN_CHAT_IDS` | kosong | ya bila token diisi | Daftar private chat ID admin, pisahkan koma. Grup ditolak. |
| `TG_ADMIN_API_BASE` | `https://api.telegram.org` | tidak | Basis API; ubah hanya untuk fake API/development. |

Jika token atau chat ID kosong, kategori notifikasi tersebut dilewati. Untuk bot admin: token dan minimal satu chat ID harus diisi bersamaan; kombinasi sebagian membuat startup gagal.

## Validasi startup

Service menolak start bila:

- `CENTER_URL`, `MEMBER_ID`, `PIN`, `PASSWORD`, atau `AUVYN_SECRET` kosong;
- `PORT` di luar 1–65535;
- `EWALLET_BEBAS_UNIT < 1`;
- `DELIVERY_MAX_ATTEMPTS < 1`;
- `CALLBACK_TIMEOUT_MS <= 0`;
- `RETENTION_DAYS < 0`;
- `REPORT_ALLOW_IPS`/`REPORT_TOKEN` kosong tanpa `ALLOW_INSECURE_REPORT=true`;
- `CALLBACK_TARGETS` bukan JSON valid;
- `REPORT_ALLOW_IPS`, `TRUSTED_PROXY_CIDRS`, atau `WEBHOOK_BACKOFF` tidak dapat diparse.

Kredensial admin tidak wajib untuk start; endpoint admin akan mengembalikan `500 SERVER_MISCONFIGURED` bila belum diisi.

## Contoh environment

```env
HOST=127.0.0.1
PORT=5882

CENTER_URL=http://203.0.113.10:6969
MEMBER_ID=AG000001
PIN=0000
PASSWORD=change-me
SIGN_PREFIX=ENGINE

AUVYN_SECRET=change-me-to-a-long-random-string
ADMIN_SECRET=admin-secret
ADMIN_PIN=admin-pin
ADMIN_KEYS=admin-keys

DATA_DIR=./data
DB_PATH=./data/auvyn.db
RETENTION_DAYS=365

MANAGED_WEBHOOKS_ENABLED=true
CALLBACK_TIMEOUT_MS=15000
DELIVERY_MAX_ATTEMPTS=6
WEBHOOK_BACKOFF=1m,5m,30m,2h,6h,24h

REPORT_ALLOW_IPS=203.0.113.10/32
REPORT_TOKEN=
ALLOW_INSECURE_REPORT=false
TRUSTED_PROXY_CIDRS=127.0.0.1/32,::1/128

BALANCE_LOW_LIMIT=50000
EWALLET_BEBAS_PRODUCTS=DANABEBAS,OVTBEBAS
EWALLET_BEBAS_QTY_IS_NOMINAL=true
EWALLET_BEBAS_UNIT=1000
SIGN_INCLUDE_NOMINAL=false

ALLOW_INSECURE_WEBHOOKS=false
ALLOW_PRIVATE_WEBHOOKS=false

ACCESS_LOG=true
LOG_LEVEL=info
TELEGRAM_SELFCHECK=true
```

## systemd

```ini
[Service]
EnvironmentFile=/opt/auvyn-apies/.env
ExecStart=/opt/auvyn-apies/auvyn
```

File `.env` sebaiknya mode `0600`, owner user service.

## Docker

Binary dapat dijalankan dalam image minimal. Pastikan:

- volume untuk `DATA_DIR`;
- environment diteruskan;
- satu container saja per database;
- healthcheck memanggil `/healthz`;
- stdout dikumpulkan oleh driver log dengan rotasi.

## Dokumen terkait

- Deployment lengkap: [14 — Deployment](14-deployment.md)
- Keamanan rahasia: [12 — Keamanan](12-security.md)
