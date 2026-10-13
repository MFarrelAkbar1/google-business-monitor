package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const defaultTelegramURL = "https://api.telegram.org"

// TelegramSender mengirim Alert lewat Telegram Bot API (sendMessage).
type TelegramSender struct {
	token   string
	baseURL string
	http    *http.Client
}

// NewTelegramSender membuat sender. token adalah token bot dari @BotFather dan
// hanya boleh berasal dari env var server. baseURL kosong = API Telegram asli
// (test mengisinya dengan server palsu).
func NewTelegramSender(token, baseURL string) (*TelegramSender, error) {
	if token == "" {
		return nil, errors.New("notify: telegram bot token is empty")
	}
	if baseURL == "" {
		baseURL = defaultTelegramURL
	}
	return &TelegramSender{
		token:   token,
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: 15 * time.Second},
	}, nil
}

type telegramRequest struct {
	ChatID             int64              `json:"chat_id"`
	Text               string             `json:"text"`
	ParseMode          string             `json:"parse_mode"`
	LinkPreviewOptions linkPreviewOptions `json:"link_preview_options"`
}

type linkPreviewOptions struct {
	IsDisabled bool `json:"is_disabled"`
}

type telegramResponse struct {
	OK          bool   `json:"ok"`
	ErrorCode   int    `json:"error_code"`
	Description string `json:"description"`
}

// Send memenuhi interface Sender.
func (s *TelegramSender) Send(ctx context.Context, a Alert) error {
	if a.TelegramChatID == 0 {
		return fmt.Errorf("%w: user has no telegram chat id", ErrPermanent)
	}
	body, err := json.Marshal(telegramRequest{
		ChatID:             a.TelegramChatID,
		Text:               TelegramText(a),
		ParseMode:          "HTML",
		LinkPreviewOptions: linkPreviewOptions{IsDisabled: true},
	})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/bot"+s.token+"/sendMessage", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("notify: telegram request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.http.Do(req)
	if err != nil {
		// Error dari http.Client berbentuk *url.Error yang memuat URL lengkap,
		// termasuk token bot. Ambil hanya error di dalamnya supaya token tidak
		// bocor ke log atau kolom last_error.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return fmt.Errorf("notify: telegram send: %w", err)
	}
	defer resp.Body.Close()

	var tr telegramResponse
	_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&tr)
	if resp.StatusCode == http.StatusOK && tr.OK {
		return nil
	}

	err = fmt.Errorf("notify: telegram %d: %s", resp.StatusCode, tr.Description)
	switch resp.StatusCode {
	case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound:
		// 400 chat tidak ditemukan, 403 bot diblokir pengguna: mencoba lagi
		// tidak akan berhasil. 401/404 berarti token salah.
		return fmt.Errorf("%w: %w", ErrPermanent, err)
	default:
		// 429 (terlalu banyak pesan) dan 5xx: coba lagi nanti.
		return err
	}
}

// TelegramText menyusun isi pesan Telegram dengan format HTML. Semua nilai
// di-escape. Nilai di dalam <code> bisa disalin dengan sekali ketuk di
// aplikasi Telegram.
func TelegramText(a Alert) string {
	e := html.EscapeString
	var b strings.Builder
	fmt.Fprintf(&b, "<b>[%s] %s %s</b>\n\n", e(a.SeverityName()), e(a.FieldName()), e(a.Problem()))
	fmt.Fprintf(&b, "Listing: <b>%s</b>\nTerdeteksi: %s\n", e(a.ListingName()), e(a.DetectedAtText()))
	if v := a.CorrectValue(); v != "" {
		fmt.Fprintf(&b, "\nData yang benar menurut Anda (ketuk untuk menyalin):\n<code>%s</code>\n", e(v))
	}
	fmt.Fprintf(&b, "\n<a href=\"%s\">Buka di Google Maps</a>\n\n", e(a.MapsURL()))
	b.WriteString("Kalau perubahan ini bukan dari Anda, perbaiki lewat Google Business Profile atau \"Sarankan perubahan\". ")
	b.WriteString("Kalau memang dari Anda, perbarui data listing di Jaga Listing.")
	return b.String()
}
