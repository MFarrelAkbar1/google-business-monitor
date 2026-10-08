package places

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testKey = "test-key-123"

// newTestClient menjalankan server HTTP lokal (httptest) yang meniru Google,
// lalu membuat GoogleClient yang diarahkan ke server itu.
func newTestClient(t *testing.T, handler http.HandlerFunc) *GoogleClient {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c, err := NewGoogleClient(testKey, WithBaseURL(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestGetPlace_Success(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/places/ChIJabc_123-x" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("X-Goog-Api-Key"); got != testKey {
			t.Errorf("api key header = %q", got)
		}
		if got := r.Header.Get("X-Goog-FieldMask"); got != DetailsFieldMask {
			t.Errorf("field mask = %q", got)
		}
		if r.URL.Query().Has("key") {
			t.Error("api key must not be sent in query string")
		}
		if got := r.URL.Query().Get("languageCode"); got != "id" {
			t.Errorf("languageCode = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"id": "ChIJabc_123-x",
			"displayName": {"text": "Toko Maju Cabang Depok", "languageCode": "id"},
			"nationalPhoneNumber": "0812-3456-7890",
			"internationalPhoneNumber": "+62 812-3456-7890",
			"websiteUri": "https://tokomaju.co.id/",
			"formattedAddress": "Jl. Margonda No. 1, Depok",
			"businessStatus": "OPERATIONAL"
		}`))
	})

	got, err := c.GetPlace(context.Background(), "ChIJabc_123-x")
	if err != nil {
		t.Fatal(err)
	}
	want := Place{
		ID:                       "ChIJabc_123-x",
		DisplayName:              "Toko Maju Cabang Depok",
		NationalPhoneNumber:      "0812-3456-7890",
		InternationalPhoneNumber: "+62 812-3456-7890",
		WebsiteURI:               "https://tokomaju.co.id/",
		FormattedAddress:         "Jl. Margonda No. 1, Depok",
		BusinessStatus:           "OPERATIONAL",
	}
	if got != want {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
}

func TestGetPlace_MissingFieldsAreEmpty(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id": "abc", "displayName": {"text": "Warung"}}`))
	})
	got, err := c.GetPlace(context.Background(), "abc")
	if err != nil {
		t.Fatal(err)
	}
	if got.WebsiteURI != "" || got.NationalPhoneNumber != "" {
		t.Errorf("expected empty phone/website, got %+v", got)
	}
}

func TestGetPlace_NotFound(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error": {"code": 404, "message": "not found", "status": "NOT_FOUND"}}`))
	})
	_, err := c.GetPlace(context.Background(), "abc")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestGetPlace_APIError(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error": {"code": 429, "message": "quota exceeded", "status": "RESOURCE_EXHAUSTED"}}`))
	})
	_, err := c.GetPlace(context.Background(), "abc")

	// errors.As memeriksa apakah err (atau error yang dibungkusnya) bertipe *APIError.
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *APIError", err)
	}
	if apiErr.HTTPStatus != 429 || apiErr.Status != "RESOURCE_EXHAUSTED" {
		t.Errorf("unexpected api error: %+v", apiErr)
	}
	if strings.Contains(err.Error(), testKey) {
		t.Error("error message leaks api key")
	}
}

func TestGetPlace_InvalidPlaceIDNoRequest(t *testing.T) {
	called := false
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { called = true })

	for _, id := range []string{"", "abc/../x", "abc?key=1", "abc def"} {
		if _, err := c.GetPlace(context.Background(), id); !errors.Is(err, ErrInvalidPlaceID) {
			t.Errorf("GetPlace(%q) err = %v, want ErrInvalidPlaceID", id, err)
		}
	}
	if called {
		t.Error("server must not be called for invalid place id")
	}
}

func TestNewGoogleClient_EmptyKey(t *testing.T) {
	if _, err := NewGoogleClient(""); err == nil {
		t.Error("expected error for empty api key")
	}
}
