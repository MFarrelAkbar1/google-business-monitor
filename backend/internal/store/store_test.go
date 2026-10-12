package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"jagalisting/internal/store"
	"jagalisting/internal/testdb"
)

// Integration test: menjalankan Postgres sungguhan di Docker (lihat package
// testdb), lalu menguji query sqlc.

func TestStore(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	q := store.New(pool)

	user, err := q.CreateUser(ctx, store.CreateUserParams{Email: "Pemilik@Example.com", PasswordHash: "x"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := q.CreateUser(ctx, store.CreateUserParams{Email: "lain@example.com", PasswordHash: "x"})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("email unik tanpa peduli huruf besar (citext)", func(t *testing.T) {
		_, err := q.CreateUser(ctx, store.CreateUserParams{Email: "pemilik@example.com", PasswordHash: "x"})
		if err == nil {
			t.Error("expected unique violation")
		}
	})

	listing, err := q.CreateListing(ctx, store.CreateListingParams{UserID: user.ID, PlaceID: "ChIJabc", Label: "Depok"})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("default listing", func(t *testing.T) {
		if want := int64(12 * time.Hour / time.Microsecond); listing.PollInterval.Microseconds != want {
			t.Errorf("poll_interval = %+v, want 12 jam", listing.PollInterval)
		}
		if !listing.Active || !listing.NextCheckAt.Valid {
			t.Errorf("unexpected defaults: %+v", listing)
		}
	})

	t.Run("listing milik user lain tidak terlihat", func(t *testing.T) {
		_, err := q.GetListingForUser(ctx, store.GetListingForUserParams{ID: listing.ID, UserID: other.ID})
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Errorf("err = %v, want pgx.ErrNoRows", err)
		}
	})

	t.Run("upsert baseline", func(t *testing.T) {
		_, err := q.UpsertBaseline(ctx, store.UpsertBaselineParams{
			ListingID: listing.ID, Name: "Toko Maju", PhoneE164: "+6281234567890",
			BusinessStatus: "OPERATIONAL",
		})
		if err != nil {
			t.Fatal(err)
		}
		_, err = q.UpsertBaseline(ctx, store.UpsertBaselineParams{
			ListingID: listing.ID, Name: "Toko Maju", PhoneE164: "+6289900001111",
			BusinessStatus: "OPERATIONAL",
		})
		if err != nil {
			t.Fatal(err)
		}
		got, err := q.GetBaseline(ctx, listing.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.PhoneE164 != "+6289900001111" || got.BusinessStatus != "OPERATIONAL" {
			t.Errorf("baseline not updated: %+v", got)
		}
	})

	t.Run("field check upsert", func(t *testing.T) {
		for _, m := range []bool{true, false} {
			err := q.UpsertFieldCheck(ctx, store.UpsertFieldCheckParams{ListingID: listing.ID, Field: "phone", Matches: m})
			if err != nil {
				t.Fatal(err)
			}
		}
		checks, err := q.ListFieldChecks(ctx, listing.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(checks) != 1 || checks[0].Matches {
			t.Errorf("checks = %+v, want one row with matches=false", checks)
		}
	})

	t.Run("dedup change event dan auto resolve", func(t *testing.T) {
		params := store.OpenOrTouchChangeEventParams{
			ListingID: listing.ID, Field: "phone", Kind: "mismatch", Severity: "critical",
		}
		first, err := q.OpenOrTouchChangeEvent(ctx, params)
		if err != nil {
			t.Fatal(err)
		}
		second, err := q.OpenOrTouchChangeEvent(ctx, params)
		if err != nil {
			t.Fatal(err)
		}
		if !first.Inserted || second.Inserted || first.ID != second.ID {
			t.Fatalf("first=%+v second=%+v, want one event inserted then touched", first, second)
		}

		// Notifikasi juga dedup per (event, channel).
		for range 2 {
			if err := q.CreateNotification(ctx, store.CreateNotificationParams{ChangeEventID: first.ID, Channel: "email"}); err != nil {
				t.Fatal(err)
			}
		}
		var n int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM notifications WHERE change_event_id = $1", first.ID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Errorf("notifications = %d, want 1", n)
		}

		resolved, err := q.AutoResolveChangeEvent(ctx, store.AutoResolveChangeEventParams{ListingID: listing.ID, Field: "phone"})
		if err != nil || resolved != 1 {
			t.Fatalf("AutoResolve = %d, %v; want 1", resolved, err)
		}
		open, err := q.ListOpenChangeEvents(ctx, listing.ID)
		if err != nil || len(open) != 0 {
			t.Fatalf("open events = %+v, %v; want none", open, err)
		}

		// Setelah resolved, mismatch berikutnya membuat event baru.
		third, err := q.OpenOrTouchChangeEvent(ctx, params)
		if err != nil {
			t.Fatal(err)
		}
		if !third.Inserted || third.ID == first.ID {
			t.Errorf("third=%+v, want new event", third)
		}
	})

	t.Run("CHECK constraint menolak nilai tidak dikenal", func(t *testing.T) {
		_, err := q.OpenOrTouchChangeEvent(ctx, store.OpenOrTouchChangeEventParams{
			ListingID: listing.ID, Field: "name", Kind: "mismatch", Severity: "urgent",
		})
		if err == nil {
			t.Error("expected check violation for severity 'urgent'")
		}
	})

	t.Run("api usage harian", func(t *testing.T) {
		for _, isErr := range []bool{false, false, true} {
			if err := q.IncrementAPIUsage(ctx, store.IncrementAPIUsageParams{Sku: "place_details_enterprise", IsError: isErr}); err != nil {
				t.Fatal(err)
			}
		}
		var calls, errs int
		err := pool.QueryRow(ctx,
			"SELECT calls, errors FROM api_usage_daily WHERE day = current_date AND sku = 'place_details_enterprise'",
		).Scan(&calls, &errs)
		if err != nil || calls != 3 || errs != 1 {
			t.Errorf("calls=%d errors=%d err=%v; want 3, 1", calls, errs, err)
		}
	})

	t.Run("hapus user menghapus datanya (cascade)", func(t *testing.T) {
		if _, err := pool.Exec(ctx, "DELETE FROM users WHERE id = $1", user.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := q.GetBaseline(ctx, listing.ID); !errors.Is(err, pgx.ErrNoRows) {
			t.Errorf("baseline still exists: err = %v", err)
		}
	})
}
