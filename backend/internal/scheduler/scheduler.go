// Package scheduler menentukan kapan setiap listing dicek (PLAN.md bagian 6).
//
// Kode lain hanya bergantung pada interface Scheduler. Implementasinya
// sekarang memakai Postgres (FOR UPDATE SKIP LOCKED) dan nanti bisa diganti,
// misalnya dengan ForgeQ, tanpa mengubah package monitor.
package scheduler

import (
	"context"
	"errors"
)

// Job adalah satu pengecekan listing.
type Job struct {
	ListingID int64
	PlaceID   string
}

// Handler menjalankan satu Job. Error yang dikembalikan menentukan jadwal
// berikutnya: nil → poll_interval, error biasa → backoff, Permanent → poll_interval.
type Handler func(ctx context.Context, job Job) error

// Scheduler menjalankan Handler untuk setiap listing yang sudah waktunya dicek,
// sampai ctx dibatalkan.
type Scheduler interface {
	Run(ctx context.Context, h Handler) error
}

// permanentError menandai error yang tidak akan hilang walau dicoba lagi
// sebentar lagi, misalnya place_id yang sudah tidak ada di Google.
type permanentError struct{ err error }

func (e permanentError) Error() string { return e.err.Error() }

// Unwrap membuat errors.Is dan errors.As tetap bisa melihat error aslinya.
func (e permanentError) Unwrap() error { return e.err }

// Permanent membungkus err supaya scheduler tidak memakai backoff cepat.
// Mencoba ulang dalam 15 menit hanya membuang kuota API.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return permanentError{err}
}

// IsPermanent melaporkan apakah err (atau error yang dibungkusnya) dibuat lewat Permanent.
func IsPermanent(err error) bool {
	var p permanentError
	return errors.As(err, &p)
}
