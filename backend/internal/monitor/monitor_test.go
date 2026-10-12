package monitor_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"jagalisting/internal/monitor"
	"jagalisting/internal/places"
	"jagalisting/internal/scheduler"
	"jagalisting/internal/store"
	"jagalisting/internal/testdb"
)

const placeID = "ChIJtokomaju"

// googleOK adalah data Google yang cocok dengan baseline di setup.
var googleOK = places.Place{
	ID:                       placeID,
	DisplayName:              "Toko Maju",
	InternationalPhoneNumber: "+62 812-3456-7890",
	WebsiteURI:               "https://www.tokomaju.co.id/",
	FormattedAddress:         "Jl. Margonda No. 1, Depok",
	BusinessStatus:           "OPERATIONAL",
}

func setup(t *testing.T, telegram bool) (*pgxpool.Pool, int64) {
	t.Helper()
	pool := testdb.New(t)
	ctx := context.Background()
	q := store.New(pool)
	u, err := q.CreateUser(ctx, store.CreateUserParams{Email: "pemilik@example.com", PasswordHash: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if telegram {
		if _, err := pool.Exec(ctx, "UPDATE users SET telegram_chat_id = 42 WHERE id = $1", u.ID); err != nil {
			t.Fatal(err)
		}
	}
	l, err := q.CreateListing(ctx, store.CreateListingParams{UserID: u.ID, PlaceID: placeID})
	if err != nil {
		t.Fatal(err)
	}
	_, err = q.UpsertBaseline(ctx, store.UpsertBaselineParams{
		ListingID:      l.ID,
		Name:           "Toko Maju",
		PhoneE164:      "+6281234567890",
		WebsiteDomain:  "tokomaju.co.id",
		Address:        "Jl. Margonda No. 1, Depok",
		BusinessStatus: "OPERATIONAL",
	})
	if err != nil {
		t.Fatal(err)
	}
	return pool, l.ID
}

func count(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func newMonitor(pool *pgxpool.Pool, c places.Client) *monitor.Monitor {
	return monitor.New(pool, c, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestCheckLifecycle(t *testing.T) {
	pool, listingID := setup(t, true)
	ctx := context.Background()
	fake := places.NewFakeClient(googleOK)
	m := newMonitor(pool, fake)

	check := func(want monitor.Summary) {
		t.Helper()
		got, err := m.Check(ctx, listingID, placeID)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("summary = %+v, want %+v", got, want)
		}
	}

	// 1. Semua cocok: 6 field tercatat, tidak ada event.
	check(monitor.Summary{})
	if n := count(t, pool, "SELECT count(*) FROM field_checks WHERE listing_id = $1 AND matches", listingID); n != 6 {
		t.Errorf("matching field_checks = %d, want 6", n)
	}

	// 2. Telepon diganti orang lain: event KRITIS + notifikasi email dan telegram.
	hijacked := googleOK
	hijacked.InternationalPhoneNumber = "+62 899-0000-1111"
	fake.Set(hijacked)
	check(monitor.Summary{Mismatches: 1, NewEvents: 1})
	if n := count(t, pool, `SELECT count(*) FROM change_events
		WHERE listing_id = $1 AND field = 'phone' AND severity = 'critical' AND status = 'open'`, listingID); n != 1 {
		t.Errorf("open critical phone events = %d, want 1", n)
	}
	if n := count(t, pool, "SELECT count(*) FROM notifications WHERE status = 'pending'"); n != 2 {
		t.Errorf("pending notifications = %d, want 2 (email + telegram)", n)
	}

	// 3. Masih berbeda di pengecekan berikutnya: dedup, tidak ada notifikasi baru.
	check(monitor.Summary{Mismatches: 1})
	if n := count(t, pool, "SELECT count(*) FROM notifications"); n != 2 {
		t.Errorf("notifications = %d, want still 2", n)
	}

	// 4. Nomor dikembalikan: event selesai otomatis.
	fake.Set(googleOK)
	check(monitor.Summary{Resolved: 1})
	if n := count(t, pool, "SELECT count(*) FROM change_events WHERE status = 'auto_resolved'"); n != 1 {
		t.Errorf("auto_resolved events = %d, want 1", n)
	}

	if n := count(t, pool, "SELECT calls FROM api_usage_daily WHERE sku = $1", monitor.SKUPlaceDetails); n != 4 {
		t.Errorf("api calls = %d, want 4", n)
	}
}

func TestCheckEmailOnlyWithoutTelegram(t *testing.T) {
	pool, listingID := setup(t, false)
	moved := googleOK
	moved.WebsiteURI = "https://tokomaju-promo.com"
	m := newMonitor(pool, places.NewFakeClient(moved))

	if _, err := m.Check(context.Background(), listingID, placeID); err != nil {
		t.Fatal(err)
	}
	if n := count(t, pool, "SELECT count(*) FROM notifications WHERE channel = 'email'"); n != 1 {
		t.Errorf("email notifications = %d, want 1", n)
	}
	if n := count(t, pool, "SELECT count(*) FROM notifications WHERE channel = 'telegram'"); n != 0 {
		t.Errorf("telegram notifications = %d, want 0", n)
	}
}

func TestCheckErrors(t *testing.T) {
	pool, listingID := setup(t, false)
	ctx := context.Background()
	fake := places.NewFakeClient(googleOK)
	m := newMonitor(pool, fake)

	t.Run("error sementara dari Google", func(t *testing.T) {
		fake.SetError(placeID, &places.APIError{HTTPStatus: 503, Status: "UNAVAILABLE"})
		defer fake.SetError(placeID, nil)
		_, err := m.Check(ctx, listingID, placeID)
		if err == nil || scheduler.IsPermanent(err) {
			t.Errorf("err = %v, want transient error", err)
		}
	})

	t.Run("place tidak ditemukan", func(t *testing.T) {
		_, err := m.Check(ctx, listingID, "ChIJhilang")
		if !errors.Is(err, places.ErrNotFound) || !scheduler.IsPermanent(err) {
			t.Errorf("err = %v, want permanent ErrNotFound", err)
		}
	})

	t.Run("listing tanpa baseline tidak memanggil Google", func(t *testing.T) {
		if _, err := pool.Exec(ctx, "DELETE FROM baselines WHERE listing_id = $1", listingID); err != nil {
			t.Fatal(err)
		}
		before := fake.Calls(placeID)
		_, err := m.Check(ctx, listingID, placeID)
		if !errors.Is(err, monitor.ErrNoBaseline) || !scheduler.IsPermanent(err) {
			t.Errorf("err = %v, want permanent ErrNoBaseline", err)
		}
		if fake.Calls(placeID) != before {
			t.Error("Google was called for a listing without baseline")
		}
	})

	// 503 dan 404 sama-sama dihitung sebagai call yang gagal.
	var calls, errs int
	err := pool.QueryRow(ctx, "SELECT calls, errors FROM api_usage_daily WHERE sku = $1", monitor.SKUPlaceDetails).Scan(&calls, &errs)
	if err != nil || calls != 2 || errs != 2 {
		t.Errorf("usage calls=%d errors=%d err=%v; want 2, 2", calls, errs, err)
	}
	if n := count(t, pool, "SELECT count(*) FROM field_checks"); n != 0 {
		t.Errorf("field_checks = %d, want 0 (failed checks write nothing)", n)
	}
}
