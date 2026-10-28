// Package fleetcare is a client for the FleetCare vendor maintenance API
// (services/maintenance-api in this repo is a mock of it). The vendor is
// treated as immutable: this client only adapts to its documented contract
// (GET /v1/assets/:tag/maintenance, POST /v1/maintenance/batch, max 25 tags
// per batch, 20 requests/10s, X-Api-Key auth) - it never assumes behavior
// beyond what's documented and measured.
package fleetcare

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

const batchMax = 25

// Window is one FleetCare maintenance window for an asset.
type Window struct {
	StartsAt   time.Time
	EndsAt     time.Time
	Kind       string
	Technician string
}

// RateLimitedError means the vendor returned 429. RetryAfter is how long to
// wait before trying again - callers must not retry sooner than this.
type RateLimitedError struct {
	RetryAfter time.Duration
}

func (e *RateLimitedError) Error() string {
	return fmt.Sprintf("fleetcare: rate limited, retry after %s", e.RetryAfter)
}

// Client talks to one FleetCare instance over HTTP.
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// NewClient builds a client with a bounded per-call timeout. There is no
// built-in retry: callers decide retry/backoff policy (this package never
// retries on its own, to avoid retry storms against a rate-limited vendor).
func NewClient(baseURL, apiKey string, timeout time.Duration) *Client {
	return &Client{
		baseURL:    baseURL,
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: timeout},
	}
}

// FetchAll fetches maintenance windows for all of tags, batching into
// groups of at most 25 (the vendor's documented maximum) and making one
// request per batch. It returns whatever batches succeeded even if a later
// batch fails - a partial failure must not discard partial success - along
// with a non-nil error naming the failure. Tags missing from the returned
// map are exactly the ones the caller does not have fresh data for.
func (c *Client) FetchAll(ctx context.Context, tags []string) (map[string][]Window, error) {
	result := map[string][]Window{}
	for start := 0; start < len(tags); start += batchMax {
		end := start + batchMax
		if end > len(tags) {
			end = len(tags)
		}
		batch := tags[start:end]
		batchResult, err := c.fetchBatch(ctx, batch)
		for tag, windows := range batchResult {
			result[tag] = windows
		}
		if err != nil {
			return result, fmt.Errorf("batch %d-%d: %w", start, end, err)
		}
	}
	return result, nil
}

func (c *Client) fetchBatch(ctx context.Context, tags []string) (map[string][]Window, error) {
	body, _ := json.Marshal(map[string]any{"asset_tags": tags})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/maintenance/batch", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		retryAfter := 1 * time.Second
		if v := resp.Header.Get("Retry-After"); v != "" {
			if secs, err := strconv.Atoi(v); err == nil {
				retryAfter = time.Duration(secs) * time.Second
			}
		}
		return nil, &RateLimitedError{RetryAfter: retryAfter}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fleetcare returned HTTP %d", resp.StatusCode)
	}

	var parsed struct {
		Results map[string][]struct {
			StartsAt   string `json:"starts_at"`
			EndsAt     string `json:"ends_at"`
			Kind       string `json:"kind"`
			Technician string `json:"technician"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	out := map[string][]Window{}
	for tag, windows := range parsed.Results {
		converted := make([]Window, 0, len(windows))
		for _, w := range windows {
			starts, err := time.Parse(time.RFC3339, w.StartsAt)
			if err != nil {
				return out, fmt.Errorf("asset %s: unparseable starts_at %q: %w", tag, w.StartsAt, err)
			}
			ends, err := time.Parse(time.RFC3339, w.EndsAt)
			if err != nil {
				return out, fmt.Errorf("asset %s: unparseable ends_at %q: %w", tag, w.EndsAt, err)
			}
			converted = append(converted, Window{StartsAt: starts, EndsAt: ends, Kind: w.Kind, Technician: w.Technician})
		}
		out[tag] = converted
	}
	return out, nil
}
