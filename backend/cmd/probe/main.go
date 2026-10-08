// Command probe memanggil Place Details (New) untuk satu place_id dan mencetak
// field yang dipantau Jaga Listing. Dipakai untuk menguji seberapa cepat
// perubahan di Google Maps muncul di Places API.
//
// Setiap pemanggilan ditagih sebagai SKU Place Details Enterprise.
// Tidak ada data yang disimpan ke disk.
//
// Contoh:
//
//	go run ./cmd/probe -place-id ChIJ...
//	go run ./cmd/probe -place-id ChIJ... -every 30m
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"

	"jagalisting/internal/places"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	placeID := flag.String("place-id", "", "Place ID yang akan dicek (wajib)")
	every := flag.Duration("every", 0, "ulangi setiap durasi ini, misalnya 30m (0 = sekali saja). Minimal 5m.")
	lang := flag.String("lang", "id", "languageCode untuk hasil")
	region := flag.String("region", "ID", "regionCode untuk hasil")
	flag.Parse()

	if *placeID == "" {
		flag.Usage()
		return errors.New("-place-id wajib diisi")
	}
	if *every != 0 && *every < 5*time.Minute {
		return errors.New("-every minimal 5m supaya kuota tidak cepat habis")
	}

	apiKey := os.Getenv("GOOGLE_PLACES_API_KEY")
	if apiKey == "" {
		return errors.New("env GOOGLE_PLACES_API_KEY belum diisi (lihat .env.example)")
	}
	client, err := places.NewGoogleClient(apiKey, places.WithLocale(*lang, *region))
	if err != nil {
		return err
	}

	// ctx otomatis dibatalkan saat Ctrl+C ditekan, sehingga request yang sedang
	// berjalan dan loop -every berhenti dengan rapi.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	var prev *places.Place // hasil sebelumnya, hanya di memori
	for {
		p, err := fetch(ctx, client, *placeID)
		switch {
		case errors.Is(err, context.Canceled):
			return nil
		case err != nil:
			fmt.Fprintf(os.Stderr, "%s  gagal: %v\n", now(), err)
			if *every == 0 {
				return err
			}
		default:
			printPlace(p, prev)
			prev = &p
		}

		if *every == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(*every):
		}
	}
}

func fetch(ctx context.Context, c places.Client, placeID string) (places.Place, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	return c.GetPlace(ctx, placeID)
}

func now() string { return time.Now().Format("2006-01-02 15:04:05 MST") }

func printPlace(p places.Place, prev *places.Place) {
	fmt.Printf("%s  place_id=%s\n", now(), p.ID)
	fields := []struct {
		name string
		get  func(places.Place) string
	}{
		{"displayName", func(p places.Place) string { return p.DisplayName }},
		{"nationalPhoneNumber", func(p places.Place) string { return p.NationalPhoneNumber }},
		{"internationalPhoneNumber", func(p places.Place) string { return p.InternationalPhoneNumber }},
		{"websiteUri", func(p places.Place) string { return p.WebsiteURI }},
		{"formattedAddress", func(p places.Place) string { return p.FormattedAddress }},
		{"businessStatus", func(p places.Place) string { return p.BusinessStatus }},
	}
	for _, f := range fields {
		v := f.get(p)
		shown := v
		if shown == "" {
			shown = "(kosong)"
		}
		mark := ""
		if prev != nil && f.get(*prev) != v {
			mark = "   <-- BERUBAH"
		}
		fmt.Printf("  %-26s %s%s\n", f.name, shown, mark)
	}
	fmt.Println()
}
