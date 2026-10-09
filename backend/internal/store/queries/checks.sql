-- name: UpsertFieldCheck :exec
INSERT INTO field_checks (listing_id, field, matches, checked_at)
VALUES ($1, $2, $3, now())
ON CONFLICT (listing_id, field) DO UPDATE SET
    matches    = EXCLUDED.matches,
    checked_at = EXCLUDED.checked_at;

-- name: ListFieldChecks :many
SELECT * FROM field_checks
WHERE listing_id = $1
ORDER BY field;

-- name: OpenOrTouchChangeEvent :one
-- Dedup: kalau sudah ada event "open" untuk (listing, field), hanya
-- last_seen_at yang diperbarui. inserted = true berarti event baru, dan hanya
-- event baru yang perlu dibuatkan notifikasi.
-- (xmax = 0) adalah trik Postgres: bernilai true jika baris hasil INSERT,
-- false jika baris hasil UPDATE dari ON CONFLICT.
INSERT INTO change_events (listing_id, field, kind, severity)
VALUES ($1, $2, $3, $4)
ON CONFLICT (listing_id, field) WHERE status = 'open' DO UPDATE SET
    last_seen_at = now()
RETURNING id, (xmax = 0)::boolean AS inserted;

-- name: AutoResolveChangeEvent :execrows
-- Dipanggil saat field kembali cocok.
UPDATE change_events
SET status = 'auto_resolved', resolved_at = now()
WHERE listing_id = $1 AND field = $2 AND status = 'open';

-- name: ListOpenChangeEvents :many
SELECT * FROM change_events
WHERE listing_id = $1 AND status = 'open'
ORDER BY detected_at;

-- name: CreateNotification :exec
INSERT INTO notifications (change_event_id, channel)
VALUES ($1, $2)
ON CONFLICT (change_event_id, channel) DO NOTHING;

-- name: IncrementAPIUsage :exec
INSERT INTO api_usage_daily (day, sku, calls, errors)
VALUES (current_date, $1, 1, CASE WHEN sqlc.arg(is_error)::boolean THEN 1 ELSE 0 END)
ON CONFLICT (day, sku) DO UPDATE SET
    calls  = api_usage_daily.calls + 1,
    errors = api_usage_daily.errors + EXCLUDED.errors;
