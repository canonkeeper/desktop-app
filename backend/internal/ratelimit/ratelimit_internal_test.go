package ratelimit

import (
	"context"
	"testing"
	"time"
)

func TestMemoryLimiterEvictsExpiredBuckets(t *testing.T) {
	clock := &memoryLimiterTestClock{now: time.Date(2026, 7, 6, 10, 0, 0, 0, time.UTC)}
	limiter := NewMemoryLimiter(MemoryLimiterConfig{
		Limit:  1,
		Window: time.Minute,
		Now:    clock.Now,
	})

	mustAllowMemoryLimiter(t, limiter, AuthenticatedKey("user-1", "project-1", "getJob"))
	mustAllowMemoryLimiter(t, limiter, AuthenticatedKey("user-2", "project-2", "getProject"))
	if got := len(limiter.buckets); got != 2 {
		t.Fatalf("bucket count before expiry = %d, want 2", got)
	}

	clock.Advance(time.Minute + time.Second)
	mustAllowMemoryLimiter(t, limiter, AuthenticatedKey("user-3", "project-3", "listProjects"))

	if got := len(limiter.buckets); got != 1 {
		t.Fatalf("bucket count after expiry cleanup = %d, want 1", got)
	}
	if _, exists := limiter.buckets[AuthenticatedKey("user-1", "project-1", "getJob").String()]; exists {
		t.Fatal("expired bucket for user-1/getJob was not evicted")
	}
	if _, exists := limiter.buckets[AuthenticatedKey("user-2", "project-2", "getProject").String()]; exists {
		t.Fatal("expired bucket for user-2/getProject was not evicted")
	}
}

type memoryLimiterTestClock struct {
	now time.Time
}

func (clock *memoryLimiterTestClock) Now() time.Time {
	return clock.now
}

func (clock *memoryLimiterTestClock) Advance(duration time.Duration) {
	clock.now = clock.now.Add(duration)
}

func mustAllowMemoryLimiter(t *testing.T, limiter *MemoryLimiter, key Key) {
	t.Helper()

	decision, err := limiter.Allow(context.Background(), key)
	if err != nil {
		t.Fatalf("Allow returned error: %v", err)
	}
	if !decision.Allowed {
		t.Fatalf("Allow denied key unexpectedly: %+v", decision)
	}
}
