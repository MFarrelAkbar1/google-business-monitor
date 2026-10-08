// Package places membungkus Google Places API (New).
//
// Kode lain hanya bergantung pada interface Client, bukan pada GoogleClient
// secara langsung. Dengan begitu test dan dev bisa memakai implementasi palsu
// tanpa memanggil API sungguhan (dan tanpa biaya).
package places

import (
	"context"
	"errors"
	"regexp"
)

// Place berisi field listing yang kita pantau.
//
// Sesuai Terms Google, nilai-nilai ini hanya boleh ada di memori selama satu
// pengecekan dan tidak boleh disimpan ke database (kecuali ID).
type Place struct {
	ID                       string
	DisplayName              string
	NationalPhoneNumber      string
	InternationalPhoneNumber string
	WebsiteURI               string
	FormattedAddress         string
	BusinessStatus           string // OPERATIONAL, CLOSED_TEMPORARILY, CLOSED_PERMANENTLY
}

// Client adalah kontrak untuk mengambil data listing.
//
// ctx membawa batas waktu dan sinyal pembatalan: kalau ctx dibatalkan
// (misalnya program dihentikan dengan Ctrl+C), request HTTP ikut dihentikan.
type Client interface {
	GetPlace(ctx context.Context, placeID string) (Place, error)
}

// ErrNotFound dikembalikan jika place_id tidak dikenal Google (HTTP 404).
var ErrNotFound = errors.New("places: place not found")

// ErrInvalidPlaceID dikembalikan sebelum request dikirim jika format place_id tidak valid.
var ErrInvalidPlaceID = errors.New("places: invalid place id")

// Place ID Google berisi huruf, angka, '-' dan '_'. Validasi ini mencegah
// karakter seperti '/' atau '?' disisipkan ke path URL request.
var placeIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,512}$`)

// ValidPlaceID melaporkan apakah s berformat place_id yang masuk akal.
func ValidPlaceID(s string) bool {
	return placeIDPattern.MatchString(s)
}
