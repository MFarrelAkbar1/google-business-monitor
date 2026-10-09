-- Schema awal Jaga Listing (lihat PLAN.md bagian 5).
--
-- Aturan penting: tidak ada nilai dari Google yang disimpan di sini, kecuali
-- place_id. Kolom "baseline" berisi data milik pemilik usaha.
--
-- Nilai pilihan (kind, severity, status, channel) memakai CHECK constraint di
-- atas kolom text, bukan tipe ENUM Postgres, karena CHECK lebih mudah diubah
-- lewat migrasi berikutnya.

-- +goose Up
CREATE EXTENSION IF NOT EXISTS citext;

CREATE TABLE users (
    id               bigserial   PRIMARY KEY,
    email            citext      NOT NULL UNIQUE,
    password_hash    text        NOT NULL, -- argon2id
    telegram_chat_id bigint,
    created_at       timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE sessions (
    token_hash bytea       PRIMARY KEY, -- hash token, bukan token aslinya
    user_id    bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX sessions_user_id_idx ON sessions (user_id);

CREATE TABLE listings (
    id                bigserial   PRIMARY KEY,
    user_id           bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    place_id          text        NOT NULL,
    label             text        NOT NULL DEFAULT '',
    poll_interval     interval    NOT NULL DEFAULT '12 hours',
    next_check_at     timestamptz NOT NULL DEFAULT now(),
    last_checked_at   timestamptz,
    last_check_status text,
    last_error        text,
    active            boolean     NOT NULL DEFAULT true,
    created_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, place_id),
    CHECK (poll_interval >= interval '1 hour')
);
-- Dipakai scheduler: listing aktif yang sudah waktunya dicek.
CREATE INDEX listings_due_idx ON listings (next_check_at) WHERE active;

CREATE TABLE baselines (
    listing_id      bigint      PRIMARY KEY REFERENCES listings (id) ON DELETE CASCADE,
    name            text        NOT NULL DEFAULT '',
    phone_e164      text        NOT NULL DEFAULT '',
    whatsapp_e164   text        NOT NULL DEFAULT '',
    website_domain  text        NOT NULL DEFAULT '',
    website_url     text        NOT NULL DEFAULT '',
    address         text        NOT NULL DEFAULT '',
    business_status text        NOT NULL DEFAULT 'OPERATIONAL',
    updated_at      timestamptz NOT NULL DEFAULT now()
);

-- Hasil cocok/tidak cocok terakhir per field, tanpa nilai Google.
CREATE TABLE field_checks (
    listing_id bigint      NOT NULL REFERENCES listings (id) ON DELETE CASCADE,
    field      text        NOT NULL,
    matches    boolean     NOT NULL,
    checked_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (listing_id, field)
);

CREATE TABLE change_events (
    id           bigserial   PRIMARY KEY,
    listing_id   bigint      NOT NULL REFERENCES listings (id) ON DELETE CASCADE,
    field        text        NOT NULL,
    kind         text        NOT NULL CHECK (kind IN ('mismatch', 'missing')),
    severity     text        NOT NULL CHECK (severity IN ('critical', 'medium', 'low')),
    detected_at  timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    status       text        NOT NULL DEFAULT 'open'
        CHECK (status IN ('open', 'confirmed_mine', 'reported_not_mine', 'auto_resolved')),
    resolved_at  timestamptz
);
-- Dedup: paling banyak satu event "open" per (listing, field).
CREATE UNIQUE INDEX change_events_open_uniq ON change_events (listing_id, field) WHERE status = 'open';
CREATE INDEX change_events_listing_idx ON change_events (listing_id, detected_at DESC);

CREATE TABLE notifications (
    id              bigserial   PRIMARY KEY,
    change_event_id bigint      NOT NULL REFERENCES change_events (id) ON DELETE CASCADE,
    channel         text        NOT NULL CHECK (channel IN ('email', 'telegram')),
    status          text        NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'sent', 'failed')),
    attempts        integer     NOT NULL DEFAULT 0,
    last_error      text,
    sent_at         timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (change_event_id, channel)
);
-- Dipakai worker outbox: notifikasi yang belum terkirim.
CREATE INDEX notifications_pending_idx ON notifications (created_at) WHERE status = 'pending';

CREATE TABLE api_usage_daily (
    day    date    NOT NULL,
    sku    text    NOT NULL,
    calls  integer NOT NULL DEFAULT 0,
    errors integer NOT NULL DEFAULT 0,
    PRIMARY KEY (day, sku)
);

-- +goose Down
DROP TABLE api_usage_daily;
DROP TABLE notifications;
DROP TABLE change_events;
DROP TABLE field_checks;
DROP TABLE baselines;
DROP TABLE listings;
DROP TABLE sessions;
DROP TABLE users;
DROP EXTENSION IF EXISTS citext;
