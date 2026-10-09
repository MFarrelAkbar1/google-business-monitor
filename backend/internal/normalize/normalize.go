// Package normalize mengubah nilai listing ke bentuk baku sebelum dibandingkan,
// supaya beda format (misalnya "0812-3456-7890" vs "+62 812 3456 7890") tidak
// dianggap sebagai perubahan.
//
// Semua fungsi di sini murni: tidak ada I/O, hasilnya hanya bergantung pada input.
package normalize

import (
	"errors"
	"net/url"
	"strings"

	"github.com/nyaruka/phonenumbers"
	"golang.org/x/net/publicsuffix"
)

// DefaultRegion dipakai untuk nomor tanpa kode negara, misalnya "0812...".
const DefaultRegion = "ID"

var (
	ErrInvalidPhone   = errors.New("normalize: invalid phone number")
	ErrInvalidWebsite = errors.New("normalize: invalid website url")
)

// Phone mengubah nomor telepon ke format E.164, misalnya "+6281234567890".
// String kosong menghasilkan "" tanpa error (artinya "tidak ada nomor").
func Phone(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	num, err := phonenumbers.Parse(raw, DefaultRegion)
	if err != nil {
		return "", ErrInvalidPhone
	}
	// IsPossibleNumber hanya mengecek panjang nomor. IsValidNumber lebih ketat
	// (mengecek rentang nomor operator), tetapi bisa menolak nomor baru yang
	// belum ada di metadata library.
	if !phonenumbers.IsPossibleNumber(num) {
		return "", ErrInvalidPhone
	}
	return phonenumbers.Format(num, phonenumbers.E164), nil
}

// Website adalah URL website dalam bentuk baku.
type Website struct {
	// URL adalah host + path: huruf kecil, tanpa skema, tanpa "www.", tanpa
	// trailing slash, tanpa query dan fragment. Contoh: "tokomaju.co.id/menu".
	// Query dibuang karena Google sering menambahkan parameter seperti
	// ?utm_source=gmb yang bukan perubahan sungguhan.
	URL string
	// Domain adalah eTLD+1, misalnya "tokomaju.co.id" untuk "shop.tokomaju.co.id".
	Domain string
}

// ParseWebsite menormalkan URL website. String kosong menghasilkan Website{}
// tanpa error.
func ParseWebsite(raw string) (Website, error) {
	u, err := parseURL(raw)
	if err != nil || u == nil {
		return Website{}, err
	}
	host := cleanHost(u.Hostname())
	path := strings.TrimRight(strings.ToLower(u.Path), "/")

	// eTLD+1 memakai Public Suffix List, sehingga "co.id" dikenali sebagai
	// satu suffix: domain "a.tokomaju.co.id" adalah "tokomaju.co.id", bukan "co.id".
	domain, err := publicsuffix.EffectiveTLDPlusOne(host)
	if err != nil {
		// Misalnya host berupa IP atau "localhost": pakai host apa adanya.
		domain = host
	}
	return Website{URL: host + path, Domain: domain}, nil
}

// Domain menormalkan nama domain yang diketik pemilik, misalnya "WWW.TokoMaju.co.id".
func Domain(raw string) string {
	return cleanHost(strings.TrimSpace(raw))
}

// WhatsApp mengambil nomor WhatsApp (E.164) dari link wa.me atau
// api.whatsapp.com/send?phone=...
//
// Hasilnya:
//   - "", nil jika raw bukan link WhatsApp (atau kosong),
//   - nomor, nil jika link WhatsApp dengan nomor valid,
//   - "", ErrInvalidPhone jika link WhatsApp tetapi nomornya tidak valid.
func WhatsApp(raw string) (string, error) {
	u, err := parseURL(raw)
	if err != nil || u == nil {
		return "", nil
	}

	var number string
	switch cleanHost(u.Hostname()) {
	case "wa.me":
		// wa.me/6281234567890 → segmen pertama path.
		number, _, _ = strings.Cut(strings.Trim(u.Path, "/"), "/")
	case "api.whatsapp.com", "whatsapp.com", "web.whatsapp.com":
		if strings.Trim(u.Path, "/") != "send" {
			return "", nil
		}
		number = u.Query().Get("phone")
	default:
		return "", nil
	}

	digits := onlyDigits(number)
	if digits == "" {
		return "", ErrInvalidPhone
	}
	// Format resmi wa.me memakai kode negara tanpa "+". Sebagian UMKM menulis
	// "wa.me/0812...", jadi nomor berawalan 0 diperlakukan sebagai nomor lokal.
	if !strings.HasPrefix(digits, "0") {
		digits = "+" + digits
	}
	return Phone(digits)
}

// Text merapikan teks bebas (nama, alamat): trim, spasi ganda menjadi satu,
// dan huruf kecil supaya perbandingan tidak peka huruf besar/kecil.
func Text(s string) string {
	// strings.Fields memecah berdasarkan whitespace apa pun (spasi, tab, newline).
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// parseURL mengembalikan nil, nil untuk string kosong.
func parseURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	// Tanpa skema, url.Parse menganggap "tokomaju.co.id" sebagai path, bukan host.
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return nil, ErrInvalidWebsite
	}
	return u, nil
}

func cleanHost(h string) string {
	h = strings.ToLower(strings.TrimSuffix(h, "."))
	return strings.TrimPrefix(h, "www.")
}

func onlyDigits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}
