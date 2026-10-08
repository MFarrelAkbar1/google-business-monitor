# Jaga Listing — Rencana

Layanan yang memantau listing Google Maps / Google Business Profile milik UMKM dan memberi
peringatan ketika data penting (telepon, website, nama, alamat, status) tidak lagi cocok
dengan data yang benar menurut pemilik.

Status: **disetujui** (revisi 1). Perubahan rencana dicatat di bagian "Riwayat keputusan".

---

## 1. Kepatuhan terhadap Google Maps Platform Terms

Hasil pengecekan Service Specific Terms (dicek oleh pemilik proyek):

- Untuk Places API, hanya **lat/lng** yang boleh di-cache (maks. 30 hari). **place_id boleh disimpan** tanpa batas.
- Karena itu **tidak ada nilai dari Google yang disimpan**, termasuk hash-nya (hash nomor
  telepon mudah di-brute-force karena ruang nilainya kecil).
- Yang disimpan hanya:
  - `place_id`,
  - baseline: data yang diisi atau dikonfirmasi pemilik (milik pemilik, bukan konten Google),
  - hasil perbandingan per field: **cocok / tidak cocok**, beserta jenis dan tingkat bahayanya.
- Nilai Google hanya ada di memori selama satu pengecekan. Nilai itu boleh ditampilkan ke
  pemilik secara langsung (misalnya di isi notifikasi atau hasil pencarian), tetapi tidak ditulis ke database.
- Jangan scraping. Hanya gunakan Places API (New).

## 2. Biaya API

- Field mask Place Details:
  `id,displayName,nationalPhoneNumber,internationalPhoneNumber,websiteUri,formattedAddress,businessStatus`
- Telepon dan website termasuk SKU **Place Details Enterprise**: US$20 per 1.000 call, gratis 1.000 call per bulan.
- Default `poll_interval` **12 jam**, sehingga 60 call/listing/bulan. Kuota gratis cukup untuk ±16 listing.
- Semua call ke Google melewati rate limiter global (token bucket di proses worker).
- Test dan dev memakai `FakeClient`, tidak memanggil API sungguhan.

## 3. Struktur folder

```
google-business-monitor/
├── PLAN.md
├── Taskfile.yml
├── docker-compose.yml        # postgres, mailpit
├── .env.example
├── backend/
│   ├── go.mod                # module jagalisting
│   ├── cmd/
│   │   ├── probe/            # cek manual satu place_id ke API asli
│   │   ├── worker/           # scheduler + pengecekan + notifikasi (Fase 2)
│   │   └── api/              # HTTP server (Fase 3)
│   ├── internal/
│   │   ├── places/           # interface Client, GoogleClient, FakeClient
│   │   ├── normalize/        # phone, url/domain, wa.me, teks
│   │   ├── diff/             # bandingkan dengan baseline + severity
│   │   ├── store/            # queries/*.sql + kode sqlc
│   │   ├── scheduler/        # interface + implementasi SKIP LOCKED (Fase 2)
│   │   ├── monitor/          # fetch → normalize → diff → event (Fase 2)
│   │   ├── notify/           # email, telegram (Fase 2)
│   │   ├── auth/             # argon2id, session (Fase 3)
│   │   └── httpapi/          # handler chi + middleware (Fase 3)
│   ├── db/migrations/        # goose
│   └── sqlc.yaml
└── web/                      # Next.js (Fase 4)
```

## 4. Aturan normalisasi dan perbandingan

| Field | Normalisasi | Dibandingkan dengan |
|---|---|---|
| Telepon | E.164 via `nyaruka/phonenumbers`, region default ID | `baseline.phone_e164` |
| Website | huruf kecil, buang skema, `www.`, trailing slash; domain = eTLD+1 (`publicsuffix`) | `baseline.website_domain` |
| Link WhatsApp di website (`wa.me/…`, `api.whatsapp.com/send?phone=…`) | ambil nomornya lalu E.164 | `baseline.whatsapp_e164` |
| Nama | trim, rapikan spasi, case-insensitive | `baseline.name` |
| Alamat | trim, rapikan spasi, case-insensitive | `baseline.address` |
| businessStatus | apa adanya | `baseline.business_status` (default `OPERATIONAL`) |
| Jam buka | **tidak dipantau di MVP** | n/a |

### Tingkat bahaya

| Kondisi | Severity |
|---|---|
| Telepon tidak cocok (diganti atau muncul padahal baseline kosong) | KRITIS |
| Nomor WhatsApp di website tidak cocok | KRITIS |
| Domain website (eTLD+1) berbeda, misalnya `.co.id` → `.com` | KRITIS |
| Telepon atau website hilang | SEDANG |
| Nama atau alamat tidak cocok | SEDANG |
| businessStatus ≠ baseline (misalnya `CLOSED_PERMANENTLY`) | SEDANG |
| Domain sama, path berbeda | RENDAH |

## 5. Schema database

```sql
users(id bigserial PK, email citext UNIQUE, password_hash text,  -- argon2id
      telegram_chat_id bigint NULL, created_at)

sessions(token_hash bytea PK, user_id FK, expires_at, created_at)

listings(id bigserial PK, user_id FK, place_id text,
         label text,                               -- nama cabang versi pemilik
         poll_interval interval DEFAULT '12 hours',
         next_check_at timestamptz, last_checked_at timestamptz,
         last_check_status text, last_error text, active bool, created_at,
         UNIQUE (user_id, place_id))

baselines(listing_id PK FK, name, phone_e164, whatsapp_e164,
          website_domain, website_url, address,
          business_status text DEFAULT 'OPERATIONAL', updated_at)

-- Hasil cocok/tidak cocok terakhir per field (untuk dashboard), tanpa nilai Google
field_checks(listing_id FK, field, matches bool, checked_at,
             PK (listing_id, field))

change_events(id bigserial PK, listing_id FK, field,
              kind (mismatch | missing),
              severity (critical | medium | low),
              detected_at, last_seen_at,
              status (open | confirmed_mine | reported_not_mine | auto_resolved),
              resolved_at)
  UNIQUE (listing_id, field) WHERE status = 'open'   -- dedup

notifications(id, change_event_id FK, channel (email | telegram),
              status (pending | sent | failed), attempts, last_error, sent_at,
              UNIQUE (change_event_id, channel))

api_usage_daily(day, sku, calls, errors, PK (day, sku))
```

Catatan:
- `change_events` tidak menyimpan nilai Google. `baseline_value` juga tidak perlu karena
  baseline sudah ada di tabel `baselines`.
- **Dedup:** selama ada event `open` untuk (listing, field), mismatch berikutnya hanya
  memperbarui `last_seen_at`. Tidak ada event atau notifikasi baru.
- Field yang kembali cocok membuat event-nya menjadi `auto_resolved`.
- **"Ini saya":** pemilik memasukkan nilai baru sebagai baseline. Karena nilai Google tidak
  disimpan, pemilik mengetik ulang nilainya, atau UI mengambil nilai terkini lewat satu call
  live untuk ditampilkan dan dikonfirmasi pemilik.
- Event dan baris `notifications` ditulis dalam satu transaksi.

## 6. Scheduler

Fase 2 memakai scheduler sederhana, bukan ForgeQ:

- Loop setiap ±1 menit menjalankan:
  `SELECT … FROM listings WHERE active AND next_check_at <= now() ORDER BY next_check_at FOR UPDATE SKIP LOCKED LIMIT n`
- Setelah dicek: `next_check_at = now() + poll_interval`. Jika gagal, jadwal ulang memakai backoff.
- Notifikasi diproses dari tabel `notifications` (pola outbox) dengan retry.
- Dibungkus interface (`scheduler.Scheduler`) supaya nanti bisa diganti dengan ForgeQ.

## 7. Endpoint REST (Fase 3)

Semua query listing dan event difilter dengan `user_id` pemilik. Jika bukan miliknya,
respons 404 (pencegahan IDOR).

```
POST   /api/auth/register | /api/auth/login (rate limit) | /api/auth/logout
GET    /api/me
POST   /api/me/telegram/link
POST   /api/telegram/webhook          (secret header)

POST   /api/places/search             {query} → kandidat (Text Search)
GET    /api/listings
POST   /api/listings                  {place_id, label, baseline}
GET    /api/listings/{id}
PATCH  /api/listings/{id}             {label, poll_interval, active}
DELETE /api/listings/{id}
PUT    /api/listings/{id}/baseline
POST   /api/listings/{id}/check       (cek manual, dibatasi)

GET    /api/events?status=&listing_id=
POST   /api/events/{id}/mine          → baseline diperbarui (transaksi)
POST   /api/events/{id}/not-mine      → langkah pemulihan
GET    /healthz, /readyz
```

Resolve listing di MVP hanya lewat **pencarian nama** (Text Search (New)), lalu pemilik
memilih kandidat. Parser link Maps dan redirect short link ditunda.
⚠️ Text Search punya SKU sendiri. Cek harganya sebelum Fase 3.

## 8. Fase

- **Fase 1, inti backend**
  1. `cmd/probe`: menerima place_id, memanggil Place Details, mencetak field yang dipantau.
     **Berhenti di sini untuk uji kecepatan update API vs Google Maps.**
  2. Migrasi goose + sqlc, `FakeClient`, `normalize`, `diff` + unit test (wa.me berubah,
     `.co.id` → `.com`, `0812…` vs `+62812…`, http/https/www/trailing slash), integration test
     migrasi dengan testcontainers.
- **Fase 2:** scheduler, monitor, notifikasi email + Telegram dengan dedup.
- **Fase 3:** autentikasi + REST API.
- **Fase 4:** dashboard Next.js.
- **Fase 5:** deployment, health check, logging terstruktur, alert kuota/error API.

## 9. Asumsi yang masih terbuka ⚠️

- Seberapa cepat hasil Suggested Edit muncul di Places API dibanding di aplikasi Google Maps.
  Diuji memakai `cmd/probe`.
- Harga SKU Text Search (New) untuk field `id,displayName,formattedAddress`.
- `languageCode` memengaruhi format `displayName` dan `formattedAddress`. Probe memakai
  `id` / region `ID`. Baseline harus dibuat dengan bahasa yang sama.

## Riwayat keputusan

- **Revisi 1:** tidak menyimpan nilai atau hash dari Google; jam buka tidak dipantau;
  baseline businessStatus = OPERATIONAL; dedup satu event open per (listing, field);
  poll default 12 jam; scheduler sederhana (bukan ForgeQ); resolve hanya lewat Text Search;
  probe dikerjakan pertama.
