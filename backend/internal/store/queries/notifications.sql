-- name: ClaimPendingNotifications :many
-- Pola outbox: ambil notifikasi pending yang sudah waktunya dikirim, naikkan
-- attempts, dan majukan next_attempt_at sebesar lease (sama seperti
-- ClaimDueListings). Sekalian ambil semua data yang dibutuhkan isi pesan.
-- Hanya channel yang pengirimnya aktif yang diambil; sisanya tetap pending.
WITH due AS (
    SELECT id FROM notifications
    WHERE status = 'pending'
      AND next_attempt_at <= now()
      AND channel = ANY(sqlc.arg(channels)::text[])
    ORDER BY next_attempt_at
    LIMIT sqlc.arg(batch_size)
    FOR UPDATE SKIP LOCKED
)
UPDATE notifications n
SET attempts        = n.attempts + 1,
    next_attempt_at = now() + sqlc.arg(lease)::interval
FROM due, change_events e, listings l, users u, baselines b
WHERE n.id = due.id
  AND e.id = n.change_event_id
  AND l.id = e.listing_id
  AND u.id = l.user_id
  AND b.listing_id = l.id
RETURNING
    n.id, n.channel, n.attempts,
    e.field, e.kind, e.severity, e.detected_at,
    l.label, l.place_id,
    u.email, u.telegram_chat_id,
    b.name           AS baseline_name,
    b.phone_e164     AS baseline_phone_e164,
    b.whatsapp_e164  AS baseline_whatsapp_e164,
    b.website_domain AS baseline_website_domain,
    b.website_url    AS baseline_website_url,
    b.address        AS baseline_address,
    b.business_status AS baseline_business_status;

-- name: MarkNotificationSent :exec
UPDATE notifications
SET status = 'sent', sent_at = now(), last_error = NULL
WHERE id = $1;

-- name: MarkNotificationFailed :exec
-- Error permanen atau percobaan sudah habis → status failed (berhenti).
-- Selain itu tetap pending dan dicoba lagi dengan backoff 1m, 2m, 4m, ...,
-- maksimal 1 jam. attempts sudah dinaikkan saat claim.
UPDATE notifications
SET last_error      = sqlc.arg(last_error)::text,
    status          = CASE
        WHEN sqlc.arg(permanent)::boolean OR attempts >= sqlc.arg(max_attempts)::int THEN 'failed'
        ELSE 'pending'
    END,
    next_attempt_at = now() + LEAST(interval '1 hour', interval '1 minute' * power(2, LEAST(attempts - 1, 10)))
WHERE id = sqlc.arg(id);
