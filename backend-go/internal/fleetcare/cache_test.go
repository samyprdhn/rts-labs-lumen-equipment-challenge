package fleetcare

import (
	"context"
	"errors"
	"testing"
	"time"
)

var errUnavailable = errors.New("fleetcare: service unavailable (test double)")

// fakeFetcher is a deterministic test double - no network, no real timing,
// no dependency on the vendor mock's random chaos.
type fakeFetcher struct {
	calls   int
	results map[string][]Window
	err     error
	// tagsSeen records what FetchAll was asked for, per call.
	tagsSeen [][]string
}

func (f *fakeFetcher) FetchAll(ctx context.Context, tags []string) (map[string][]Window, error) {
	f.calls++
	f.tagsSeen = append(f.tagsSeen, append([]string{}, tags...))
	return f.results, f.err
}

func day(y, m, d, h int) time.Time {
	return time.Date(y, time.Month(m), d, h, 0, 0, 0, time.UTC)
}

// Scenario 9: before any successful fetch, status must be "unknown", never "ok".
func TestCache_ColdStartIsUnknown(t *testing.T) {
	fetcher := &fakeFetcher{err: errUnavailable} // every fetch fails
	cache := NewCache(fetcher, 5*time.Minute, 15*time.Minute)

	now := day(2026, 10, 2, 0)
	cache.EnsureFresh(context.Background(), []string{"MC-004"}, now)

	status, _ := cache.Status("MC004", now, day(2026, 10, 2, 0), day(2026, 10, 2, 23))
	if status != "unknown" {
		t.Fatalf("expected unknown at cold start, got %q", status)
	}
}

// Scenario 1: a window intersecting the query range forces in_maintenance.
func TestCache_InMaintenanceWhenWindowOverlaps(t *testing.T) {
	windowStart := time.Date(2026, 10, 2, 16, 0, 0, 0, time.UTC)
	windowEnd := time.Date(2026, 10, 2, 20, 0, 0, 0, time.UTC)
	fetcher := &fakeFetcher{results: map[string][]Window{
		"MC004": {{StartsAt: windowStart, EndsAt: windowEnd, Kind: "annual_inspection"}},
	}}
	cache := NewCache(fetcher, 5*time.Minute, 15*time.Minute)
	now := day(2026, 10, 2, 0)
	cache.EnsureFresh(context.Background(), []string{"MC-004"}, now)

	status, _ := cache.Status("MC004", now, day(2026, 10, 2, 0), day(2026, 10, 2, 23))
	if status != "in_maintenance" {
		t.Fatalf("expected in_maintenance, got %q", status)
	}
}

// Scenario 2/3: no overlapping window (or no windows at all) means ok.
func TestCache_OkWhenNoOverlap(t *testing.T) {
	fetcher := &fakeFetcher{results: map[string][]Window{
		"MC004": {}, // fetched successfully, genuinely zero windows
	}}
	cache := NewCache(fetcher, 5*time.Minute, 15*time.Minute)
	now := day(2026, 10, 2, 0)
	cache.EnsureFresh(context.Background(), []string{"MC-004"}, now)

	status, _ := cache.Status("MC004", now, day(2026, 10, 2, 0), day(2026, 10, 2, 23))
	if status != "ok" {
		t.Fatalf("expected ok, got %q", status)
	}
}

// Scenario 4: a window that only touches the query boundary must not
// trigger in_maintenance - same strict-inequality semantics as bookings.
func TestCache_AdjacentWindowIsNotInMaintenance(t *testing.T) {
	windowStart := time.Date(2026, 10, 2, 20, 0, 0, 0, time.UTC)
	windowEnd := time.Date(2026, 10, 2, 23, 0, 0, 0, time.UTC)
	fetcher := &fakeFetcher{results: map[string][]Window{
		"MC004": {{StartsAt: windowStart, EndsAt: windowEnd}},
	}}
	cache := NewCache(fetcher, 5*time.Minute, 15*time.Minute)
	now := day(2026, 10, 2, 0)
	cache.EnsureFresh(context.Background(), []string{"MC-004"}, now)

	// query ends exactly when the window starts - touching, not overlapping
	status, _ := cache.Status("MC004", now, time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC), windowStart)
	if status != "ok" {
		t.Fatalf("a touching-boundary window must not be in_maintenance, got %q", status)
	}
}

// Scenario 12/13: within the TTL, a second EnsureFresh must not refetch;
// past the TTL, it must.
func TestCache_RespectsTTL(t *testing.T) {
	fetcher := &fakeFetcher{results: map[string][]Window{"MC004": {}}}
	cache := NewCache(fetcher, 5*time.Minute, 15*time.Minute)

	t0 := day(2026, 10, 2, 0)
	cache.EnsureFresh(context.Background(), []string{"MC-004"}, t0)
	if fetcher.calls != 1 {
		t.Fatalf("expected 1 call after first EnsureFresh, got %d", fetcher.calls)
	}

	cache.EnsureFresh(context.Background(), []string{"MC-004"}, t0.Add(2*time.Minute)) // within TTL
	if fetcher.calls != 1 {
		t.Fatalf("expected no refetch within TTL, got %d calls", fetcher.calls)
	}

	cache.EnsureFresh(context.Background(), []string{"MC-004"}, t0.Add(6*time.Minute)) // past TTL
	if fetcher.calls != 2 {
		t.Fatalf("expected a refetch past TTL, got %d calls", fetcher.calls)
	}
}

// Scenario 11: a partial batch failure must leave the tags that DID
// succeed usable, while tags never successfully fetched read as unknown.
func TestCache_PartialFailureIsolatesUnknownTags(t *testing.T) {
	fetcher := &fakeFetcher{
		results: map[string][]Window{"MC004": {}}, // only MC004 came back
		err:     errUnavailable,                   // but the overall call reports a failure (e.g. a later batch 503'd)
	}
	cache := NewCache(fetcher, 5*time.Minute, 15*time.Minute)
	now := day(2026, 10, 2, 0)
	cache.EnsureFresh(context.Background(), []string{"MC-004", "MC-005"}, now)

	okStatus, _ := cache.Status("MC004", now, day(2026, 10, 2, 0), day(2026, 10, 2, 23))
	if okStatus != "ok" {
		t.Fatalf("MC004 was successfully fetched and should read ok, got %q", okStatus)
	}
	unknownStatus, _ := cache.Status("MC005", now, day(2026, 10, 2, 0), day(2026, 10, 2, 23))
	if unknownStatus != "unknown" {
		t.Fatalf("MC005 was never fetched and must read unknown, not %q", unknownStatus)
	}
}

// Scenario 13 (rate limit side): after a 429, EnsureFresh must not call the
// fetcher again before Retry-After has elapsed (no retry storm).
func TestCache_RespectsRetryAfter(t *testing.T) {
	fetcher := &fakeFetcher{err: &RateLimitedError{RetryAfter: 10 * time.Minute}}
	cache := NewCache(fetcher, 1*time.Second, 1*time.Hour) // tiny TTL so it WOULD refetch if not blocked

	t0 := day(2026, 10, 2, 0)
	cache.EnsureFresh(context.Background(), []string{"MC-004"}, t0)
	if fetcher.calls != 1 {
		t.Fatalf("expected 1 call, got %d", fetcher.calls)
	}

	cache.EnsureFresh(context.Background(), []string{"MC-004"}, t0.Add(5*time.Minute)) // still inside RetryAfter
	if fetcher.calls != 1 {
		t.Fatalf("must not retry before Retry-After elapses, got %d calls", fetcher.calls)
	}

	cache.EnsureFresh(context.Background(), []string{"MC-004"}, t0.Add(11*time.Minute)) // past RetryAfter
	if fetcher.calls != 2 {
		t.Fatalf("expected a retry after Retry-After elapsed, got %d calls", fetcher.calls)
	}
}
