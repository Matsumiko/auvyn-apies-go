# 15 — Pengembangan

## Struktur repository

```text
cmd/auvyn/              entry point, subcommand, wiring, maintenance
internal/config/        environment + validasi
internal/logging/       slog + URL aman
internal/security/      token/id/hash/constant-time
internal/ipcenter/      signature, URL, parser, sanitizer, HTTP client
internal/store/         SQLite, skema, outbox, stats, prune, importer
internal/httpapi/       router, middleware, handler, envelope
internal/webhook/       validasi URL, dispatcher, HMAC
internal/notify/        Telegram
internal/version/       versi
deploy/                 systemd, nginx, journald, backup
docs/                   dokumentasi
```

## Prinsip kode

- Protokol provider beku; perubahan memerlukan capture/konfirmasi dan test vector.
- Jangan menambah dependensi untuk hal yang bisa diselesaikan standard library.
- Handler tipis; logika bisnis di store/ipcenter/webhook.
- Semua penulisan state bisnis harus atomik atau idempoten.
- Jangan pernah mencatat kredensial, query provider, SN, atau token.
- Error yang dikembalikan ke klien harus generik; detail internal hanya di log.
- API admin/client baru mengikuti resource-oriented + envelope `data/error`.
- API publik lama dipertahankan sampai ada versi baru.

## Menjalankan test

```bash
go test ./...
go test -race ./...
go vet ./...
```

Test penting:

| Paket | Isi |
|---|---|
| `internal/ipcenter` | signature vector, URL exact, parser semua pola, redaksi |
| `internal/store` | transisi status, dedupe, prune, import, webhook |
| `internal/httpapi` | API publik, report, admin/client v1, legacy alias, auth |
| `internal/webhook` | HMAC, retry, dead-letter, validasi URL |
| `internal/notify` | truncation, error URL tanpa token |
| `internal/config` | validasi guard/retensi |

CI menjalankan `go vet`, `go test -race`, dan build statis.

## Menambah pola parser

1. Tambahkan pola pada urutan yang benar di `internal/ipcenter/parse.go`.
2. Jangan mengubah pola lama; tambahkan pola baru sebelum fallback.
3. Tambahkan test di `internal/ipcenter/parse_test.go` dengan data sintetis (bukan data pelanggan).
4. Jika pola menangkap field baru, tambahkan ke `ProviderResult`.
5. Jika format memuat data sensitif baru, tambahkan redaksi di `sanitize.go` dan test.
6. Update `CONTRACT.md` dan `docs/09-provider-protocol.md`.
7. Jalankan seluruh test dan smoke.

Urutan pola menentukan hasil. Pola yang terlalu umum dapat menelan pola lain; selalu uji contoh positif dan negatif.

## Menambah endpoint

- API publik legacy: tambahkan route di `internal/httpapi/server.go` dan handler dengan envelope lama.
- Admin/client v1: tambahkan ke `adminV1`/`clientV1`, gunakan `writeData`/`writeAPIError`, status HTTP yang benar, path resource-oriented.
- Validasi input sebelum menyentuh store.
- Jangan menaruh rahasia di response list/detail.
- Tambahkan test auth, sukses, validasi, dan not-found.
- Update dokumentasi API terkait.

## Perubahan database

Skema ada di `internal/store/schema.sql` dan dijalankan dengan `CREATE TABLE IF NOT EXISTS`. Untuk perubahan pada database yang sudah ada:

1. Tambahkan migrasi eksplisit (ALTER TABLE) dengan pengecekan versi/kolom.
2. Uji pada salinan DB produksi.
3. Backup sebelum upgrade.
4. Dokumentasikan rollback.

Jangan mengandalkan `CREATE TABLE IF NOT EXISTS` untuk menambah kolom.

## Redaksi

Setiap data yang keluar dari proses (callback, API response, log, Telegram) harus melewati aturan redaksi:

- SN/Ref, SN (khusus log/Telegram; payload client mempertahankan SN);
- saldo (`Saldo`, `Sal`, `M-Bal`);
- `HRG`;
- `sign`;
- `memberID`;
- URL `/trx`;
- token report di query audit.

Tambahkan test untuk setiap pola sensitif baru.

## Debug lokal

- Gunakan provider palsu yang mengembalikan teks Otomax.
- Set `ALLOW_INSECURE_REPORT=true` hanya untuk lokal.
- Gunakan `LOG_LEVEL=debug` bila perlu, tetapi jangan log payload mentah.
- Untuk webhook lokal: `ALLOW_INSECURE_WEBHOOKS=true`, `ALLOW_PRIVATE_WEBHOOKS=true`, URL `http://127.0.0.1:PORT/cb`.
- Periksa DB dengan `sqlite3`.

Contoh provider palsu ada di [03 — Mulai cepat](03-getting-started.md).

## Versioning

- Versi ada di `internal/version/version.go`.
- Naikkan versi saat rilis.
- `/version` dan log startup menampilkan versi.
- Perubahan yang memutus kompatibilitas API publik harus didokumentasikan dan diberi versi baru.

## Review checklist

- [ ] Tidak ada rahasia di log/response/test.
- [ ] Input divalidasi dan dibatasi.
- [ ] Penulisan state atomik/idempoten.
- [ ] Error tidak membocorkan URL provider.
- [ ] Test race lulus.
- [ ] Dokumentasi dan CONTRACT diperbarui.
- [ ] Perubahan perilaku dijelaskan di changelog/commit.

## Dokumen terkait

- Arsitektur: [02 — Arsitektur](02-architecture.md)
- Database: [11 — Database](11-database.md)
- Migrasi: [16 — Migrasi](16-migration.md)
