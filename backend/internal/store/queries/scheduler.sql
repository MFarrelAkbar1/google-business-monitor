-- name: ClaimDueListings :many
-- Mengambil listing yang sudah waktunya dicek, lalu langsung memajukan
-- next_check_at sebesar "lease". Selama lease berlaku, worker lain tidak akan
-- mengambil listing yang sama. Kalau worker mati di tengah pengecekan,
-- listing otomatis dicek ulang setelah lease habis.
--
-- FOR UPDATE SKIP LOCKED: baris yang sedang dikunci transaksi lain dilewati,
-- bukan ditunggu, jadi beberapa worker bisa berjalan bersamaan tanpa bentrok.
WITH due AS (
    SELECT id FROM listings
    WHERE active AND next_check_at <= now()
    ORDER BY next_check_at
    LIMIT sqlc.arg(batch_size)
    FOR UPDATE SKIP LOCKED
)
UPDATE listings l
SET next_check_at = now() + sqlc.arg(lease)::interval
FROM due
WHERE l.id = due.id
RETURNING l.id, l.place_id;

-- name: MarkCheckSucceeded :exec
UPDATE listings
SET last_checked_at      = now(),
    last_check_status    = 'ok',
    last_error           = NULL,
    consecutive_failures = 0,
    next_check_at        = now() + poll_interval
WHERE id = $1;

-- name: MarkCheckFailed :exec
-- Error sementara: coba lagi lebih cepat dengan backoff 15m, 30m, 1j, ...,
-- maksimal poll_interval. Error permanen (misalnya place tidak ditemukan):
-- langsung pakai poll_interval supaya kuota tidak terbuang.
-- Di sisi kanan SET, consecutive_failures masih bernilai lama.
UPDATE listings
SET last_checked_at      = now(),
    last_check_status    = 'error',
    last_error           = sqlc.arg(last_error)::text,
    consecutive_failures = consecutive_failures + 1,
    next_check_at        = now() + CASE
        WHEN sqlc.arg(permanent)::boolean THEN poll_interval
        ELSE LEAST(poll_interval, interval '15 minutes' * power(2, LEAST(consecutive_failures, 10)))
    END
WHERE id = sqlc.arg(id);

-- name: GetCheckTarget :one
-- Baseline listing beserta info pemiliknya yang dibutuhkan monitor.
SELECT b.*, (u.telegram_chat_id IS NOT NULL)::boolean AS has_telegram
FROM baselines b
JOIN listings l ON l.id = b.listing_id
JOIN users u ON u.id = l.user_id
WHERE b.listing_id = $1;
