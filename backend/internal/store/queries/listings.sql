-- name: CreateUser :one
INSERT INTO users (email, password_hash)
VALUES ($1, $2)
RETURNING *;

-- name: CreateListing :one
INSERT INTO listings (user_id, place_id, label)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetListingForUser :one
-- Selalu filter dengan user_id pemilik (pencegahan IDOR).
SELECT * FROM listings
WHERE id = $1 AND user_id = $2;

-- name: UpsertBaseline :one
INSERT INTO baselines (
    listing_id, name, phone_e164, whatsapp_e164,
    website_domain, website_url, address, business_status
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (listing_id) DO UPDATE SET
    name            = EXCLUDED.name,
    phone_e164      = EXCLUDED.phone_e164,
    whatsapp_e164   = EXCLUDED.whatsapp_e164,
    website_domain  = EXCLUDED.website_domain,
    website_url     = EXCLUDED.website_url,
    address         = EXCLUDED.address,
    business_status = EXCLUDED.business_status,
    updated_at      = now()
RETURNING *;

-- name: GetBaseline :one
SELECT * FROM baselines
WHERE listing_id = $1;
