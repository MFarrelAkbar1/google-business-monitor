// Package monitor menjalankan satu pengecekan listing:
// ambil data Google → bandingkan dengan baseline → simpan hasil dan event.
//
// Sesuai PLAN.md bagian 1, nilai dari Google (places.Place) hanya hidup di
// memori selama Check berjalan. Yang ditulis ke database hanya cocok/tidak
// cocok per field, event, dan baris notifikasi. Nilai Google juga tidak
// pernah ditulis ke log.
package monitor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"jagalisting/internal/diff"
	"jagalisting/internal/places"
	"jagalisting/internal/scheduler"
	"jagalisting/internal/store"
)

// SKUPlaceDetails adalah nama SKU di tabel api_usage_daily.
const SKUPlaceDetails = "place_details_enterprise"

// Channel notifikasi; nilainya sama dengan kolom notifications.channel.
const (
	ChannelEmail    = "email"
	ChannelTelegram = "telegram"
)

// ErrNoBaseline dikembalikan jika listing belum punya baseline. Tanpa
// baseline tidak ada yang bisa dibandingkan, jadi Google tidak dipanggil.
var ErrNoBaseline = errors.New("monitor: listing has no baseline")

// Monitor menyatukan sumber data (places.Client) dan database.
type Monitor struct {
	pool   *pgxpool.Pool
	places places.Client
	log    *slog.Logger
}

// New membuat Monitor. client sebaiknya sudah dibungkus places.WithRateLimit.
func New(pool *pgxpool.Pool, client places.Client, log *slog.Logger) *Monitor {
	return &Monitor{pool: pool, places: client, log: log}
}

// Summary merangkum hasil satu pengecekan, untuk log dan test.
type Summary struct {
	Mismatches int // field yang tidak cocok
	NewEvents  int // event baru (yang dibuatkan notifikasi)
	Resolved   int // event yang otomatis selesai karena field kembali cocok
}

// Handle memenuhi tipe scheduler.Handler.
func (m *Monitor) Handle(ctx context.Context, job scheduler.Job) error {
	s, err := m.Check(ctx, job.ListingID, job.PlaceID)
	if err != nil {
		return err
	}
	m.log.Info("monitor: listing dicek", "listing_id", job.ListingID,
		"mismatches", s.Mismatches, "new_events", s.NewEvents, "resolved", s.Resolved)
	return nil
}

// Check mengecek satu listing. Error yang dibungkus scheduler.Permanent
// berarti mencoba ulang sebentar lagi tidak akan membantu.
func (m *Monitor) Check(ctx context.Context, listingID int64, placeID string) (Summary, error) {
	q := store.New(m.pool)

	target, err := q.GetCheckTarget(ctx, listingID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Summary{}, scheduler.Permanent(ErrNoBaseline)
	}
	if err != nil {
		return Summary{}, fmt.Errorf("monitor: load baseline: %w", err)
	}

	place, err := m.places.GetPlace(ctx, placeID)
	if err := m.recordUsage(ctx, q, err); err != nil {
		return Summary{}, err
	}
	switch {
	case errors.Is(err, places.ErrNotFound), errors.Is(err, places.ErrInvalidPlaceID):
		// ⚠️ Place hilang bisa berarti listing dihapus atau digabung orang lain.
		// Untuk sekarang hanya tercatat di listings.last_error.
		return Summary{}, scheduler.Permanent(err)
	case err != nil:
		return Summary{}, err
	}

	results := diff.Compare(toDiffBaseline(target), place)

	var sum Summary
	// pgx.BeginFunc: commit jika fungsi mengembalikan nil, rollback jika error.
	// Event dan notifikasinya selalu tersimpan bersama, atau tidak sama sekali.
	err = pgx.BeginFunc(ctx, m.pool, func(tx pgx.Tx) error {
		sum = Summary{}
		qtx := q.WithTx(tx)
		for _, r := range results {
			if err := m.saveResult(ctx, qtx, listingID, target.HasTelegram, r, &sum); err != nil {
				return fmt.Errorf("monitor: save %s: %w", r.Field, err)
			}
		}
		return nil
	})
	if err != nil {
		return Summary{}, err
	}
	return sum, nil
}

func (m *Monitor) saveResult(ctx context.Context, q *store.Queries, listingID int64, hasTelegram bool, r diff.Result, sum *Summary) error {
	field := string(r.Field)
	if err := q.UpsertFieldCheck(ctx, store.UpsertFieldCheckParams{
		ListingID: listingID, Field: field, Matches: r.Matches,
	}); err != nil {
		return err
	}

	if r.Matches {
		n, err := q.AutoResolveChangeEvent(ctx, store.AutoResolveChangeEventParams{ListingID: listingID, Field: field})
		sum.Resolved += int(n)
		return err
	}

	sum.Mismatches++
	ev, err := q.OpenOrTouchChangeEvent(ctx, store.OpenOrTouchChangeEventParams{
		ListingID: listingID, Field: field, Kind: string(r.Kind), Severity: string(r.Severity),
	})
	if err != nil {
		return err
	}
	if !ev.Inserted {
		return nil // dedup: event lama masih open, tidak ada notifikasi baru
	}
	sum.NewEvents++

	channels := []string{ChannelEmail}
	if hasTelegram {
		channels = append(channels, ChannelTelegram)
	}
	for _, ch := range channels {
		if err := q.CreateNotification(ctx, store.CreateNotificationParams{ChangeEventID: ev.ID, Channel: ch}); err != nil {
			return err
		}
	}
	return nil
}

// recordUsage mencatat satu call ke Google di api_usage_daily. Call yang tidak
// pernah sampai ke Google (place_id tidak valid, ctx dibatalkan, ditahan rate
// limiter) tidak dihitung.
func (m *Monitor) recordUsage(ctx context.Context, q *store.Queries, callErr error) error {
	if errors.Is(callErr, places.ErrInvalidPlaceID) || ctx.Err() != nil {
		return nil
	}
	if err := q.IncrementAPIUsage(ctx, store.IncrementAPIUsageParams{
		Sku: SKUPlaceDetails, IsError: callErr != nil,
	}); err != nil {
		return fmt.Errorf("monitor: record api usage: %w", err)
	}
	return nil
}

func toDiffBaseline(t store.GetCheckTargetRow) diff.Baseline {
	return diff.Baseline{
		Name:           t.Name,
		PhoneE164:      t.PhoneE164,
		WhatsAppE164:   t.WhatsappE164,
		WebsiteDomain:  t.WebsiteDomain,
		WebsiteURL:     t.WebsiteUrl,
		Address:        t.Address,
		BusinessStatus: t.BusinessStatus,
	}
}
