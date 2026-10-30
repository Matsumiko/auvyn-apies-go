# 21 — Format Transaksi WhatsApp Bot Center

Dokumen ini mencatat format perintah WhatsApp bot center yang dipakai di deployment ini. Ini **bukan endpoint HTTP Auvyn** dan bukan kontrak publik: format bot center dapat berbeda antar provider/operator. Verifikasi ulang ke operator sebelum dipakai untuk otomasi.

Placeholder:

- `[kodeproduk]` — kode produk, mis. `PULSA10`
- `[tujuan]` — nomor/ID tujuan
- `[pin]` — PIN transaksi bot center
- `[trxid]` — ID transaksi yang diinginkan
- `[jumlah]` — nominal deposit
- `[nama]` — nama akun/agen

## Daftar format

| No | Aksi | Format | Contoh | Catatan |
|---|---|---|---|---|
| 1 | Ubah nama | `UN.[nama].[pin]` | `UN.TokoA.[PIN]` | Pemisah titik. |
| 2 | Change OID untuk OtomaX HTH | `UBAHOID [oid] [pin]` | `UBAHOID 12345 [PIN]` | Pemisah spasi. |
| 3 | Change IP Address | `IPADDRESS*[newip]*[pin]` | `IPADDRESS*203.0.113.10*[PIN]` | Pemisah bintang. |
| 4 | Transaksi pengisian pulsa + ref | `[kodeproduk].[tujuan].[pin] R#[trxid]` | `PULSA10.081234567890.[PIN] R#12345` | Suffix ` R#[trxid]` menentukan ID transaksi. |
| 5 | Transaksi pengisian pulsa | `[kodeproduk].[tujuan].[pin]` | `PULSA10.081234567890.[PIN]` | Tanpa suffix; bot membuat ID sendiri. |
| 6 | Tiket deposit | `TIKET.[jumlah].[pin]` | `TIKET.10000.[PIN]` | Membuat tiket deposit. |
| 7 | Tambah nomor WA | `TWA.[nomor].[pin]` | `TWA.081234567890.[PIN]` | Menambahkan nomor WhatsApp. |
| 8 | Cek status N transaksi terakhir | `LAP` | `LAP` | N mengikuti `Setting.LimitCekTransaksiAkhir`. |
| 9 | Cek status transaksi | `CEK.[tujuan]` | `CEK.081234567890` | Cek berdasarkan nomor tujuan. |
| 10 | Cek saldo akhir | `SAL` | `SAL` | Tanpa argumen. |
| 11 | Cek harga produk | `CH.[kodeproduk]` | `CH.PULSA10` | Cek harga produk. |
| 12 | Change IP Password | `PASSIP*[oldpassword]*[newpassword]*[pin]` | `PASSIP*old*new*[PIN]` | Pemisah bintang. |
| 13 | Ubah PIN | `GP.[pinlama].[pinbaru]` | `GP.[PINLAMA].[PINBARU]` | Pemisah titik. |

## Pemisah perintah

| Kelompok | Pemisah |
|---|---|
| `UN`, transaksi, `TIKET`, `TWA`, `CEK`, `CH`, `GP` | titik (`.`) |
| `IPADDRESS`, `PASSIP` | bintang (`*`) |
| `UBAHOID` | spasi |
| `LAP`, `SAL` | tanpa argumen |

Jangan mencampur pemisah. `PULSA10.081234567890.[PIN]` berbeda dari `PULSA10*081234567890*[PIN]`.

## Catatan operasional

- PIN dan password adalah kredensial. Jangan kirim ke log, screenshot publik, atau chat yang tidak aman.
- Untuk uji coba, gunakan nominal kecil dan produk yang jelas.
- Format `R#[trxid]` bersifat opsional; tanpa suffix, bot center membuat ID transaksi sendiri.
- `LAP` dibatasi oleh konfigurasi `Setting.LimitCekTransaksiAkhir` milik bot center.
- Jika operator mengubah format, perbarui dokumen ini dan test vector terkait.

## Pemetaan ke Auvyn HTTP API

Ini bukan pemetaan otomatis; hanya padanan konsep:

| WhatsApp bot center | Auvyn Apies | Catatan |
|---|---|---|
| Transaksi no. 4/5 | `POST /api/transaction` | Kirim `product`, `dest`, `qty`, `refID`; PIN provider tidak dikirim ke Auvyn. |
| `TIKET.[jumlah].[pin]` | `POST /api/ticket-mbal` | Body `amount`; memakai `x-auvyn-secret` operasional. |
| `SAL` | `POST /api/balance` | Memakai `x-auvyn-secret` operasional. |
| `CEK` / `LAP` | `GET|POST /api/transaction/status` | Auvyn mencari berdasarkan `refID`, bukan nomor tujuan. |
| `CH.[kodeproduk]` | — | Harga produk tidak diekspos oleh Auvyn. |
| `UN`, `UBAHOID`, `IPADDRESS`, `TWA`, `PASSIP`, `GP` | — | Perintah admin bot center, bukan API Auvyn. |

Signature provider memakai prefix default `OtomaX` (case-sensitive), tetapi perintah WhatsApp di atas tidak menghitung signature; bot center yang menanganinya.
