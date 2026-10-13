package notify

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

func sampleAlert() Alert {
	return Alert{
		NotificationID: 1,
		Channel:        "email",
		Field:          "phone",
		Kind:           "mismatch",
		Severity:       "critical",
		DetectedAt:     time.Date(2026, 10, 12, 3, 4, 0, 0, time.UTC),
		Label:          "Toko Maju Depok",
		PlaceID:        "ChIJtokomaju",
		Email:          "pemilik@example.com",
		BaselineValue:  "+6281234567890",
	}
}

func TestAlertText(t *testing.T) {
	a := sampleAlert()
	if got, want := a.Subject(), "[KRITIS] Nomor telepon tidak cocok dengan data Anda – Toko Maju Depok"; got != want {
		t.Errorf("Subject = %q, want %q", got, want)
	}
	if got, want := a.DetectedAtText(), "12 Oct 2026 10:04 WIB"; got != want {
		t.Errorf("DetectedAtText = %q, want %q", got, want)
	}

	t.Run("label kosong memakai place_id, baris baru dibuang", func(t *testing.T) {
		a := sampleAlert()
		a.Label = ""
		if a.ListingName() != "ChIJtokomaju" {
			t.Errorf("ListingName = %q", a.ListingName())
		}
		a.Label = "Toko\r\nBcc: korban@example.com"
		if strings.ContainsAny(a.ListingName(), "\r\n") {
			t.Errorf("ListingName still contains newline: %q", a.ListingName())
		}
	})

	t.Run("jenis masalah", func(t *testing.T) {
		a := sampleAlert()
		a.Kind = "missing"
		if !strings.Contains(a.Problem(), "hilang") {
			t.Errorf("missing: %q", a.Problem())
		}
		a.Kind, a.BaselineValue = "mismatch", ""
		if !strings.Contains(a.Problem(), "tidak mendaftarkannya") {
			t.Errorf("appeared: %q", a.Problem())
		}
	})

	t.Run("status usaha diterjemahkan", func(t *testing.T) {
		a := sampleAlert()
		a.Field, a.BaselineValue = "business_status", "OPERATIONAL"
		if a.CorrectValue() != "Buka (OPERATIONAL)" {
			t.Errorf("CorrectValue = %q", a.CorrectValue())
		}
	})

	t.Run("link Google Maps", func(t *testing.T) {
		u, err := url.Parse(sampleAlert().MapsURL())
		if err != nil {
			t.Fatal(err)
		}
		q := u.Query()
		if u.Host != "www.google.com" || q.Get("api") != "1" || q.Get("query_place_id") != "ChIJtokomaju" || q.Get("query") != "Toko Maju Depok" {
			t.Errorf("MapsURL = %s", u)
		}
	})
}
