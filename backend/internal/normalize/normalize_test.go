package normalize

import (
	"errors"
	"testing"
)

// Test di file ini memakai pola "table-driven test": daftar kasus dalam slice,
// lalu satu loop menjalankan semuanya. t.Run membuat tiap kasus menjadi
// subtest bernama, sehingga yang gagal terlihat jelas.

func TestPhone(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "", want: ""},
		{in: "   ", want: ""},
		{in: "0812-3456-7890", want: "+6281234567890"},
		{in: "+62 812-3456-7890", want: "+6281234567890"},
		{in: "+6281234567890", want: "+6281234567890"},
		{in: "(021) 555-1234", want: "+62215551234"},
		{in: "62 812 3456 7890", want: "+6281234567890"},
		{in: "+1 650-253-0000", want: "+16502530000"},
		{in: "abc", wantErr: true},
		{in: "123", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := Phone(tt.in)
			if tt.wantErr {
				if !errors.Is(err, ErrInvalidPhone) {
					t.Fatalf("Phone(%q) err = %v, want ErrInvalidPhone", tt.in, err)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Errorf("Phone(%q) = %q, %v; want %q", tt.in, got, err, tt.want)
			}
		})
	}
}

func TestPhone_LocalAndInternationalAreEqual(t *testing.T) {
	a, _ := Phone("0812-3456-7890")
	b, _ := Phone("+62812 3456 7890")
	if a != b {
		t.Errorf("%q != %q", a, b)
	}
}

func TestParseWebsite(t *testing.T) {
	tests := []struct {
		in         string
		wantURL    string
		wantDomain string
	}{
		{"", "", ""},
		{"https://tokomaju.co.id/", "tokomaju.co.id", "tokomaju.co.id"},
		{"http://tokomaju.co.id", "tokomaju.co.id", "tokomaju.co.id"},
		{"https://www.tokomaju.co.id/", "tokomaju.co.id", "tokomaju.co.id"},
		{"tokomaju.co.id", "tokomaju.co.id", "tokomaju.co.id"},
		{"HTTPS://WWW.TokoMaju.CO.ID/Menu/", "tokomaju.co.id/menu", "tokomaju.co.id"},
		{"https://tokomaju.co.id/?utm_source=gmb#top", "tokomaju.co.id", "tokomaju.co.id"},
		{"https://shop.tokomaju.co.id/cabang", "shop.tokomaju.co.id/cabang", "tokomaju.co.id"},
		{"https://tokomaju.com", "tokomaju.com", "tokomaju.com"},
		{"https://tokomaju.blogspot.com", "tokomaju.blogspot.com", "tokomaju.blogspot.com"},
		{"https://wa.me/6281234567890", "wa.me/6281234567890", "wa.me"},
		{"http://tokomaju.co.id:8080/x", "tokomaju.co.id/x", "tokomaju.co.id"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseWebsite(tt.in)
			if err != nil {
				t.Fatal(err)
			}
			if got.URL != tt.wantURL || got.Domain != tt.wantDomain {
				t.Errorf("ParseWebsite(%q) = %+v; want URL=%q Domain=%q", tt.in, got, tt.wantURL, tt.wantDomain)
			}
		})
	}
}

func TestParseWebsite_CoIDvsCom(t *testing.T) {
	a, _ := ParseWebsite("https://tokomaju.co.id")
	b, _ := ParseWebsite("https://tokomaju.com")
	if a.Domain == b.Domain {
		t.Errorf("domains must differ: %q", a.Domain)
	}
}

func TestParseWebsite_Invalid(t *testing.T) {
	for _, in := range []string{"https://", "http://:80/x", "%%%"} {
		if _, err := ParseWebsite(in); !errors.Is(err, ErrInvalidWebsite) {
			t.Errorf("ParseWebsite(%q) err = %v, want ErrInvalidWebsite", in, err)
		}
	}
}

func TestDomain(t *testing.T) {
	if got := Domain(" WWW.TokoMaju.co.id. "); got != "tokomaju.co.id" {
		t.Errorf("Domain = %q", got)
	}
}

func TestWhatsApp(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "", want: ""},
		{in: "https://tokomaju.co.id", want: ""},
		{in: "https://wa.me/6281234567890", want: "+6281234567890"},
		{in: "wa.me/6281234567890?text=Halo", want: "+6281234567890"},
		{in: "https://wa.me/+62-812-3456-7890", want: "+6281234567890"},
		{in: "https://wa.me/081234567890", want: "+6281234567890"},
		{in: "https://api.whatsapp.com/send?phone=6281234567890&text=Halo", want: "+6281234567890"},
		{in: "https://api.whatsapp.com/send/?phone=6281234567890", want: "+6281234567890"},
		{in: "https://web.whatsapp.com/send?phone=6281234567890", want: "+6281234567890"},
		{in: "https://www.whatsapp.com/", want: ""},
		{in: "https://wa.me/", wantErr: true},
		{in: "https://api.whatsapp.com/send?text=Halo", wantErr: true},
		{in: "https://wa.me/12", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := WhatsApp(tt.in)
			if tt.wantErr {
				if !errors.Is(err, ErrInvalidPhone) {
					t.Fatalf("WhatsApp(%q) err = %v, want ErrInvalidPhone", tt.in, err)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Errorf("WhatsApp(%q) = %q, %v; want %q", tt.in, got, err, tt.want)
			}
		})
	}
}

func TestText(t *testing.T) {
	tests := map[string]string{
		"":                             "",
		"  Toko   Maju\tCabang Depok\n": "toko maju cabang depok",
		"Jl. Margonda No. 1, DEPOK":    "jl. margonda no. 1, depok",
	}
	for in, want := range tests {
		if got := Text(in); got != want {
			t.Errorf("Text(%q) = %q, want %q", in, got, want)
		}
	}
}
