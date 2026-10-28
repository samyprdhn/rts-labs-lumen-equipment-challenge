package fleetcare

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"lumen/equipment-api/internal/availability"
)

// Fetcher is the subset of Client's behavior Cache depends on, so tests can
// substitute a deterministic double instead of a real HTTP vendor call.
type Fetcher interface {
	FetchAll(ctx context.Context, tags []string) (map[string][]Window, error)
}

// Cache holds the last successfully-fetched FleetCare windows per asset tag,
// with per-tag freshness tracking so a partial batch failure only degrades
// the tags it actually failed to refresh.
//
// Cold start (no successful fetch yet for a tag) and data older than
// staleAfter both report as "unknown" - this is deliberate: an availability
// consumer must never read "ok" for data we don't actually have confidence
// in. There is no built-in retry loop; EnsureFresh is called once per
// incoming request and only attempts a fetch when the TTL has elapsed and
// no rate-limit backoff is in effect, so there is no retry storm.
type Cache struct {
	mu           sync.Mutex
	fetcher      Fetcher
	ttl          time.Duration
	staleAfter   time.Duration
	blockedUntil time.Time
	windows      map[string][]Window
	asOf         map[string]time.Time
}

// NewCache builds a cache. ttl is how often a refresh is attempted;
// staleAfter is how long previously-good data may be served before it must
// be reported as unknown rather than ok (staleAfter should be > ttl, giving
// a few missed refresh cycles of grace before failing cautious).
func NewCache(fetcher Fetcher, ttl, staleAfter time.Duration) *Cache {
	return &Cache{
		fetcher:    fetcher,
		ttl:        ttl,
		staleAfter: staleAfter,
		windows:    map[string][]Window{},
		asOf:       map[string]time.Time{},
	}
}

// EnsureFresh refreshes any of tags whose data is older than the TTL (or
// never fetched), unless a prior 429 has us backed off past `now`. It never
// retries beyond this single attempt per call.
func (c *Cache) EnsureFresh(ctx context.Context, tags []string, now time.Time) {
	c.mu.Lock()
	if now.Before(c.blockedUntil) {
		c.mu.Unlock()
		return
	}
	var due []string
	for _, tag := range tags {
		key := availability.NormalizeTag(tag)
		if now.Sub(c.asOf[key]) > c.ttl {
			due = append(due, tag)
		}
	}
	c.mu.Unlock()

	if len(due) == 0 {
		return
	}

	result, err := c.fetcher.FetchAll(ctx, due)

	c.mu.Lock()
	defer c.mu.Unlock()
	for tag, windows := range result {
		key := availability.NormalizeTag(tag)
		c.windows[key] = windows
		c.asOf[key] = now
	}
	if err != nil {
		var rle *RateLimitedError
		if errors.As(err, &rle) {
			c.blockedUntil = now.Add(rle.RetryAfter)
		}
		log.Printf("fleetcare: refresh of %d tag(s) did not fully succeed: %v", len(due), err)
	}
}

// Status reports tag's maintenance state against [queryStart, queryEnd),
// using the same Overlaps test as booking conflicts. "unknown" is returned
// whenever we don't have sufficiently fresh data - never silently "ok".
func (c *Cache) Status(tag string, now, queryStart, queryEnd time.Time) (string, []Window) {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := availability.NormalizeTag(tag)
	asOf, ok := c.asOf[key]
	if !ok || now.Sub(asOf) > c.staleAfter {
		return "unknown", nil
	}

	windows := c.windows[key]
	for _, w := range windows {
		if availability.Overlaps(w.StartsAt, w.EndsAt, queryStart, queryEnd) {
			return "in_maintenance", windows
		}
	}
	return "ok", windows
}
