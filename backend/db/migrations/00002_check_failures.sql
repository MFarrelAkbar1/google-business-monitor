-- Dipakai scheduler (Fase 2) untuk backoff: berapa kali pengecekan listing ini
-- gagal berturut-turut. Kembali ke 0 setelah pengecekan berhasil.

-- +goose Up
ALTER TABLE listings
    ADD COLUMN consecutive_failures integer NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE listings
    DROP COLUMN consecutive_failures;
