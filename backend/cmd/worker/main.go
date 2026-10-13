// Command worker menjalankan pengecekan berkala semua listing aktif
// (PLAN.md bagian 6): scheduler mengambil listing yang jatuh tempo, lalu
// monitor membandingkannya dengan baseline dan mencatat event + notifikasi.
//
// Di proses yang sama, dispatcher mengirim notifikasi pending lewat email dan
// (jika TELEGRAM_BOT_TOKEN diisi) Telegram.
//
// Migrasi tidak dijalankan otomatis; jalankan "task migrate" lebih dulu.
//
// Env:
//
//	DATABASE_URL           wajib
//	GOOGLE_PLACES_API_KEY  wajib
//	PLACES_RPS             call Google per detik untuk seluruh proses (default 1)
//	SMTP_HOST, SMTP_PORT   server email (default localhost:1025 = Mailpit)
//	SMTP_USERNAME, SMTP_PASSWORD  kosong = tanpa login
//	SMTP_FROM              default "Jaga Listing <noreply@jagalisting.local>"
//	TELEGRAM_BOT_TOKEN     opsional; kosong = notifikasi Telegram tetap pending
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
	"golang.org/x/sync/errgroup"
	"golang.org/x/time/rate"

	"jagalisting/internal/monitor"
	"jagalisting/internal/notify"
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

	senders, err := buildSenders(log)
	if err != nil {
		return err
	}

	mon := monitor.New(pool, client, log)
	sched := scheduler.NewPostgres(pool, scheduler.Config{}, log)
	disp := notify.NewDispatcher(pool, senders, notify.Config{}, log)

	log.Info("worker berjalan", "places_rps", rps)
	// Scheduler dan dispatcher berjalan bersamaan di dua goroutine.
	// g.Wait menunggu keduanya selesai (setelah ctx dibatalkan).
	var g errgroup.Group
	g.Go(func() error { return sched.Run(ctx, mon.Handle) })
	g.Go(func() error { return disp.Run(ctx) })
	err = g.Wait()
	log.Info("worker berhenti dengan rapi")
	return err
}

func buildSenders(log *slog.Logger) (map[string]notify.Sender, error) {
	port := 1025
	if s := os.Getenv("SMTP_PORT"); s != "" {
		p, err := strconv.Atoi(s)
		if err != nil {
			return nil, fmt.Errorf("env SMTP_PORT tidak valid: %q", s)
		}
		port = p
	}
	email, err := notify.NewEmailSender(notify.EmailConfig{
		Host:     envOr("SMTP_HOST", "localhost"),
		Port:     port,
		Username: os.Getenv("SMTP_USERNAME"),
		Password: os.Getenv("SMTP_PASSWORD"),
		From:     envOr("SMTP_FROM", "Jaga Listing <noreply@jagalisting.local>"),
	})
	if err != nil {
		return nil, err
	}
	senders := map[string]notify.Sender{monitor.ChannelEmail: email}

	if token := os.Getenv("TELEGRAM_BOT_TOKEN"); token != "" {
		tg, err := notify.NewTelegramSender(token, "")
		if err != nil {
			return nil, err
		}
		senders[monitor.ChannelTelegram] = tg
	} else {
		log.Warn("TELEGRAM_BOT_TOKEN kosong: notifikasi Telegram tidak dikirim")
	}
	return senders, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
