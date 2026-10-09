// Package diff membandingkan data listing dari Google dengan baseline milik
// pemilik dan menentukan tingkat bahayanya (PLAN.md bagian 4).
//
// Hasilnya hanya "cocok / tidak cocok" per field, tanpa nilai Google, sehingga
// aman untuk disimpan ke database.
package diff

import (
	"jagalisting/internal/normalize"
	"jagalisting/internal/places"
)

// Field adalah nama field yang dipantau. Nilainya sama dengan kolom
// field_checks.field dan change_events.field di database.
type Field string

const (
	FieldName           Field = "name"
	FieldPhone          Field = "phone"
	FieldWhatsApp       Field = "whatsapp"
	FieldWebsite        Field = "website"
	FieldAddress        Field = "address"
	FieldBusinessStatus Field = "business_status"
)

// Kind adalah jenis ketidakcocokan.
type Kind string

const (
	KindMismatch Kind = "mismatch" // nilai berbeda, atau muncul padahal baseline kosong
	KindMissing  Kind = "missing"  // baseline ada, tetapi di Google kosong
)

// Severity adalah tingkat bahaya ketidakcocokan.
type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityMedium   Severity = "medium"
	SeverityLow      Severity = "low"
)

// DefaultBusinessStatus dipakai jika baseline tidak mengisi business_status.
const DefaultBusinessStatus = "OPERATIONAL"

// Baseline adalah data yang benar menurut pemilik.
// String kosong berarti "pemilik tidak punya / tidak mengisi".
type Baseline struct {
	Name           string
	PhoneE164      string
	WhatsAppE164   string
	WebsiteDomain  string
	WebsiteURL     string // opsional; jika diisi, perubahan path ikut dideteksi
	Address        string
	BusinessStatus string
}

// Result adalah hasil perbandingan satu field.
// Kind dan Severity hanya diisi jika Matches == false.
type Result struct {
	Field    Field
	Matches  bool
	Kind     Kind
	Severity Severity
}

// Compare membandingkan satu Place dengan baseline dan selalu mengembalikan
// satu Result untuk setiap field, dengan urutan tetap.
func Compare(b Baseline, p places.Place) []Result {
	return []Result{
		compareName(b, p),
		comparePhone(b, p),
		compareWhatsApp(b, p),
		compareWebsite(b, p),
		compareAddress(b, p),
		compareBusinessStatus(b, p),
	}
}

func match(f Field) Result { return Result{Field: f, Matches: true} }

func mismatch(f Field, s Severity) Result {
	return Result{Field: f, Kind: KindMismatch, Severity: s}
}

func missing(f Field) Result {
	return Result{Field: f, Kind: KindMissing, Severity: SeverityMedium}
}

// compareContact dipakai untuk telepon dan WhatsApp, yang aturannya sama:
// nomor beda atau nomor baru muncul → KRITIS; nomor hilang → SEDANG.
// gotErr != nil berarti Google punya nilai yang tidak bisa dibaca sebagai
// nomor; itu jelas bukan nomor pemilik, jadi dianggap mismatch.
func compareContact(f Field, base, got string, gotErr error) Result {
	switch {
	case gotErr != nil:
		return mismatch(f, SeverityCritical)
	case base == "" && got == "":
		return match(f)
	case got == "":
		return missing(f)
	case got != base:
		return mismatch(f, SeverityCritical)
	default:
		return match(f)
	}
}

func comparePhone(b Baseline, p places.Place) Result {
	raw := p.InternationalPhoneNumber
	if raw == "" {
		raw = p.NationalPhoneNumber
	}
	got, err := normalize.Phone(raw)
	return compareContact(FieldPhone, normalizeBasePhone(b.PhoneE164), got, err)
}

func compareWhatsApp(b Baseline, p places.Place) Result {
	got, err := normalize.WhatsApp(p.WebsiteURI)
	return compareContact(FieldWhatsApp, normalizeBasePhone(b.WhatsAppE164), got, err)
}

// normalizeBasePhone menormalkan ulang nomor baseline. Seharusnya sudah E.164,
// tetapi ini menjaga perbandingan tetap benar jika ada data lama yang belum baku.
func normalizeBasePhone(s string) string {
	if n, err := normalize.Phone(s); err == nil {
		return n
	}
	return s
}

func compareWebsite(b Baseline, p places.Place) Result {
	got, err := normalize.ParseWebsite(p.WebsiteURI)
	if err != nil {
		return mismatch(FieldWebsite, SeverityCritical)
	}

	base, _ := normalize.ParseWebsite(b.WebsiteURL)
	baseDomain := normalize.Domain(b.WebsiteDomain)
	if baseDomain == "" {
		baseDomain = base.Domain
	}

	switch {
	case baseDomain == "" && got.Domain == "":
		return match(FieldWebsite)
	case got.Domain == "":
		return missing(FieldWebsite)
	case got.Domain != baseDomain:
		// Termasuk website yang muncul padahal pemilik tidak punya website.
		return mismatch(FieldWebsite, SeverityCritical)
	case base.URL != "" && got.URL != base.URL && !isWhatsAppLink(p.WebsiteURI):
		// Untuk link WhatsApp, path-nya adalah nomor; perubahannya sudah
		// ditangani field whatsapp dengan severity KRITIS.
		return mismatch(FieldWebsite, SeverityLow)
	default:
		return match(FieldWebsite)
	}
}

func isWhatsAppLink(raw string) bool {
	n, err := normalize.WhatsApp(raw)
	return n != "" || err != nil
}

// compareText dipakai untuk nama dan alamat. Baseline kosong berarti field ini
// tidak dipantau.
func compareText(f Field, base, got string) Result {
	base, got = normalize.Text(base), normalize.Text(got)
	switch {
	case base == "":
		return match(f)
	case got == "":
		return missing(f)
	case got != base:
		return mismatch(f, SeverityMedium)
	default:
		return match(f)
	}
}

func compareName(b Baseline, p places.Place) Result {
	return compareText(FieldName, b.Name, p.DisplayName)
}

func compareAddress(b Baseline, p places.Place) Result {
	return compareText(FieldAddress, b.Address, p.FormattedAddress)
}

func compareBusinessStatus(b Baseline, p places.Place) Result {
	base := b.BusinessStatus
	if base == "" {
		base = DefaultBusinessStatus
	}
	// ⚠️ Google tidak selalu mengirim businessStatus. Nilai kosong dianggap
	// "tidak diketahui", bukan perubahan, supaya tidak ada alarm palsu.
	if p.BusinessStatus == "" || p.BusinessStatus == base {
		return match(FieldBusinessStatus)
	}
	return mismatch(FieldBusinessStatus, SeverityMedium)
}
