# 03 — Mulai cepat

## Prasyarat

- Go 1.22 atau lebih baru
- SQLite CLI (opsional, untuk inspeksi/backup)
- Akses ke IP Center dan kredensial agen
- Linux untuk deployment; macOS/Linux untuk development

## Build

```bash
git clone <repo-url> auvyn-apies-go
cd auvyn-apies-go
go build -o auvyn ./cmd/auvyn
go test ./...
```

Binary statis:

```bash
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o auvyn ./cmd/auvyn
```

## Konfigurasi minimum

Service **tidak membaca `.env` otomatis**. Isi environment lewat shell, systemd `EnvironmentFile`, atau container.

```bash
export HOST=127.0.0.1
export PORT=5882
export CENTER_URL=http://203.0.113.10:6969
export MEMBER_ID=AG000001
export PIN=0000
export PASSWORD=change-me
export SIGN_PREFIX=ENGINE
export AUVYN_SECRET=change-me-to-a-long-random-string
export ADMIN_SECRET=admin-secret
export ADMIN_PIN=admin-pin
export ADMIN_KEYS=admin-keys

# /report wajib punya guard
export REPORT_ALLOW_IPS=203.0.113.10/32
```

Lihat referensi lengkap di [04 — Konfigurasi](04-configuration.md).

## Jalankan

```bash
./auvyn
```

Log JSON muncul di stdout. Cek health:

```bash
curl -s http://127.0.0.1:5882/healthz
curl -s http://127.0.0.1:5882/version
curl -s http://127.0.0.1:5882/readyz
```

## Request pertama

### Ping

```bash
curl -sS -X POST http://127.0.0.1:5882/api/ping \
  -H "x-auvyn-secret: $AUVYN_SECRET"
```

### Transaksi

Transaksi wajib memakai kredensial client. Dapatkan `CLIENT_SECRET`, `CLIENT_PIN`, dan `CLIENT_KEYS` saat membuat client lewat admin/bot:

```bash
curl -sS -X POST http://127.0.0.1:5882/api/transaction \
  -H "content-type: application/json" \
  -H "Authorization: Bearer $CLIENT_SECRET" \
  -H "X-Client-Pin: $CLIENT_PIN" \
  -H "X-Client-Keys: $CLIENT_KEYS" \
  -d '{
    "product": "PULSA10",
    "dest": "081234567890",
    "qty": 1,
    "refID": "AVN-DEMO-1",
    "meta": {"orderId": "INV-1"}
  }'
```

`meta.client_id` tidak perlu diisi; server mengambilnya dari client yang terautentikasi.

### Cek saldo

```bash
curl -sS -X POST http://127.0.0.1:5882/api/balance \
  -H "content-type: application/json" \
  -H "x-auvyn-secret: $AUVYN_SECRET" \
  -d '{}'
```

### Health dan statistik

```bash
curl -sS http://127.0.0.1:5882/healthz
curl -sS -X POST http://127.0.0.1:5882/api/ping -H "x-auvyn-secret: $AUVYN_SECRET"

curl -sS http://127.0.0.1:5882/admin/v1/stats \
  -H "Authorization: Bearer $ADMIN_SECRET" \
  -H "x-admin-pin: $ADMIN_PIN" \
  -H "x-admin-keys: $ADMIN_KEYS"
```

## Menambahkan client dan webhook

```bash
curl -sS -X POST http://127.0.0.1:5882/admin/v1/clients \
  -H "Authorization: Bearer $ADMIN_SECRET" \
  -H "x-admin-pin: $ADMIN_PIN" \
  -H "x-admin-keys: $ADMIN_KEYS" \
  -H "content-type: application/json" \
  -d '{
    "client_name": "acme",
    "webhook": {
      "url": "https://example.com/cb",
      "events": ["invoice.paid", "invoice.failed", "invoice.pending"]
    }
  }'
```

Simpan `credentials.client_secret`, `client_pin`, `client_keys`, dan `callback_secret` dari respons. Nilai ini hanya ditampilkan sekali.

## Menjalankan dengan provider palsu

Untuk development tanpa provider nyata, arahkan `CENTER_URL` ke server lokal yang mengembalikan pesan Otomax. Contoh minimal dengan Python:

```python
from http.server import BaseHTTPRequestHandler, HTTPServer

class H(BaseHTTPRequestHandler):
    def do_GET(self):
        body = b"#12345.081234567890 SUKSES. SN/Ref: SN-DEMO. Saldo 100.000 - 10.000 = 90.000"
        self.send_response(200)
        self.send_header("content-type", "text/plain")
        self.send_header("content-length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

HTTPServer(("127.0.0.1", 6969), H).serve_forever()
```

Set `CENTER_URL=http://127.0.0.1:6969`, lalu kirim transaksi. Service akan memanggil `/trx` dan memproses respons.

## Struktur direktori runtime

```text
data/
  auvyn.db
  auvyn.db-wal
  auvyn.db-shm
backup/
  auvyn-YYYY-MM-DD-HHMM.db
```

`data/` tidak boleh masuk version control.

## Langkah berikutnya

- Semua konfigurasi: [04 — Konfigurasi](04-configuration.md)
- API lengkap: [05 — API publik](05-api-public.md)
- Deploy produksi: [14 — Deployment](14-deployment.md)
- Migrasi dari Node: [16 — Migrasi](16-migration.md)
