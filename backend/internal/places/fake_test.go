package places

import (
	"context"
	"errors"
	"testing"
)

func TestFakeClient(t *testing.T) {
	ctx := context.Background()
	f := NewFakeClient(Place{ID: "abc", DisplayName: "Warung"})

	got, err := f.GetPlace(ctx, "abc")
	if err != nil || got.DisplayName != "Warung" {
		t.Fatalf("GetPlace = %+v, %v", got, err)
	}

	f.Set(Place{ID: "abc", DisplayName: "Warung Baru"})
	if got, _ := f.GetPlace(ctx, "abc"); got.DisplayName != "Warung Baru" {
		t.Errorf("after Set, DisplayName = %q", got.DisplayName)
	}

	if _, err := f.GetPlace(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing place err = %v, want ErrNotFound", err)
	}
	if _, err := f.GetPlace(ctx, "bad/id"); !errors.Is(err, ErrInvalidPlaceID) {
		t.Errorf("invalid id err = %v, want ErrInvalidPlaceID", err)
	}

	quota := &APIError{HTTPStatus: 429, Status: "RESOURCE_EXHAUSTED"}
	f.SetError("abc", quota)
	if _, err := f.GetPlace(ctx, "abc"); !errors.Is(err, quota) {
		t.Errorf("err = %v, want injected error", err)
	}
	f.SetError("abc", nil)
	if _, err := f.GetPlace(ctx, "abc"); err != nil {
		t.Errorf("after clearing error, err = %v", err)
	}

	if n := f.Calls("abc"); n != 4 {
		t.Errorf("Calls(abc) = %d, want 4", n)
	}

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := f.GetPlace(canceled, "abc"); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled ctx err = %v, want context.Canceled", err)
	}
}
