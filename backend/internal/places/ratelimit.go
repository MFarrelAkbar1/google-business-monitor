package places

import (
	"context"

	"golang.org/x/time/rate"
)

// rateLimited membungkus Client lain dan menahan setiap call sampai token
// bucket mengizinkan. Ini pola "decorator": tipenya tetap Client, jadi kode
// pemanggil tidak tahu (dan tidak perlu tahu) ada pembatasan di dalamnya.
type rateLimited struct {
	next    Client
	limiter *rate.Limiter
}

// WithRateLimit mengembalikan Client yang semua call-nya melewati limiter.
// Pakai satu limiter untuk seluruh proses supaya batasnya berlaku global.
func WithRateLimit(c Client, limiter *rate.Limiter) Client {
	return rateLimited{next: c, limiter: limiter}
}

func (r rateLimited) GetPlace(ctx context.Context, placeID string) (Place, error) {
	// Wait berhenti lebih awal dengan error jika ctx dibatalkan.
	if err := r.limiter.Wait(ctx); err != nil {
		return Place{}, err
	}
	return r.next.GetPlace(ctx, placeID)
}
