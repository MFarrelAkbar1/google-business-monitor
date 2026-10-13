// Package notify mengirim notifikasi dari tabel notifications (pola outbox)
// lewat email dan Telegram, dengan retry.
//
// Isi pesan hanya memuat data milik pemilik (label, baseline) dan jenis
// perubahannya. Nilai dari Google tidak pernah disimpan, jadi juga tidak ada
// di pesan. Pemilik diarahkan untuk membuka listing-nya di Google Maps.
package notify

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// ErrPermanent menandai error yang tidak akan hilang walau dicoba lagi,
// misalnya bot Telegram diblokir pengguna. Bungkus dengan
// fmt.Errorf("%w: %w", ErrPermanent, err); errors.Is tetap mengenali keduanya.
var ErrPermanent = errors.New("permanent")

// Alert adalah satu notifikasi yang siap dikirim.
type Alert struct {
	NotificationID int64
	Channel        string
	Field          string // name, phone, whatsapp, website, address, business_status
	Kind           string // mismatch, missing
	Severity       string // critical, medium, low
	DetectedAt     time.Time
	Label          string // nama cabang versi pemilik; bisa kosong
	PlaceID        string
	Email          string
	TelegramChatID int64 // 0 = belum terhubung
	// BaselineValue adalah data yang benar menurut pemilik untuk field ini.
	// Kosong berarti pemilik memang tidak mendaftarkan nilai apa pun.
	BaselineValue string
}

// wib adalah zona waktu Indonesia Barat. Indonesia tidak memakai daylight
// saving, jadi offset tetap +7 cukup dan tidak butuh database zona waktu.
var wib = time.FixedZone("WIB", 7*60*60)

var fieldNames = map[string]string{
	"name":            "Nama usaha",
	"phone":           "Nomor telepon",
	"whatsapp":        "Nomor WhatsApp di link website",
	"website":         "Website",
	"address":         "Alamat",
	"business_status": "Status usaha",
}

var severityNames = map[string]string{
	"critical": "KRITIS",
	"medium":   "SEDANG",
	"low":      "RENDAH",
}

var statusNames = map[string]string{
	"OPERATIONAL":        "Buka (OPERATIONAL)",
	"CLOSED_TEMPORARILY": "Tutup sementara (CLOSED_TEMPORARILY)",
	"CLOSED_PERMANENTLY": "Tutup permanen (CLOSED_PERMANENTLY)",
}

func lookup(m map[string]string, k string) string {
	if v, ok := m[k]; ok {
		return v
	}
	return k
}

// FieldName adalah nama field yang mudah dibaca, misalnya "Nomor telepon".
func (a Alert) FieldName() string { return lookup(fieldNames, a.Field) }

// SeverityName adalah tingkat bahaya dalam bahasa Indonesia, misalnya "KRITIS".
func (a Alert) SeverityName() string { return lookup(severityNames, a.Severity) }

// ListingName adalah label pemilik, atau place_id jika label kosong.
// Baris baru dibuang supaya label tidak bisa menyisipkan header email.
func (a Alert) ListingName() string {
	name := a.Label
	if strings.TrimSpace(name) == "" {
		name = a.PlaceID
	}
	return strings.Join(strings.Fields(name), " ")
}

// Problem menjelaskan apa yang terjadi, misalnya "tidak cocok dengan data Anda".
func (a Alert) Problem() string {
	if a.Kind == "missing" {
		return "hilang dari listing Google Maps"
	}
	if a.BaselineValue == "" {
		return "muncul di listing Google Maps, padahal Anda tidak mendaftarkannya"
	}
	return "tidak cocok dengan data Anda"
}

// CorrectValue adalah nilai yang bisa disalin pemilik saat mengajukan
// perbaikan ke Google. Kosong jika tidak ada.
func (a Alert) CorrectValue() string {
	if a.Field == "business_status" {
		return lookup(statusNames, a.BaselineValue)
	}
	return a.BaselineValue
}

// Subject adalah judul pesan.
func (a Alert) Subject() string {
	return fmt.Sprintf("[%s] %s %s – %s", a.SeverityName(), a.FieldName(), a.Problem(), a.ListingName())
}

// DetectedAtText adalah waktu deteksi dalam WIB.
func (a Alert) DetectedAtText() string {
	return a.DetectedAt.In(wib).Format("2 Jan 2006 15:04 MST")
}

// MapsURL membuka listing ini di Google Maps (format "Maps URLs" resmi:
// query wajib diisi, query_place_id menentukan listing yang tepat).
func (a Alert) MapsURL() string {
	v := url.Values{
		"api":            {"1"},
		"query":          {a.ListingName()},
		"query_place_id": {a.PlaceID},
	}
	return "https://www.google.com/maps/search/?" + v.Encode()
}
