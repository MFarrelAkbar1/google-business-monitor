// Command worker menjalankan pengecekan berkala semua listing aktif
// (PLAN.md bagian 6): scheduler mengambil listing yang jatuh tempo, lalu
// monitor membandingkannya dengan baseline dan mencatat event + notifikasi.
//
// Pengiriman notifikasi (email, Telegram) dibuat di Task 4. Sampai saat itu,
// baris notifications hanya menumpuk dengan status pending.
//
// Migrasi tidak dijalankan otomatis; jalankan "task migrate" lebih dulu.
//
// Env:
//
//	DATABASE_URL           wajib
//	GOOGLE_PLACES_API_KEY  wajib
//	PLACES_RPS             call Google per detik untuk seluruh proses (default 1)
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/time/rate"

	"jagalisting/internal/monitor"
	"jagalisting/internal/places"
	"jagalisting/internal/scheduler"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(log); err != nil {
		log.Error("worker berhenti", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		return errors.New("env DATABASE_URL belum diisi (lihat .env.example)")
	}
	apiKey := os.Getenv("GOOGLE_PLACES_API_KEY")
	if apiKey == "" {
		return errors.New("env GOOGLE_PLACES_API_KEY belum diisi (lihat .env.example)")
	}
	rps := 1.0
	if s := os.Getenv("PLACES_RPS"); s != "" {
		v, err := strconv.ParseFloat(s, 64)
		if err != nil || v <= 0 {
			return fmt.Errorf("env PLACES_RPS tidak valid: %q", s)
		}
		rps = v
	}

	// ctx dibatalkan saat Ctrl+C atau SIGTERM (dikirim Docker saat container
	// dihentikan). Pengecekan yang sedang berjalan ikut berhenti.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return fmt.Errorf("connect db: %w", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping db: %w", err)
	}

	google, err := places.NewGoogleClient(apiKey)
	if err != nil {
		return err
	}
	// Satu limiter untuk seluruh proses: berapa pun goroutine yang mengecek,
	// total call ke Google tidak melebihi rps.
	client := places.WithRateLimit(google, rate.NewLimiter(rate.Limit(rps), 1))

	mon := monitor.New(pool, client, log)
	sched := scheduler.NewPostgres(pool, scheduler.Config{}, log)

	log.Info("worker berjalan", "places_rps", rps)
	err = sched.Run(ctx, mon.Handle)
	log.Info("worker berhenti dengan rapi")
	return err
}
