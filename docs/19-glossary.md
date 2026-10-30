# 18 — Glosarium

Istilah diurutkan alfabetis.

## Admin

Klien internal yang mengelola client dan webhook melalui `/admin/*`. Autentikasinya memakai secret, PIN, dan keys.

## Bebas nominal

Mode produk di mana nominal tidak dikirim ke provider; `qty` mewakili nominal. Daftar produk diatur `EWALLET_BEBAS_PRODUCTS`.

## Callback

HTTP POST dari service ke backend/webhook konsumen. Terdiri dari callback legacy (environment) dan managed webhook (per client).

## Category

Kategori transaksi: `pending`, `success`, `failed`.

## Client

Pemilik webhook yang terdaftar di service. Memiliki `client_secret`, `client_pin`, `client_keys`, dan `secret_callback`.

## Conflict

Kondisi ketika report final datang dengan kategori berbeda dari status final yang sudah tersimpan. Status lama dipertahankan; konflik dicatat dan di-alert.

## Dead-letter

Delivery yang gagal setelah batas percobaan (`DELIVERY_MAX_ATTEMPTS`) atau dibatalkan karena webhook/client dihapus. Statusnya `dead`.

## Dedupe key

Kunci unik pada tabel `events` yang mencegah event yang sama ditulis dua kali.

## Delivery

Satu percobaan pengiriman event ke satu target. Satu event bisa memiliki beberapa delivery.

## Destination (`dest`)

Nomor tujuan atau identitas tujuan transaksi (misalnya nomor HP atau ID akun game).

## Duplicate

Report dengan kategori sama untuk transaksi yang sudah final. Tidak menghasilkan event baru.

## Event

Kejadian internal yang disimpan di outbox: `transaction.request`, `transaction.report`, `balance.check`, `balance.ticket`, atau `invoice.*` untuk managed webhook.

## Event ID

ID numerik event (`x-auvyn-event-id`) yang dapat dipakai konsumen untuk idempotensi.

## Final

Kategori `success` atau `failed`. Transaksi final tidak berubah kecuali ada konflik yang ditangani manual.

## HMAC

Hash-based Message Authentication Code. Service memakai HMAC-SHA256 atas `timestamp + "." + body` untuk callback.

## Idempotensi

Sifat operasi yang aman diulang tanpa efek ganda. Diterapkan pada `refID`, event dedupe, dan replay transaksi final.

## Intent

Baris transaksi `pending` yang ditulis sebelum provider dipanggil. Intent mencegah double charge dan menyimpan konteks untuk rekonsiliasi.

## IP Center

Engine/provider produk digital yang menjadi tujuan request keluar. Protokolnya bergaya Otomax.

## Legacy

Jalur lama yang dipertahankan untuk kompatibilitas: callback berbasis environment dan path admin/client verb-style.

## M-Bal

Saldo/istilah saldo pada pesan provider. Juga dapat muncul sebagai `Saldo` pada format lapangan.

## Managed webhook

Webhook per client yang disimpan di database dan menerima event `invoice.*`, dipilih berdasarkan `meta.client_id`.

## Nominal

Nilai uang transaksi (rupiah integer). Untuk produk bebas, nominal menjadi dasar `qty`.

## Otomax

Gaya protokol provider: HTTP GET dengan signature SHA1, parameter query, dan respons berupa teks yang diparse dengan pola regex.

## Outbox

Pola penyimpanan event dan delivery di database sebelum pengiriman, agar event tidak hilang saat proses berhenti.

## Pending

Kategori transaksi yang belum final.

## Provider

Sinonim IP Center.

## Prune

Pembersihan otomatis delivery selesai, event tanpa delivery, dan audit report lama sesuai `RETENTION_DAYS`. Transaksi tidak dipangkas.

## Qty

Jumlah pada request. Untuk produk bebas, qty mewakili nominal.

## RefID

ID transaksi yang dikirim ke provider dan dikembalikan pada report. Menjadi kunci idempotensi dan ledger.

## Replay

Pengembalian hasil transaksi final yang sudah ada ketika `refID` yang sama dikirim ulang, tanpa memanggil provider lagi.

## Report

Pesan masuk dari provider ke `/report` yang berisi status akhir transaksi.

## Sanitasi/redaksi

Proses menghapus atau mengganti data sensitif sebelum keluar dari proses: SN, saldo, sign, memberID, URL provider, token.

## Secret callback

Secret per client (atau legacy) untuk HMAC callback. Disimpan plaintext karena diperlukan untuk menghitung signature.

## Signature

Hasil `base64url(SHA1(template))` yang dikirim ke provider. Template dan prefix tidak boleh diubah tanpa bukti provider.

## SN/Ref

Serial number/referensi transaksi dari provider. Diredaksi sebagai `[REDACTED]` pada output eksternal.

## SSRF

Server-Side Request Forgery. Dicegah dengan validasi skema URL, blokir IP privat/loopback, dan resolusi DNS.

## Stale intent

Transaksi `pending` tanpa hasil provider lebih dari 15 menit. Ditampilkan di statistik untuk rekonsiliasi.

## State machine

Aturan transisi status transaksi dan delivery.

## WAL

Write-Ahead Logging SQLite. Meningkatkan konkurensi baca/tulis dan menghasilkan file `-wal`/`-shm`.

## Webhook

Endpoint HTTP milik client yang menerima callback event.
