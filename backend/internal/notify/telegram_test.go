package notify

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testToken = "123456:RAHASIA"

func isPermanent(err error) bool { return errors.Is(err, ErrPermanent) }

func telegramAlert() Alert {
	a := sampleAlert()
	a.Channel = "telegram"
	a.TelegramChatID = 42
	a.Label = "Toko <Maju> & Co"
	return a
}

func TestTelegramSend(t *testing.T) {
	var got telegramRequest
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
	}))
	defer srv.Close()

	s, err := NewTelegramSender(testToken, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Send(context.Background(), telegramAlert()); err != nil {
		t.Fatal(err)
	}
	if path != "/bot"+testToken+"/sendMessage" {
		t.Errorf("path = %q", path)
	}
	if got.ChatID != 42 || got.ParseMode != "HTML" || !got.LinkPreviewOptions.IsDisabled {
		t.Errorf("request = %+v", got)
	}
	// Label pemilik di-escape; nilai baseline di dalam <code> supaya mudah disalin.
	for _, want := range []string{"Toko &lt;Maju&gt; &amp; Co", "<code>+6281234567890</code>"} {
		if !strings.Contains(got.Text, want) {
			t.Errorf("text missing %q:\n%s", want, got.Text)
		}
	}
}

func TestTelegramErrors(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		permanent bool
	}{
		{"bot diblokir pengguna", http.StatusForbidden, true},
		{"chat tidak ditemukan", http.StatusBadRequest, true},
		{"terlalu banyak pesan", http.StatusTooManyRequests, false},
		{"server Telegram error", http.StatusBadGateway, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(`{"ok":false,"description":"nope"}`))
			}))
			defer srv.Close()
			s, _ := NewTelegramSender(testToken, srv.URL)
			err := s.Send(context.Background(), telegramAlert())
			if err == nil || isPermanent(err) != tt.permanent {
				t.Errorf("err = %v, want permanent=%v", err, tt.permanent)
			}
		})
	}

	t.Run("user belum menghubungkan Telegram", func(t *testing.T) {
		s, _ := NewTelegramSender(testToken, "http://127.0.0.1:1")
		a := telegramAlert()
		a.TelegramChatID = 0
		if err := s.Send(context.Background(), a); !isPermanent(err) {
			t.Errorf("err = %v, want permanent", err)
		}
	})

	t.Run("error jaringan tidak membocorkan token", func(t *testing.T) {
		srv := httptest.NewServer(http.NotFoundHandler())
		srv.Close() // server sudah mati: koneksi pasti gagal
		s, _ := NewTelegramSender(testToken, srv.URL)
		err := s.Send(context.Background(), telegramAlert())
		if err == nil || isPermanent(err) {
			t.Fatalf("err = %v, want transient error", err)
		}
		if strings.Contains(err.Error(), "RAHASIA") {
			t.Errorf("error leaks bot token: %v", err)
		}
	})
}
