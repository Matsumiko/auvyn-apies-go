# 20 — Protokol Otomax (IP Center)

Dokumen ini merangkum protokol Otomax/IP Center dan memetakannya ke implementasi Auvyn Apies v2.

> Dokumen ini adalah panduan internal, bukan kontrak baru. Perilaku provider aktual (yang sudah diverifikasi lewat transaksi/report) tetap menang. Jangan mengubah request keluar tanpa konfirmasi provider.

---

## 1. Model koneksi

Ketentuan umum:

> Koneksi melalui IP statis dengan protokol HTTP GET.

Kredensial yang diperlukan:

| Item | Keterangan |
|---|---|
| Production IP | IP statis yang dipakai untuk transaksi live |
| Production Callback | URL/IP yang menerima report dari provider |
| PIN | PIN transaksi |
| Password | Password transaksi |

Pada project ini:

| Protokol umum | Implementasi Auvyn |
|---|---|
| Production IP | IP egress VPS yang didaftarkan ke provider (`IPADDRESS*...*PIN`) |
| Production Callback | `REPORTURL` domain `/report` |
| PIN | `PIN` di `.env` |
| Password | `PASSWORD` di `.env` |
| Base endpoint | `CENTER_URL` |

Provider memproses request secara **sinkron**: respons awal dapat langsung `success`, `failed`, atau `pending`. Report final dikirim kemudian ke callback.

---

## 2. Endpoint protokol

Protokol umum menggunakan:

```text
http://IPCENTER:PORT
```

Contoh:

```text
http://IPCENTER:PORT/saldo?memberID=[memberid]&sign=[sign]

http://IPCENTER:PORT/trx?product=HSF10&qty=1&dest=0812345678&refID=123456789&memberID=OX9999&sign=[sign]
```

Transaksi tanpa sign:

```text
http://IPCENTER:PORT/trx?product=HSF10&qty=1&dest=0812345678&refID=123456789&memberID=OX9999&password=1234&pin=1234
```

### Endpoint yang dipakai project ini

| Operasi | Path project | Catatan |
|---|---|---|
| Transaksi | `/trx` | `product`, `dest`, `qty`, `refID`, `memberID`, `sign`, opsional `nominal` |
| Cek saldo | `/balance` | Provider aktual menerima `/balance` (diverifikasi 29 Okt 2026) |
| Tiket M-Bal | `/?cmd=ticket` | `memberid`, `amount`, `sign` |

Catatan penting:

- Protokol umum menulis `/saldo`; provider yang dipakai project ini merespons `/balance`. **Jangan mengganti path** tanpa konfirmasi provider.
- Protokol umum memakai nama `kodeproduk`, `tujuan`, `trxid`; provider aktual menerima `product`, `dest`, `refID`. Project mengikuti yang aktual.
- Protokol umum mendukung transaksi tanpa sign dengan `pin` + `password`. Project saat ini selalu mengirim sign untuk transaksi; opsi tanpa sign tidak diaktifkan.

---

## 3. Parameter request

### Protokol umum

| Parameter | Deskripsi | Wajib |
|---|---|---|
| `memberid` | ID agen | Ya |
| `password` | Password | Ya |
| `pin` | PIN | Ya |
| `kodeproduk` | Kode produk | Ya |
| `tujuan` | Nomor/ID tujuan | Ya |
| `qty` | Quantity | Tidak |
| `trxid` | ID transaksi unik | Ya |

### Pemetaan project

| Protokol umum | Project |
|---|---|
| `memberid` | `memberID` / `memberid` |
| `password` | `PASSWORD` (env) |
| `pin` | `PIN` (env) |
| `kodeproduk` | `product` |
| `tujuan` | `dest` |
| `trxid` | `refID` |
| `qty` | `qty` |

Untuk produk bebas nominal, project **tidak mengirim nominal**; `qty` mewakili nominal. Lihat [09 — Protokol provider](09-provider-protocol.md).

---

## 4. Rumus signature

### Rumus signature

```php
<?php
// transaksi
$str = 'OtomaX|' . $memberId . '|' . $product . '|' . $dest . '|' . $refID . '|' . $pin . '|' . $password;

// deposit tiket
$str = 'OtomaX|ticket|' . $memberId . '|' . $pin . '|' . $password . '|' . $amount;

// cek saldo
$str = 'OtomaX|CheckBalance|' . $memberId . '|' . $pin . '|' . $password;

$sign = str_replace('/', '_', str_replace('+', '-', rtrim(base64_encode(sha1($str, true)), '=')));
?>
```

Catatan: untuk cek transaksi/saldo tanpa `product`, `dest`, atau `refID`, rumus tidak berubah—parameter tersebut dikosongkan:

```text
OtomaX|OT0000||||8715|yunw)uc&@
```

### Implementasi Go

| Fungsi | Template |
|---|---|
| `BuildSignature` | `prefix\|memberId\|product\|dest\|refID\|pin\|password[ \|nominal]` |
| `BuildCheckBalanceSignature` | `prefix\|CheckBalance\|memberId\|pin\|password` |
| `BuildTicketSignature` | `prefix\|ticket\|memberId\|pin\|password\|amount` |

`prefix` berasal dari `SIGN_PREFIX` dan **case-sensitive**. Provider yang dipakai project ini memakai `OtomaX`.

`nominal` hanya masuk template transaksi bila:

- `SIGN_INCLUDE_NOMINAL=true`, dan
- nominal ada, dan
- produk bukan produk bebas.

Pada produk bebas, nominal tidak dikirim dan tidak masuk signature.

### Implementasi encoding

1. SHA1 atas string template (binary).
2. Base64 standar.
3. `+` → `-`, `/` → `_`.
4. Buang padding `=`.

### Test vector

| Kasus | Template | Expected `sign` |
|---|---|---|
| Transaksi | `ENGINE\|AG000001\|PULSA10\|081234567890\|AVN1750000000000\|0000\|Secret123` | `iYvFqEokeut7u5lFWG60-MH1OWc` |
| Transaksi + nominal 15000 | template di atas + `\|15000` | `LIQfj8k2qTHiEGHUpy0y8-84jPY` |
| Cek saldo | `ENGINE\|CheckBalance\|AG000001\|0000\|Secret123` | `xsqtqJgRRgn0Eirf-6KCnKNL01o` |
| Tiket 50000 | `ENGINE\|ticket\|AG000001\|0000\|Secret123\|50000` | `K7rt5-vC7HGg9iSeerIE0gjF_lE` |

Vector diuji di `internal/ipcenter/sign_test.go`.

---

## 5. Cek saldo

### Protokol umum

```text
GET /saldo?memberID=[memberid]&sign=[sign]
```

### Project

`GET /balance` dengan:

```text
memberID=<id>
memberid=<id>
pin=<pin>
password=<password>
sign=base64url(sha1(prefix|CheckBalance|memberId|pin|password))
```

Parameter `memberID` dan `memberid` keduanya dikirim untuk kompatibilitas. Provider aktual menerima format ini (diverifikasi 29 Oktober 2026).

Contoh respons provider:

```text
Yth. Nama Agen, ID Agen: XXXXX
Saldo 42.068
Dlm proses 0
Pakai hari ini 10.305
```

`ParseBalance` mengambil nilai setelah `Saldo`.

---

## 6. Tiket M-Bal

### Protokol umum

```text
prefix|ticket|memberId|pin|password|amount
```

### Project

```text
GET /?cmd=ticket&memberid=<id>&amount=<amount>&sign=<sign>
```

Sign memakai `BuildTicketSignature`.

Status uji live: **teruji**. Provider menerima request `amount=30000` dengan `HTTP 200`. Responsnya bukan ID tiket, melainkan instruksi transfer deposit:

```text
Silakan Transfer Rp. 30.967 (HARUS SAMA)
[rekening provider]
```

- `amount` = 30.000 (nominal yang diminta);
- nominal transfer = 30.967 (termasuk biaya);
- `ticket` = string kosong karena provider tidak mengirim `TICKET:`/`TIKET:`/`KODE:`;
- instruksi lengkap ada di `providerResult.raw`.

Ini adalah tiket deposit nyata; jika tidak ditransfer, tiket akan kedaluwarsa sendiri.

---

## 7. Contoh transaksi

### Dengan sign

```text
GET /trx?product=HSF10&qty=1&dest=0812345678&refID=123456789&memberID=OX9999&sign=[sign]
```

### Tanpa sign (protokol umum)

```text
GET /trx?product=HSF10&qty=1&dest=0812345678&refID=123456789&memberID=OX9999&password=1234&pin=1234
```

Project tidak memakai mode tanpa sign.

---

## 8. Contoh respons transaksi

### Sukses

```text
R#987654321 HSF5.088123456789 SUKSES. SN/Ref: 123456789. Saldo 99.999.999-4.880=99.995.119 @17/11 04.19.18
R#123456789 Fisik VSFNS12B.088123456789 SUKSES. No.Voucher: SN: 123456789101112 Harga 51807. Sal 99.999.999 @04.00
```

### Gagal

```text
R#198386403 HSF15.088123456789 GAGAL. . Saldo 99.999.999 @04.17
R#123456789 HSF15.088123456789 GAGAL. Nomor tujuan salah. Sal 99.999.999 @04.17
```

### Produk gangguan / stok kosong

```text
R#123456789 HSF5.088123456789 GAGAL. Produk sedang gangguan. Saldo 99.999.999 @03.21
R#123456789 VSFULITE28.088123456789 GAGAL. Stok sedang kosong/ditutup. Saldo 99.999.999 @09.07
```

Parser project sudah mendukung format `#trxid PRODUCT.dest ...`, `R#...`, `R...`, `Inject`, dan format lapangan. Lihat [09 — Protokol provider](09-provider-protocol.md).

---

## 9. Status report

| Kode | Keterangan |
|---|---|
| 20 | Sukses |
| 52 | Tujuan salah |
| 40 | Gagal |
| 2 | Menunggu jawaban |
| 69 | CutOff |
| 50 | Dibatalkan |
| 56 | Nomor Blacklist |
| 47 | Produk Gangguan |
| 45 | Stok Kosong |
| 55 | TimeOut |
| 53 | Tujuan Diluar Wilayah |

Project mengklasifikasi report dengan presedensi:

1. teks `gagal`/`failed`/`error`/`reject` → failed;
2. `status=20` atau teks sukses → success;
3. parser Otomax;
4. sisanya pending.

Kode status selain 20 tidak dipetakan satu per satu; teks pesan provider menjadi penentu utama.

### Parameter report protokol umum

| Parameter | Keterangan |
|---|---|
| `serverid` | RefID server/provider |
| `clientid` | RefID client |
| `statuscode` | Status transaksi |

Provider yang dipakai project ini mengirim:

```text
GET /report?refid=...&status=...&message=...
```

Project membaca `refid`/`refID`, `status`, dan `message`. Jika suatu saat provider mengirim `serverid`/`clientid`/`statuscode`, perlu adapter tambahan; jangan berasumsi nama field sama.

---

## 10. Cek status dan idtrx

Ketentuan:

> Cek Status dapat dilakukan dengan topup ulang menggunakan idtrx yang sama.
>
> Jangan melakukan cek status untuk transaksi lebih dari 90 hari; dapat membuat transaksi baru.

Project saat ini:

- `refID` final yang dikirim ulang → **replay dari ledger**, provider tidak dipanggil.
- `refID` yang masih pending → `409 IN_PROGRESS`, bukan query status baru.
- Tidak ada fungsi cek status provider terpisah.

Konsekuensinya, mekanisme “topup ulang dengan idtrx yang sama” dari protokol umum **tidak sama** dengan perilaku project. Jangan mengandalkan replay API sebagai cek status provider.

---

## 11. Perbedaan protokol umum vs project

| Area | Protokol umum | Project |
|---|---|---|
| Path saldo | `/saldo` | `/balance` (diverifikasi provider aktual) |
| Nama parameter | `kodeproduk`, `tujuan`, `trxid` | `product`, `dest`, `refID` |
| Transaksi tanpa sign | Didukung (PIN/password) | Tidak dipakai; selalu sign |
| Cek status | Topup ulang `idtrx` | Replay ledger / 409 |
| Report field | `serverid`, `clientid`, `statuscode` | `refid`, `status`, `message` |
| Produk bebas nominal | Tidak dijelaskan | Didukung; nominal tidak dikirim |
| Tiket | `prefix\|ticket\|...` | Teruji live; respons berupa instruksi transfer |

---

## 12. Aturan perubahan

Perubahan path, nama parameter, template signature, atau perilaku non-sign harus melalui:

1. capture request/response provider;
2. konfirmasi provider;
3. test vector baru di `internal/ipcenter`;
4. pembaruan `CONTRACT.md` dan dokumen ini.

Tanpa ketiganya, perlakukan sebagai bug implementasi, bukan izin mengubah protokol.

---

## 13. Status verifikasi project

| Item | Status |
|---|---|
| Signature transaksi | Teruji dan dipakai |
| Signature cek saldo | Teruji live; `/api/balance` mengembalikan saldo |
| Signature tiket | Teruji live; request `amount=30000` diterima provider |
| Endpoint `/balance` | Teruji live |
| Report domain | Teruji live, `accepted=1` |
| Parser `#trxid PRODUCT.dest` | Teruji live |
| Format `kodeproduk/tujuan/trxid` | Tidak dipakai provider aktual |
