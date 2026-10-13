package notify_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"jagalisting/internal/notify"
	"jagalisting/internal/store"
	"jagalisting/internal/testdb"
)

// fakeSender mencatat Alert yang dikirim dan bisa diatur untuk gagal.
type fakeSender struct {
	mu   sync.Mutex
	sent []notify.Alert
	err  error
}

func (f *fakeSender) Send(_ context.Context, a notify.Alert) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.sent = append(f.sent, a)
	return nil
}

// seed membuat user, listing, baseline, satu event telepon, dan notifikasi
// untuk setiap channel yang diminta. Hasilnya id notifikasi per channel.
func seed(t *testing.T, pool *pgxpool.Pool, channels ...string) map[string]int64 {
	t.Helper()
	ctx := context.Background()
	q := store.New(pool)
	u, err := q.CreateUser(ctx, store.CreateUserParams{Email: "pemilik@example.com", PasswordHash: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "UPDATE users SET telegram_chat_id = 42 WHERE id = $1", u.ID); err != nil {
		t.Fatal(err)
	}
	l, err := q.CreateListing(ctx, store.CreateListingParams{UserID: u.ID, PlaceID: "ChIJtokomaju", Label: "Depok"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.UpsertBaseline(ctx, store.UpsertBaselineParams{
		ListingID: l.ID, PhoneE164: "+6281234567890", BusinessStatus: "OPERATIONAL",
	}); err != nil {
		t.Fatal(err)
	}
	ev, err := q.OpenOrTouchChangeEvent(ctx, store.OpenOrTouchChangeEventParams{
		ListingID: l.ID, Field: "phone", Kind: "mismatch", Severity: "critical",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, ch := range channels {
		if err := q.CreateNotification(ctx, store.CreateNotificationParams{ChangeEventID: ev.ID, Channel: ch}); err != nil {
			t.Fatal(err)
		}
	}
	ids := map[string]int64{}
	rows, err := pool.Query(ctx, "SELECT channel, id FROM notifications")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var ch string
		var id int64
		if err := rows.Scan(&ch, &id); err != nil {
			t.Fatal(err)
		}
		ids[ch] = id
	}
	return ids
}

type notifState struct {
	status    string
	attempts  int32
	lastError string
	untilNext time.Duration
	sent      bool
}

func state(t *testing.T, pool *pgxpool.Pool, id int64) notifState {
	t.Helper()
	var s notifState
	var secs float64
	err := pool.QueryRow(context.Background(), `
		SELECT status, attempts, coalesce(last_error, ''),
		       extract(epoch FROM next_attempt_at - now())::float8, sent_at IS NOT NULL
		FROM notifications WHERE id = $1`, id).Scan(&s.status, &s.attempts, &s.lastError, &secs, &s.sent)
	if err != nil {
		t.Fatal(err)
	}
	s.untilNext = time.Duration(secs * float64(time.Second))
	return s
}

func near(got, want time.Duration) bool {
	return math.Abs(float64(got-want)) < float64(20*time.Second)
}

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestDispatcherSends(t *testing.T) {
	pool := testdb.New(t)
	ids := seed(t, pool, "email", "telegram")
	email := &fakeSender{}
	// Tidak ada sender telegram: notifikasi telegram harus tetap pending.
	d := notify.NewDispatcher(pool, map[string]notify.Sender{"email": email}, notify.Config{}, discard)

	n, err := d.RunOnce(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("RunOnce = %d, %v; want 1", n, err)
	}
	if len(email.sent) != 1 {
		t.Fatalf("sent = %d, want 1", len(email.sent))
	}
	a := email.sent[0]
	if a.Email != "pemilik@example.com" || a.Field != "phone" || a.Severity != "critical" ||
		a.BaselineValue != "+6281234567890" || a.Label != "Depok" || a.TelegramChatID != 42 || a.DetectedAt.IsZero() {
		t.Errorf("alert = %+v", a)
	}
	if s := state(t, pool, ids["email"]); s.status != "sent" || !s.sent || s.attempts != 1 {
		t.Errorf("email notification = %+v", s)
	}
	if s := state(t, pool, ids["telegram"]); s.status != "pending" || s.attempts != 0 {
		t.Errorf("telegram notification = %+v, want untouched", s)
	}

	// Yang sudah terkirim tidak dikirim lagi.
	if n, _ := d.RunOnce(context.Background()); n != 0 {
		t.Errorf("second RunOnce took %d notifications, want 0", n)
	}
}

func TestDispatcherRetry(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	ids := seed(t, pool, "email", "telegram")
	email := &fakeSender{err: errors.New("smtp: connection refused")}
	telegram := &fakeSender{err: fmt.Errorf("%w: bot diblokir", notify.ErrPermanent)}
	d := notify.NewDispatcher(pool, map[string]notify.Sender{"email": email, "telegram": telegram},
		notify.Config{MaxAttempts: 3}, discard)

	if _, err := d.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}

	// Error permanen langsung berhenti.
	if s := state(t, pool, ids["telegram"]); s.status != "failed" || s.lastError == "" {
		t.Errorf("telegram = %+v, want failed", s)
	}

	// Error sementara: tetap pending dengan backoff 1m, lalu 2m, lalu menyerah.
	s := state(t, pool, ids["email"])
	if s.status != "pending" || s.attempts != 1 || !near(s.untilNext, time.Minute) {
		t.Errorf("email after 1st failure = %+v", s)
	}
	retry := func() {
		t.Helper()
		if _, err := pool.Exec(ctx, "UPDATE notifications SET next_attempt_at = now() WHERE id = $1", ids["email"]); err != nil {
			t.Fatal(err)
		}
		if _, err := d.RunOnce(ctx); err != nil {
			t.Fatal(err)
		}
	}
	retry()
	if s := state(t, pool, ids["email"]); s.status != "pending" || s.attempts != 2 || !near(s.untilNext, 2*time.Minute) {
		t.Errorf("email after 2nd failure = %+v", s)
	}
	retry()
	if s := state(t, pool, ids["email"]); s.status != "failed" || s.attempts != 3 {
		t.Errorf("email after 3rd failure = %+v, want failed (max attempts)", s)
	}
}

func TestDispatcherShutdownKeepsPending(t *testing.T) {
	pool := testdb.New(t)
	ids := seed(t, pool, "email")
	ctx, cancel := context.WithCancel(context.Background())
	sender := senderFunc(func(ctx context.Context, _ notify.Alert) error {
		cancel()
		return ctx.Err()
	})
	d := notify.NewDispatcher(pool, map[string]notify.Sender{"email": sender}, notify.Config{}, discard)
	if _, err := d.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	// Status tetap pending; dicoba lagi setelah lease (5 menit) habis.
	if s := state(t, pool, ids["email"]); s.status != "pending" || s.lastError != "" || !near(s.untilNext, 5*time.Minute) {
		t.Errorf("state = %+v", s)
	}
}

type senderFunc func(context.Context, notify.Alert) error

func (f senderFunc) Send(ctx context.Context, a notify.Alert) error { return f(ctx, a) }
