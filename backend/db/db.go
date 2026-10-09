// Package db berisi file migrasi SQL dan fungsi untuk menjalankannya.
package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"

	"github.com/pressly/goose/v3"
)

// Migrations berisi semua file migrasi. //go:embed membuat file SQL ikut
// tertanam di binary, jadi binary bisa menjalankan migrasi tanpa folder
// migrations di disk.
//
//go:embed migrations/*.sql
var Migrations embed.FS

// Migrate menjalankan semua migrasi yang belum diterapkan ("goose up").
func Migrate(ctx context.Context, conn *sql.DB) error {
	// fs.Sub membuat "migrations/" menjadi root, karena goose mencari file *.sql di root FS.
	dir, err := fs.Sub(Migrations, "migrations")
	if err != nil {
		return fmt.Errorf("db: open migrations dir: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, conn, dir)
	if err != nil {
		return fmt.Errorf("db: create migration provider: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("db: migrate up: %w", err)
	}
	return nil
}
