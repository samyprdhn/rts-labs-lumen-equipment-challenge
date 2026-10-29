package fleetcare

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newTestServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *Client) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, "test-key", 200*time.Millisecond)
	return srv, c
}

// Scenario 10: 60 assets must be split into ceil(60/25)=3 batches, each <=25.
func TestFetchAll_RespectsBatchMax(t *testing.T) {
	var gotSizes []int
	_, c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct{ AssetTags []string `json:"asset_tags"` }
		json.NewDecoder(r.Body).Decode(&body)
		gotSizes = append(gotSizes, len(body.AssetTags))
		if len(body.AssetTags) > 25 {
			t.Errorf("batch of %d exceeds documented max of 25", len(body.AssetTags))
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"results": map[string]any{}})
	})

	tags := make([]string, 60)
	for i := range tags {
		tags[i] = "TAG"
	}
	_, err := c.FetchAll(context.Background(), tags)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(gotSizes) != 3 {
		t.Fatalf("expected 3 batch calls for 60 tags, got %d (%v)", len(gotSizes), gotSizes)
	}
}

// Scenario 1/3: happy path - windows come back and are attributed per tag.
func TestFetchAll_HappyPath(t *testing.T) {
	_, c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"results": map[string]any{
				"MC-001": []map[string]any{
					{"starts_at": "2026-10-02T16:00:00Z", "ends_at": "2026-10-02T20:00:00Z", "kind": "annual_inspection"},
				},
				"MC-002": []map[string]any{},
			},
		})
	})
	result, err := c.FetchAll(context.Background(), []string{"MC-001", "MC-002"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result["MC-001"]) != 1 {
		t.Fatalf("expected 1 window for MC-001, got %+v", result["MC-001"])
	}
	if len(result["MC-002"]) != 0 {
		t.Fatalf("expected 0 windows for MC-002, got %+v", result["MC-002"])
	}
}

// Scenario 5: a 503 must be reported as an error, not a panic, not treated as empty/ok.
func TestFetchAll_ServiceUnavailable(t *testing.T) {
	_, c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	_, err := c.FetchAll(context.Background(), []string{"MC-001"})
	if err == nil {
		t.Fatal("expected an error on 503, got nil")
	}
}

// Scenario 6: a response slower than the client's bound must time out, not hang the caller.
func TestFetchAll_TimesOut(t *testing.T) {
	_, c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second) // well beyond the 200ms client timeout configured above
		w.Write([]byte(`{}`))
	})
	start := time.Now()
	_, err := c.FetchAll(context.Background(), []string{"MC-001"})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected a timeout error, got nil")
	}
	if elapsed > time.Second {
		t.Fatalf("client did not respect its bounded timeout: took %v", elapsed)
	}
}

// Scenario 7: a 429 must surface as a distinguishable RateLimitedError the
// caller can use to avoid hammering the vendor (no immediate retry here).
func TestFetchAll_RateLimited(t *testing.T) {
	_, c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	_, err := c.FetchAll(context.Background(), []string{"MC-001"})
	var rle *RateLimitedError
	if !errors.As(err, &rle) {
		t.Fatalf("expected a RateLimitedError, got %v (%T)", err, err)
	}
	if rle.RetryAfter != 7*time.Second {
		t.Fatalf("expected RetryAfter=7s, got %v", rle.RetryAfter)
	}
}

// Scenario 8: malformed/truncated JSON must be a clean error, not a panic.
func TestFetchAll_MalformedBody(t *testing.T) {
	_, c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"asset_tag":"`)) // truncated, exactly like the mock's documented chaos
	})
	_, err := c.FetchAll(context.Background(), []string{"MC-001"})
	if err == nil {
		t.Fatal("expected an error on malformed JSON, got nil")
	}
}

// Scenario 11: one batch failing must not corrupt or drop results from a
// batch that succeeded - the caller needs per-batch attribution to implement
// "unknown for the failed slice, ok for the rest".
func TestFetchAll_PartialBatchFailure(t *testing.T) {
	calls := 0
	_, c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		var body struct{ AssetTags []string `json:"asset_tags"` }
		json.NewDecoder(r.Body).Decode(&body)
		results := map[string]any{}
		for _, t := range body.AssetTags {
			results[t] = []map[string]any{}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"results": results})
	})

	tags := make([]string, 30) // 2 batches: 25 + 5
	for i := range tags {
		tags[i] = "TAG"
	}
	result, err := c.FetchAll(context.Background(), tags)
	if err == nil {
		t.Fatal("expected FetchAll to surface the second batch's failure")
	}
	// The first (successful) batch's results must still be usable - a
	// partial failure must not destroy what did succeed.
	if len(result) == 0 {
		t.Fatal("expected partial results from the first successful batch, got none")
	}
}
