package places

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const defaultBaseURL = "https://places.googleapis.com/v1"

// DetailsFieldMask adalah field minimal yang kita minta dari Place Details.
// Telepon dan website membuat request ini ditagih sebagai SKU Enterprise.
const DetailsFieldMask = "id,displayName,nationalPhoneNumber,internationalPhoneNumber,websiteUri,formattedAddress,businessStatus"

// GoogleClient memanggil Places API (New) sungguhan.
type GoogleClient struct {
	apiKey       string
	baseURL      string
	languageCode string
	regionCode   string
	http         *http.Client
}

// Pengecekan saat compile: kalau GoogleClient berhenti memenuhi interface
// Client, build langsung gagal di sini.
var _ Client = (*GoogleClient)(nil)

// Option mengubah konfigurasi GoogleClient ("functional options").
// Pola ini membuat konstruktor tetap sederhana, tetapi tetap bisa dikonfigurasi.
type Option func(*GoogleClient)

// WithBaseURL mengganti alamat API, dipakai test untuk mengarahkan ke server palsu.
func WithBaseURL(u string) Option { return func(c *GoogleClient) { c.baseURL = u } }

// WithHTTPClient mengganti http.Client bawaan.
func WithHTTPClient(h *http.Client) Option { return func(c *GoogleClient) { c.http = h } }

// WithLocale mengatur bahasa dan region hasil (memengaruhi format nama dan alamat).
func WithLocale(languageCode, regionCode string) Option {
	return func(c *GoogleClient) {
		c.languageCode = languageCode
		c.regionCode = regionCode
	}
}

// NewGoogleClient membuat client. apiKey wajib diisi dan hanya boleh berasal dari env var server.
func NewGoogleClient(apiKey string, opts ...Option) (*GoogleClient, error) {
	if apiKey == "" {
		return nil, errors.New("places: api key is empty")
	}
	c := &GoogleClient{
		apiKey:       apiKey,
		baseURL:      defaultBaseURL,
		languageCode: "id",
		regionCode:   "ID",
		http:         &http.Client{Timeout: 15 * time.Second},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

// APIError adalah error yang dikembalikan Google selain 404.
type APIError struct {
	HTTPStatus int
	Status     string // misalnya PERMISSION_DENIED, RESOURCE_EXHAUSTED
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("places: api error %d %s: %s", e.HTTPStatus, e.Status, e.Message)
}

// detailsResponse mencerminkan bentuk JSON respons Place Details (New).
// Field yang kosong di listing tidak muncul di JSON, sehingga bernilai "".
type detailsResponse struct {
	ID          string `json:"id"`
	DisplayName struct {
		Text string `json:"text"`
	} `json:"displayName"`
	NationalPhoneNumber      string `json:"nationalPhoneNumber"`
	InternationalPhoneNumber string `json:"internationalPhoneNumber"`
	WebsiteURI               string `json:"websiteUri"`
	FormattedAddress         string `json:"formattedAddress"`
	BusinessStatus           string `json:"businessStatus"`
}

type errorResponse struct {
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"error"`
}

// GetPlace mengambil detail satu listing.
func (c *GoogleClient) GetPlace(ctx context.Context, placeID string) (Place, error) {
	if !ValidPlaceID(placeID) {
		return Place{}, ErrInvalidPlaceID
	}

	q := url.Values{}
	if c.languageCode != "" {
		q.Set("languageCode", c.languageCode)
	}
	if c.regionCode != "" {
		q.Set("regionCode", c.regionCode)
	}
	endpoint := c.baseURL + "/places/" + url.PathEscape(placeID)
	if len(q) > 0 {
		endpoint += "?" + q.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Place{}, fmt.Errorf("places: build request: %w", err)
	}
	// API key dikirim lewat header, bukan query string, supaya tidak ikut
	// tercatat di log URL.
	req.Header.Set("X-Goog-Api-Key", c.apiKey)
	req.Header.Set("X-Goog-FieldMask", DetailsFieldMask)

	resp, err := c.http.Do(req)
	if err != nil {
		return Place{}, fmt.Errorf("places: request failed: %w", err)
	}
	defer resp.Body.Close() // defer: dijalankan otomatis saat fungsi selesai

	// Batasi ukuran body yang dibaca, untuk jaga-jaga.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Place{}, fmt.Errorf("places: read response: %w", err)
	}

	if resp.StatusCode == http.StatusNotFound {
		return Place{}, ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		apiErr := &APIError{HTTPStatus: resp.StatusCode}
		var er errorResponse
		if json.Unmarshal(body, &er) == nil {
			apiErr.Status = er.Error.Status
			apiErr.Message = er.Error.Message
		}
		return Place{}, apiErr
	}

	var d detailsResponse
	if err := json.Unmarshal(body, &d); err != nil {
		return Place{}, fmt.Errorf("places: decode response: %w", err)
	}
	return Place{
		ID:                       d.ID,
		DisplayName:              d.DisplayName.Text,
		NationalPhoneNumber:      d.NationalPhoneNumber,
		InternationalPhoneNumber: d.InternationalPhoneNumber,
		WebsiteURI:               d.WebsiteURI,
		FormattedAddress:         d.FormattedAddress,
		BusinessStatus:           d.BusinessStatus,
	}, nil
}
