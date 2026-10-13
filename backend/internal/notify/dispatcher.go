package notify

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/errgroup"

	"jagalisting/internal/diff"
	"jagalisting/internal/store"
)

// Sender mengirim satu Alert lewat satu channel.
type Sender interface {
	Send(ctx context.Context, a Alert) error
}

// Config mengatur Dispatcher. Nilai nol diganti default.
type Config struct {
	PollEvery   time.Duration // default 15 detik
	BatchSize   int32         // default 20
	Concurrency int           // default 4
	Lease       time.Duration // default 5 menit
	MaxAttempts int32         // default 8 (±2 jam dengan backoff 1m, 2m, 4m, ... maks 1 jam)
}

func (c Config) withDefaults() Config {
	if c.PollEvery <= 0 {
		c.PollEvery = 15 * time.Second
	}
	if c.BatchSize <= 0 {
		c.BatchSize = 20
	}
	if c.Concurrency <= 0 {
		c.Concurrency = 4
	}
	if c.Lease <= 0 {
		c.Lease = 5 * time.Minute
	}
	if c.MaxAttempts <= 0 {
		c.MaxAttempts = 8
	}
	return c
}

const maxErrorLen = 500

// Dispatcher mengambil notifikasi pending dari database dan mengirimnya.
//
// Jaminannya "at-least-once": jika pesan sudah terkirim tetapi proses mati
// sebelum status sent tersimpan, pesan dikirim ulang setelah lease habis.
// Lebih baik pemilik menerima peringatan dua kali daripada tidak sama sekali.
type Dispatcher struct {
	q        *store.Queries
	senders  map[string]Sender
	channels []string
	cfg      Config
	log      *slog.Logger
}

// NewDispatcher membuat Dispatcher. senders dipetakan per channel ("email",
// "telegram"). Notifikasi untuk channel yang tidak punya sender tidak diambil
// dan tetap pending sampai sender-nya dikonfigurasi.
func NewDispatcher(pool *pgxpool.Pool, senders map[string]Sender, cfg Config, log *slog.Logger) *Dispatcher {
	channels := make([]string, 0, len(senders))
	for ch := range senders {
		channels = append(channels, ch)
	}
	return &Dispatcher{q: store.New(pool), senders: senders, channels: channels, cfg: cfg.withDefaults(), log: log}
}

// Run mengirim notifikasi setiap PollEvery sampai ctx dibatalkan.
func (d *Dispatcher) Run(ctx context.Context) error {
	ticker := time.NewTicker(d.cfg.PollEvery)
	defer ticker.Stop()
	for {
		n, err := d.RunOnce(ctx)
		if err != nil && ctx.Err() == nil {
			d.log.Error("notify: claim gagal", "err", err)
		}
		if n == int(d.cfg.BatchSize) && ctx.Err() == nil {
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// RunOnce mengambil satu batch notifikasi, mengirimnya secara paralel, lalu
// menunggu semuanya selesai. Hasilnya adalah jumlah notifikasi yang diambil.
func (d *Dispatcher) RunOnce(ctx context.Context) (int, error) {
	if len(d.channels) == 0 {
		return 0, nil
	}
	rows, err := d.q.ClaimPendingNotifications(ctx, store.ClaimPendingNotificationsParams{
		Channels:  d.channels,
		BatchSize: d.cfg.BatchSize,
		Lease:     pgtype.Interval{Microseconds: d.cfg.Lease.Microseconds(), Valid: true},
	})
	if err != nil {
		return 0, fmt.Errorf("notify: claim: %w", err)
	}

	var g errgroup.Group
	g.SetLimit(d.cfg.Concurrency)
	for _, r := range rows {
		a := toAlert(r)
		g.Go(func() error {
			d.deliver(ctx, a)
			return nil
		})
	}
	_ = g.Wait()
	return len(rows), nil
}

func (d *Dispatcher) deliver(ctx context.Context, a Alert) {
	log := d.log.With("notification_id", a.NotificationID, "channel", a.Channel)
	err := d.senders[a.Channel].Send(ctx, a)

	// Sedang dimatikan: biarkan lease habis, lalu dicoba lagi.
	if ctx.Err() != nil {
		return
	}
	if err == nil {
		if err := d.q.MarkNotificationSent(ctx, a.NotificationID); err != nil {
			log.Error("notify: gagal menyimpan status sent", "err", err)
			return
		}
		log.Info("notify: terkirim")
		return
	}

	permanent := errors.Is(err, ErrPermanent)
	log.Warn("notify: gagal mengirim", "err", err, "permanent", permanent)
	msg := err.Error()
	if len(msg) > maxErrorLen {
		msg = strings.ToValidUTF8(msg[:maxErrorLen], "")
	}
	if err := d.q.MarkNotificationFailed(ctx, store.MarkNotificationFailedParams{
		ID: a.NotificationID, LastError: msg, Permanent: permanent, MaxAttempts: d.cfg.MaxAttempts,
	}); err != nil {
		log.Error("notify: gagal menyimpan error", "err", err)
	}
}

func toAlert(r store.ClaimPendingNotificationsRow) Alert {
	return Alert{
		NotificationID: r.ID,
		Channel:        r.Channel,
		Field:          r.Field,
		Kind:           r.Kind,
		Severity:       r.Severity,
		DetectedAt:     r.DetectedAt.Time,
		Label:          r.Label,
		PlaceID:        r.PlaceID,
		Email:          r.Email,
		TelegramChatID: r.TelegramChatID.Int64, // 0 jika NULL
		BaselineValue:  baselineValue(r),
	}
}

// baselineValue memilih kolom baseline yang sesuai dengan field event.
func baselineValue(r store.ClaimPendingNotificationsRow) string {
	switch diff.Field(r.Field) {
	case diff.FieldName:
		return r.BaselineName
	case diff.FieldPhone:
		return r.BaselinePhoneE164
	case diff.FieldWhatsApp:
		return r.BaselineWhatsappE164
	case diff.FieldWebsite:
		if r.BaselineWebsiteUrl != "" {
			return r.BaselineWebsiteUrl
		}
		return r.BaselineWebsiteDomain
	case diff.FieldAddress:
		return r.BaselineAddress
	case diff.FieldBusinessStatus:
		return r.BaselineBusinessStatus
	}
	return ""
}
