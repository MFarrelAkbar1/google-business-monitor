package scheduler

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

	"jagalisting/internal/store"
)

// Config mengatur Postgres scheduler. Nilai nol diganti default.
type Config struct {
	PollEvery   time.Duration // seberapa sering mencari listing yang jatuh tempo (default 1 menit)
	BatchSize   int32         // listing maksimal per pengambilan (default 10)
	Concurrency int           // pengecekan paralel maksimal (default 4)
	Lease       time.Duration // lama listing "dipinjam" satu worker (default 10 menit)
}

func (c Config) withDefaults() Config {
	if c.PollEvery <= 0 {
		c.PollEvery = time.Minute
	}
	if c.BatchSize <= 0 {
		c.BatchSize = 10
	}
	if c.Concurrency <= 0 {
		c.Concurrency = 4
	}
	if c.Lease <= 0 {
		c.Lease = 10 * time.Minute
	}
	return c
}

// maxErrorLen membatasi panjang last_error yang disimpan.
const maxErrorLen = 500

// Postgres adalah Scheduler yang memakai tabel listings sebagai antrean.
type Postgres struct {
	q   *store.Queries
	cfg Config
	log *slog.Logger
}

var _ Scheduler = (*Postgres)(nil)

// NewPostgres membuat scheduler. Aman menjalankan beberapa proses worker
// sekaligus ke database yang sama.
func NewPostgres(pool *pgxpool.Pool, cfg Config, log *slog.Logger) *Postgres {
	return &Postgres{q: store.New(pool), cfg: cfg.withDefaults(), log: log}
}

// Run memproses listing yang jatuh tempo setiap PollEvery sampai ctx dibatalkan.
// Error database hanya dicatat ke log, supaya gangguan sesaat tidak
// menghentikan worker.
func (s *Postgres) Run(ctx context.Context, h Handler) error {
	ticker := time.NewTicker(s.cfg.PollEvery)
	defer ticker.Stop()
	for {
		n, err := s.RunOnce(ctx, h)
		if err != nil && ctx.Err() == nil {
			s.log.Error("scheduler: claim gagal", "err", err)
		}
		// Batch penuh berarti mungkin masih ada antrean: langsung ambil lagi
		// tanpa menunggu tick berikutnya.
		if n == int(s.cfg.BatchSize) && ctx.Err() == nil {
			continue
		}
		// select menunggu salah satu channel siap: ctx selesai atau tick berikutnya.
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// RunOnce mengambil satu batch listing yang jatuh tempo, menjalankan h untuk
// masing-masing secara paralel, lalu menunggu semuanya selesai. Hasilnya
// adalah jumlah listing yang diambil.
func (s *Postgres) RunOnce(ctx context.Context, h Handler) (int, error) {
	rows, err := s.q.ClaimDueListings(ctx, store.ClaimDueListingsParams{
		Lease:     durationToInterval(s.cfg.Lease),
		BatchSize: s.cfg.BatchSize,
	})
	if err != nil {
		return 0, fmt.Errorf("scheduler: claim: %w", err)
	}

	// errgroup.Group dengan SetLimit membatasi jumlah goroutine yang berjalan
	// bersamaan. g.Go menunggu jika batasnya sudah tercapai.
	var g errgroup.Group
	g.SetLimit(s.cfg.Concurrency)
	for _, r := range rows {
		job := Job{ListingID: r.ID, PlaceID: r.PlaceID}
		g.Go(func() error {
			s.process(ctx, h, job)
			return nil
		})
	}
	_ = g.Wait() // process tidak pernah mengembalikan error
	return len(rows), nil
}

// process menjalankan satu Job lalu menjadwalkan pengecekan berikutnya.
func (s *Postgres) process(ctx context.Context, h Handler, job Job) {
	log := s.log.With("listing_id", job.ListingID)
	err := safeCall(ctx, h, job)

	// Worker sedang dimatikan: jangan catat apa pun. Lease akan habis dan
	// listing ini dicek ulang oleh worker berikutnya.
	if ctx.Err() != nil {
		return
	}

	if err == nil {
		if err := s.q.MarkCheckSucceeded(ctx, job.ListingID); err != nil {
			log.Error("scheduler: gagal menyimpan hasil", "err", err)
		}
		return
	}

	permanent := IsPermanent(err)
	log.Warn("scheduler: pengecekan gagal", "err", err, "permanent", permanent)
	msg := err.Error()
	if len(msg) > maxErrorLen {
		// Potong per byte bisa membelah karakter UTF-8, yang ditolak Postgres.
		msg = strings.ToValidUTF8(msg[:maxErrorLen], "")
	}
	if err := s.q.MarkCheckFailed(ctx, store.MarkCheckFailedParams{
		ID: job.ListingID, LastError: msg, Permanent: permanent,
	}); err != nil {
		log.Error("scheduler: gagal menyimpan error", "err", err)
	}
}

// safeCall menjalankan h dan mengubah panic menjadi error biasa, supaya satu
// listing yang bermasalah tidak mematikan seluruh worker.
func safeCall(ctx context.Context, h Handler, job Job) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = errors.New(fmt.Sprint("panic: ", r))
		}
	}()
	return h(ctx, job)
}

func durationToInterval(d time.Duration) pgtype.Interval {
	return pgtype.Interval{Microseconds: d.Microseconds(), Valid: true}
}
