-- Dipakai pengirim notifikasi (Task 4): kapan notifikasi boleh dicoba kirim
-- lagi. Kolom ini juga berfungsi sebagai lease, sama seperti
-- listings.next_check_at.

-- +goose Up
ALTER TABLE notifications
    ADD COLUMN next_attempt_at timestamptz NOT NULL DEFAULT now();

DROP INDEX notifications_pending_idx;
CREATE INDEX notifications_pending_idx ON notifications (next_attempt_at) WHERE status = 'pending';

-- +goose Down
DROP INDEX notifications_pending_idx;
CREATE INDEX notifications_pending_idx ON notifications (created_at) WHERE status = 'pending';

ALTER TABLE notifications
    DROP COLUMN next_attempt_at;
