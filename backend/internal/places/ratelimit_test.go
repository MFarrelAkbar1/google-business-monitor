package places

import (
	"context"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

func TestWithRateLimit(t *testing.T) {
	fake := NewFakeClient(Place{ID: "ChIJabc"})
	// 1 call per jam, burst 1: call pertama langsung lolos, call kedua harus menunggu.
	c := WithRateLimit(fake, rate.NewLimiter(rate.Every(time.Hour), 1))

	if _, err := c.GetPlace(context.Background(), "ChIJabc"); err != nil {
		t.Fatalf("first call: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := c.GetPlace(ctx, "ChIJabc"); err == nil {
		t.Fatal("second call should be blocked by the limiter")
	}
	if got := fake.Calls("ChIJabc"); got != 1 {
		t.Errorf("calls to Google = %d, want 1", got)
	}
}
