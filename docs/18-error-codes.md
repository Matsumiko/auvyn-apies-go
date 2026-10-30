# 18 — Kode error

Katalog kode error, status HTTP, dan tindakan yang disarankan. Bentuk envelope berbeda antara API publik/legacy dan API admin/client v1.

## Bentuk respons

API publik dan legacy:

```json
{"ok": false, "error": "BAD_REQUEST", "message": "..."}
```

API admin/client v1:

```json
{"error": {"code": "BAD_REQUEST", "message": "..."}}
```

`/report` mengembalikan `403` dengan envelope publik untuk kegagalan guard; kegagalan pemrosesan report tetap `200 OK`.

## API publik

| HTTP | Code | Penyebab | Tindakan |
|---|---|---|---|
| 400 | `BAD_REQUEST` | JSON invalid, field wajib kosong, nominal invalid | perbaiki payload |
| 401 | `UNAUTHORIZED` | kredensial client atau `x-auvyn-secret` salah/kosong | cek header sesuai endpoint |
| 404 | `NOT_FOUND` | transaksi tidak ada atau bukan milik client | cek `refID` |
| 409 | `DUPLICATE_REF` | `refID` sudah ada dengan product/dest berbeda | gunakan `refID` unik atau payload sama |
| 409 | `IN_PROGRESS` | intent `refID` belum selesai | tunggu/rekonsiliasi, jangan kirim ulang dengan ID baru |
| 502 | `CENTER_ERROR` | provider timeout/gagal | cek provider; intent disimpan |
| 500 | `INTERNAL_ERROR` | database/internal | cek log dan DB |
| 500 | `SERVER_MISCONFIGURED` | `AUVYN_SECRET` kosong | isi environment server |
| 404 | `NOT_FOUND` | route tidak ada | cek path/method |

## Admin/client v1

| HTTP | Code | Penyebab | Tindakan |
|---|---|---|---|
| 400 | `BAD_REQUEST` | JSON/URL/events/pagination invalid | perbaiki request |
| 401 | `UNAUTHORIZED` | kredensial admin/client salah | cek Bearer/PIN/keys |
| 403 | `FORBIDDEN` | resource milik client lain | gunakan resource sendiri |
| 404 | `NOT_FOUND` | client/webhook tidak ada/sudah dihapus | cek ID |
| 409 | `NOT_ACTIVE` | client/webhook tidak aktif | gunakan resource aktif |
| 500 | `SERVER_MISCONFIGURED` | kredensial admin kosong | isi environment |
| 500 | `INTERNAL_ERROR` | database/internal | cek log |

`405 Method Not Allowed` dapat dikembalikan oleh router untuk kombinasi path+method yang tidak terdaftar; gunakan method yang tercantum di dokumentasi.

## Health

| HTTP | Code | Arti |
|---|---|---|
| 503 | `DB_UNAVAILABLE` | database tidak dapat di-ping (`/readyz`) |
| 404 | `NOT_FOUND` | path tidak ada |

## `/report`

| HTTP | Kondisi | Body |
|---|---|---|
| 200 | diterima, duplikat, konflik, refID asing, destination mismatch | teks `OK` |
| 403 | IP/token tidak valid | JSON error |
| 400 | body tidak dapat dibaca | JSON error |

Alasan pemrosesan tidak dikirim ke provider; lihat `provider_reports.reason`:

| Reason | Arti |
|---|---|
| `applied` | transaksi berubah ke final |
| `duplicate` | kategori sama sudah final |
| `conflict` | final berbeda, status lama dipertahankan |
| `unknown_ref` | `refID` tidak ada di ledger |
| `missing_ref` | report tanpa `refID` |
| `dest_mismatch` | tujuan pada pesan berbeda dengan ledger |

## Webhook (log/DB, bukan respons API)

| Status/field | Arti |
|---|---|
| `deliveries.last_error` | error jaringan atau status HTTP target |
| `deliveries.response_status` | status HTTP terakhir |
| `dead` | gagal setelah batas percobaan atau resource dihapus |
| `Callback error` | request ke target gagal |
| `Callback not ok` | target membalas non-2xx |

## Diagnostik

```bash
# error terakhir di log
journalctl -u auvyn-apies -n 200 --no-pager | grep -E 'error|warn'

# report ditolak/konflik
sqlite3 data/auvyn.db "SELECT ref_id,reason,category,received_at FROM provider_reports WHERE accepted=0 ORDER BY id DESC LIMIT 20;"

# delivery bermasalah
sqlite3 data/auvyn.db "SELECT id,target_name,status,attempts,last_error FROM deliveries WHERE status IN ('dead','pending') ORDER BY id DESC LIMIT 20;"
```

## Dokumen terkait

- API publik: [05 — API publik](05-api-public.md)
- API admin: [06 — API admin](06-api-admin.md)
- API client: [07 — API client](07-api-client.md)
- Report: [08 — Report provider](08-report-ingestion.md)
- Troubleshooting: [17 — Troubleshooting](17-troubleshooting.md)
