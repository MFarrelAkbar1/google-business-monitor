package places

import (
	"context"
	"sync"
)

// FakeClient adalah implementasi Client di memori untuk test dan dev.
// Tidak memanggil Google, jadi tidak ada biaya.
//
// Aman dipakai dari banyak goroutine sekaligus: semua akses ke map dijaga
// sync.Mutex, karena nanti worker bisa mengecek beberapa listing paralel.
type FakeClient struct {
	mu     sync.Mutex
	places map[string]Place
	errs   map[string]error
	calls  map[string]int
}

var _ Client = (*FakeClient)(nil)

// NewFakeClient membuat FakeClient yang sudah berisi places (boleh kosong).
func NewFakeClient(places ...Place) *FakeClient {
	f := &FakeClient{
		places: make(map[string]Place),
		errs:   make(map[string]error),
		calls:  make(map[string]int),
	}
	for _, p := range places {
		f.places[p.ID] = p
	}
	return f
}

// Set menambah atau mengganti data listing, misalnya untuk mensimulasikan
// nomor telepon yang diubah orang lain.
func (f *FakeClient) Set(p Place) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.places[p.ID] = p
}

// SetError membuat GetPlace untuk placeID selalu mengembalikan err.
// Panggil dengan err nil untuk menghapusnya.
func (f *FakeClient) SetError(placeID string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err == nil {
		delete(f.errs, placeID)
		return
	}
	f.errs[placeID] = err
}

// Calls mengembalikan berapa kali GetPlace dipanggil untuk placeID.
func (f *FakeClient) Calls(placeID string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[placeID]
}

// GetPlace memenuhi interface Client dengan perilaku yang sama seperti
// GoogleClient: validasi place_id, hormati ctx, dan ErrNotFound jika tidak ada.
func (f *FakeClient) GetPlace(ctx context.Context, placeID string) (Place, error) {
	if !ValidPlaceID(placeID) {
		return Place{}, ErrInvalidPlaceID
	}
	if err := ctx.Err(); err != nil {
		return Place{}, err
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[placeID]++
	if err, ok := f.errs[placeID]; ok {
		return Place{}, err
	}
	p, ok := f.places[placeID]
	if !ok {
		return Place{}, ErrNotFound
	}
	return p, nil
}
