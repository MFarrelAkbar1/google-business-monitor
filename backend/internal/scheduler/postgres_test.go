package scheduler_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"math"
	"sort"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"jagalisting/internal/scheduler"
	"jagalisting/internal/store"
	"jagalisting/internal/testdb"
)

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

// fixture membuat satu user dan beberapa listing yang semuanya jatuh tempo.
func fixture(t *testing.T, pool *pgxpool.Pool, placeIDs ...string) []int64 {
	t.Helper()
	ctx := context.Background()
	q := store.New(pool)
	u, err := q.CreateUser(ctx, store.CreateUserParams{Email: t.Name() + "@example.com", PasswordHash: "x"})
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for _, pid := range placeIDs {
		l, err := q.CreateListing(ctx, store.CreateListingParams{UserID: u.ID, PlaceID: pid})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, l.ID)
	}
	mustExec(t, pool, "UPDATE listings SET next_check_at = now() - interval '1 minute' WHERE user_id = $1", u.ID)
	return ids
}

func mustExec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatal(err)
	}
}

type listingState struct {
	untilNext time.Duration // next_check_at - now()
	status    pgtype.Text
	lastError pgtype.Text
	failures  int32
}

func state(t *testing.T, pool *pgxpool.Pool, id int64) listingState {
	t.Helper()
	var s listingState
	var secs float64
	err := pool.QueryRow(context.Background(), `
		SELECT extract(epoch FROM next_check_at - now())::float8, last_check_status, last_error, consecutive_failures
		FROM listings WHERE id = $1`, id).Scan(&secs, &s.status, &s.lastError, &s.failures)
	if err != nil {
		t.Fatal(err)
	}
	s.untilNext = time.Duration(secs * float64(time.Second))
	return s
}

// approx memeriksa durasi dengan toleransi 1 menit (waktu jalan test).
func approx(t *testing.T, name string, got, want time.Duration) {
	t.Helper()
	if math.Abs(float64(got-want)) > float64(time.Minute) {
		t.Errorf("%s = %v, want ±%v", name, got.Round(time.Second), want)
	}
}

func TestClaim(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	ids := fixture(t, pool, "ChIJdue1", "ChIJdue2", "ChIJlater", "ChIJinactive")
	mustExec(t, pool, "UPDATE listings SET next_check_at = now() + interval '1 hour' WHERE id = $1", ids[2])
	mustExec(t, pool, "UPDATE listings SET active = false WHERE id = $1", ids[3])

	// Transaksi pertama mengambil 1 listing dan belum commit, jadi barisnya
	// masih terkunci. Claim kedua harus melewatinya (SKIP LOCKED), bukan menunggu.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	lease := pgtype.Interval{Microseconds: (10 * time.Minute).Microseconds(), Valid: true}
	first, err := store.New(tx).ClaimDueListings(ctx, store.ClaimDueListingsParams{Lease: lease, BatchSize: 1})
	if err != nil || len(first) != 1 {
		t.Fatalf("first claim = %+v, %v", first, err)
	}

	second, err := store.New(pool).ClaimDueListings(ctx, store.ClaimDueListingsParams{Lease: lease, BatchSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 1 || second[0].ID == first[0].ID {
		t.Fatalf("second claim = %+v, want only the other due listing (first got %d)", second, first[0].ID)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	got := []int64{first[0].ID, second[0].ID}
	sort.Slice(got, func(i, j int) bool { return got[i] < got[j] })
	if got[0] != ids[0] || got[1] != ids[1] {
		t.Errorf("claimed %v, want %v (not-due and inactive listings skipped)", got, ids[:2])
	}

	// Setelah diambil, next_check_at maju sebesar lease, jadi tidak diambil lagi.
	approx(t, "lease", state(t, pool, ids[0]).untilNext, 10*time.Minute)
	again, err := store.New(pool).ClaimDueListings(ctx, store.ClaimDueListingsParams{Lease: lease, BatchSize: 10})
	if err != nil || len(again) != 0 {
		t.Errorf("claim again = %+v, %v; want nothing", again, err)
	}
}

func TestRunOnceReschedules(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	ids := fixture(t, pool, "ChIJok", "ChIJflaky", "ChIJgone", "ChIJpanic")
	ok, flaky, gone, panicky := ids[0], ids[1], ids[2], ids[3]

	handler := func(ctx context.Context, job scheduler.Job) error {
		switch job.ListingID {
		case flaky:
			return errors.New("503 dari Google")
		case gone:
			return scheduler.Permanent(errors.New("place not found"))
		case panicky:
			panic("bug")
		}
		return nil
	}
	s := scheduler.NewPostgres(pool, scheduler.Config{}, discard)

	n, err := s.RunOnce(ctx, handler)
	if err != nil || n != 4 {
		t.Fatalf("RunOnce = %d, %v; want 4", n, err)
	}

	st := state(t, pool, ok)
	if st.status.String != "ok" || st.failures != 0 {
		t.Errorf("ok listing: %+v", st)
	}
	approx(t, "sukses → poll_interval", st.untilNext, 12*time.Hour)

	st = state(t, pool, flaky)
	if st.status.String != "error" || st.failures != 1 || st.lastError.String != "503 dari Google" {
		t.Errorf("flaky listing: %+v", st)
	}
	approx(t, "gagal ke-1 → backoff", st.untilNext, 15*time.Minute)

	st = state(t, pool, gone)
	approx(t, "error permanen → poll_interval", st.untilNext, 12*time.Hour)

	st = state(t, pool, panicky)
	if st.status.String != "error" || st.lastError.String != "panic: bug" {
		t.Errorf("panic listing: %+v", st)
	}

	// Backoff naik dua kali lipat, tetapi tidak pernah melebihi poll_interval.
	for _, want := range []time.Duration{30 * time.Minute, time.Hour, 2 * time.Hour, 4 * time.Hour, 8 * time.Hour, 12 * time.Hour, 12 * time.Hour} {
		mustExec(t, pool, "UPDATE listings SET next_check_at = now() WHERE id = $1", flaky)
		if _, err := s.RunOnce(ctx, handler); err != nil {
			t.Fatal(err)
		}
		approx(t, "backoff", state(t, pool, flaky).untilNext, want)
	}

	// Sukses setelah gagal mengembalikan hitungan ke nol.
	mustExec(t, pool, "UPDATE listings SET next_check_at = now() WHERE id = $1", flaky)
	if _, err := s.RunOnce(ctx, func(context.Context, scheduler.Job) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if st := state(t, pool, flaky); st.failures != 0 || st.lastError.Valid {
		t.Errorf("after success: %+v", st)
	}
}

func TestShutdownLeavesLease(t *testing.T) {
	pool := testdb.New(t)
	ids := fixture(t, pool, "ChIJabc")

	// Worker dimatikan di tengah pengecekan: hasilnya tidak dicatat, dan
	// listing tetap memegang lease supaya nanti dicek ulang.
	ctx, cancel := context.WithCancel(context.Background())
	s := scheduler.NewPostgres(pool, scheduler.Config{}, discard)
	_, err := s.RunOnce(ctx, func(ctx context.Context, job scheduler.Job) error {
		cancel()
		return ctx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	st := state(t, pool, ids[0])
	if st.status.Valid || st.failures != 0 {
		t.Errorf("state = %+v, want untouched", st)
	}
	approx(t, "lease", st.untilNext, 10*time.Minute)
}

func TestPermanent(t *testing.T) {
	base := errors.New("x")
	wrapped := scheduler.Permanent(base)
	if !scheduler.IsPermanent(wrapped) || !errors.Is(wrapped, base) {
		t.Error("Permanent should be detectable and keep the original error")
	}
	if scheduler.IsPermanent(base) || scheduler.Permanent(nil) != nil {
		t.Error("unexpected IsPermanent / Permanent(nil) result")
	}
}
