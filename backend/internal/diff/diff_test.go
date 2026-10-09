package diff

import (
	"testing"

	"jagalisting/internal/places"
)

// baseline dan googlePlace adalah listing yang 100% cocok. Tiap kasus test
// hanya mengubah satu atau dua field dari sini.
func baseline() Baseline {
	return Baseline{
		Name:          "Toko Maju Cabang Depok",
		PhoneE164:     "+6281234567890",
		WebsiteDomain: "tokomaju.co.id",
		WebsiteURL:    "https://tokomaju.co.id/",
		Address:       "Jl. Margonda No. 1, Depok",
	}
}

func googlePlace() places.Place {
	return places.Place{
		ID:                       "abc",
		DisplayName:              "Toko Maju Cabang Depok",
		NationalPhoneNumber:      "0812-3456-7890",
		InternationalPhoneNumber: "+62 812-3456-7890",
		WebsiteURI:               "https://tokomaju.co.id/",
		FormattedAddress:         "Jl. Margonda No. 1, Depok",
		BusinessStatus:           "OPERATIONAL",
	}
}

func byField(rs []Result) map[Field]Result {
	m := make(map[Field]Result, len(rs))
	for _, r := range rs {
		m[r.Field] = r
	}
	return m
}

// want adalah hasil yang diharapkan untuk field yang TIDAK cocok.
// Field lain yang tidak disebut harus cocok.
type want map[Field]Result

func TestCompare(t *testing.T) {
	tests := []struct {
		name   string
		base   func(*Baseline)
		google func(*places.Place)
		want   want
	}{
		{
			name: "semua cocok",
		},
		{
			name: "telepon lokal vs internasional dianggap sama",
			base: func(b *Baseline) { b.PhoneE164 = "0812 3456 7890" },
			google: func(p *places.Place) {
				p.InternationalPhoneNumber = ""
				p.NationalPhoneNumber = "+62812-3456-7890"
			},
		},
		{
			name:   "telepon diganti",
			google: func(p *places.Place) { p.InternationalPhoneNumber = "+62 899-0000-1111" },
			want:   want{FieldPhone: mismatch(FieldPhone, SeverityCritical)},
		},
		{
			name: "telepon muncul padahal baseline kosong",
			base: func(b *Baseline) { b.PhoneE164 = "" },
			want: want{FieldPhone: mismatch(FieldPhone, SeverityCritical)},
		},
		{
			name: "telepon hilang",
			google: func(p *places.Place) {
				p.InternationalPhoneNumber = ""
				p.NationalPhoneNumber = ""
			},
			want: want{FieldPhone: missing(FieldPhone)},
		},
		{
			name:   "telepon tidak bisa dibaca",
			google: func(p *places.Place) { p.InternationalPhoneNumber = "hubungi kami" },
			want:   want{FieldPhone: mismatch(FieldPhone, SeverityCritical)},
		},
		{
			name: "http/https/www/trailing slash dianggap sama",
			base: func(b *Baseline) {
				b.WebsiteDomain = "WWW.TokoMaju.co.id"
				b.WebsiteURL = "http://www.tokomaju.co.id"
			},
			google: func(p *places.Place) { p.WebsiteURI = "https://tokomaju.co.id/?utm_source=gmb" },
		},
		{
			name:   "domain .co.id menjadi .com",
			google: func(p *places.Place) { p.WebsiteURI = "https://tokomaju.com/" },
			want:   want{FieldWebsite: mismatch(FieldWebsite, SeverityCritical)},
		},
		{
			name:   "domain sama, path berbeda",
			google: func(p *places.Place) { p.WebsiteURI = "https://tokomaju.co.id/promo" },
			want:   want{FieldWebsite: mismatch(FieldWebsite, SeverityLow)},
		},
		{
			name:   "path berbeda tidak dicek jika baseline tanpa URL",
			base:   func(b *Baseline) { b.WebsiteURL = "" },
			google: func(p *places.Place) { p.WebsiteURI = "https://tokomaju.co.id/promo" },
		},
		{
			name:   "website hilang",
			google: func(p *places.Place) { p.WebsiteURI = "" },
			want:   want{FieldWebsite: missing(FieldWebsite)},
		},
		{
			name: "website muncul padahal baseline kosong",
			base: func(b *Baseline) {
				b.WebsiteDomain = ""
				b.WebsiteURL = ""
			},
			want: want{FieldWebsite: mismatch(FieldWebsite, SeverityCritical)},
		},
		{
			name: "domain baseline diambil dari URL jika kosong",
			base: func(b *Baseline) { b.WebsiteDomain = "" },
		},
		{
			name: "website diganti link wa.me orang lain",
			google: func(p *places.Place) {
				p.WebsiteURI = "https://wa.me/6289900001111"
			},
			want: want{
				FieldWhatsApp: mismatch(FieldWhatsApp, SeverityCritical),
				FieldWebsite:  mismatch(FieldWebsite, SeverityCritical),
			},
		},
		{
			name: "nomor wa.me sama dalam format berbeda",
			base: func(b *Baseline) {
				b.WhatsAppE164 = "+6281234567890"
				b.WebsiteDomain = "wa.me"
				b.WebsiteURL = "https://wa.me/6281234567890"
			},
			google: func(p *places.Place) { p.WebsiteURI = "https://api.whatsapp.com/send?phone=6281234567890" },
			want: want{
				// Domain berbeda (wa.me vs whatsapp.com), nomor tetap sama.
				FieldWebsite: mismatch(FieldWebsite, SeverityCritical),
			},
		},
		{
			name: "nomor wa.me berubah",
			base: func(b *Baseline) {
				b.WhatsAppE164 = "+6281234567890"
				b.WebsiteDomain = "wa.me"
				b.WebsiteURL = "https://wa.me/6281234567890"
			},
			google: func(p *places.Place) { p.WebsiteURI = "https://wa.me/6289900001111?text=Halo" },
			want: want{
				// Path wa.me adalah nomor; cukup dilaporkan lewat field whatsapp.
				FieldWhatsApp: mismatch(FieldWhatsApp, SeverityCritical),
			},
		},
		{
			name: "link WhatsApp hilang",
			base: func(b *Baseline) { b.WhatsAppE164 = "+6281234567890" },
			want: want{FieldWhatsApp: missing(FieldWhatsApp)},
		},
		{
			name:   "link WhatsApp dengan nomor rusak",
			google: func(p *places.Place) { p.WebsiteURI = "https://wa.me/12" },
			want: want{
				FieldWhatsApp: mismatch(FieldWhatsApp, SeverityCritical),
				FieldWebsite:  mismatch(FieldWebsite, SeverityCritical),
			},
		},
		{
			name: "nama beda huruf besar dan spasi dianggap sama",
			google: func(p *places.Place) {
				p.DisplayName = "  TOKO MAJU   cabang depok "
			},
		},
		{
			name:   "nama diganti",
			google: func(p *places.Place) { p.DisplayName = "Toko Maju Jaya" },
			want:   want{FieldName: mismatch(FieldName, SeverityMedium)},
		},
		{
			name:   "alamat diganti",
			google: func(p *places.Place) { p.FormattedAddress = "Jl. Raya Bogor No. 9" },
			want:   want{FieldAddress: mismatch(FieldAddress, SeverityMedium)},
		},
		{
			name:   "alamat hilang",
			google: func(p *places.Place) { p.FormattedAddress = "" },
			want:   want{FieldAddress: missing(FieldAddress)},
		},
		{
			name: "alamat tidak dipantau jika baseline kosong",
			base: func(b *Baseline) { b.Address = "" },
		},
		{
			name:   "tutup permanen",
			google: func(p *places.Place) { p.BusinessStatus = "CLOSED_PERMANENTLY" },
			want:   want{FieldBusinessStatus: mismatch(FieldBusinessStatus, SeverityMedium)},
		},
		{
			name:   "businessStatus kosong dari Google dianggap cocok",
			google: func(p *places.Place) { p.BusinessStatus = "" },
		},
		{
			name: "baseline tutup sementara",
			base: func(b *Baseline) { b.BusinessStatus = "CLOSED_TEMPORARILY" },
			google: func(p *places.Place) {
				p.BusinessStatus = "CLOSED_TEMPORARILY"
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, p := baseline(), googlePlace()
			if tt.base != nil {
				tt.base(&b)
			}
			if tt.google != nil {
				tt.google(&p)
			}

			results := Compare(b, p)
			if len(results) != 6 {
				t.Fatalf("got %d results, want 6", len(results))
			}
			got := byField(results)
			for _, f := range []Field{FieldName, FieldPhone, FieldWhatsApp, FieldWebsite, FieldAddress, FieldBusinessStatus} {
				exp, ok := tt.want[f]
				if !ok {
					exp = match(f)
				}
				if got[f] != exp {
					t.Errorf("%s: got %+v, want %+v", f, got[f], exp)
				}
			}
		})
	}
}
