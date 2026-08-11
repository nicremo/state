package relay

import (
	"testing"
	"time"
)

func TestTokenBucketLimiterRefills(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 11, 20, 0, 0, 0, time.UTC)
	limiter := NewTokenBucketLimiter(2, time.Minute, func() time.Time { return now })
	if !limiter.Allow("route") || !limiter.Allow("route") {
		t.Fatal("initial burst was rejected")
	}
	if limiter.Allow("route") {
		t.Fatal("request above burst was accepted")
	}
	now = now.Add(30 * time.Second)
	if !limiter.Allow("route") {
		t.Fatal("refilled token was rejected")
	}
	if limiter.Allow("route") {
		t.Fatal("limiter refilled too many tokens")
	}
}
