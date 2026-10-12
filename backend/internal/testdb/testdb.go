// Package testdb menyiapkan Postgres sungguhan untuk integration test.
//
// Setiap pemanggilan New menjalankan container Postgres baru lewat
// testcontainers, menerapkan semua migrasi, lalu mengembalikan pool koneksi.
// Test dilewati jika Docker tidak berjalan atau jika memakai "go test -short".
package testdb

import (
	"context"
	"database/sql"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // mendaftarkan driver "pgx" untuk database/sql (dipakai goose)
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"jagalisting/db"
)

// New menyiapkan satu database bersih yang sudah dimigrasi.
func New(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test dilewati dengan -short")
	}
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	ctr, err := postgres.Run(ctx, "postgres:17-alpine",
		postgres.WithDatabase("jagalisting_test"),
		postgres.BasicWaitStrategies(),
	)
	// CleanupContainer tetap dipanggil walau err != nil, supaya container yang
	// sempat dibuat tidak tertinggal.
	testcontainers.CleanupContainer(t, ctr)
	if err != nil {
		t.Fatal(err)
	}
	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}

	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	if err := db.Migrate(ctx, sqlDB); err != nil {
		t.Fatal(err)
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}
