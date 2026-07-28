package beauamp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kvitrvn/muninn"
)

// newCatalogServer serves a fixed one-resource catalog, counting requests via
// calls. When fail is non-nil and reports true, it responds with a 500
// instead.
func newCatalogServer(t *testing.T, calls *int32, fail func() bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(calls, 1)
		if fail != nil && fail() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"resources": []map[string]any{
				{"id": "res-2026-06", "title": "beauamp_juin_2026_1.1.0.csv", "format": "csv"},
			},
		})
	}))
}

func TestResolveResources_CachesWithinTTL(t *testing.T) {
	var calls int32
	srv := newCatalogServer(t, &calls, nil)
	defer srv.Close()

	c := New(WithCatalogBaseURL(srv.URL+"/"), WithResourceCacheTTL(time.Hour))

	for i := 0; i < 3; i++ {
		if _, err := c.resolveResources(context.Background(), muninn.Query{}); err != nil {
			t.Fatalf("resolveResources: %v", err)
		}
	}
	if calls != 1 {
		t.Errorf("catalog calls = %d, want 1 (cached within TTL)", calls)
	}
}

func TestResolveResources_RefetchesAfterTTLExpiry(t *testing.T) {
	var calls int32
	srv := newCatalogServer(t, &calls, nil)
	defer srv.Close()

	c := New(WithCatalogBaseURL(srv.URL+"/"), WithResourceCacheTTL(time.Millisecond))

	if _, err := c.resolveResources(context.Background(), muninn.Query{}); err != nil {
		t.Fatalf("resolveResources: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	if _, err := c.resolveResources(context.Background(), muninn.Query{}); err != nil {
		t.Fatalf("resolveResources: %v", err)
	}
	if calls != 2 {
		t.Errorf("catalog calls = %d, want 2 (TTL expired between calls)", calls)
	}
}

func TestResolveResources_DisabledCacheRefetchesEveryCall(t *testing.T) {
	var calls int32
	srv := newCatalogServer(t, &calls, nil)
	defer srv.Close()

	c := New(WithCatalogBaseURL(srv.URL+"/"), WithResourceCacheTTL(0))

	for i := 0; i < 3; i++ {
		if _, err := c.resolveResources(context.Background(), muninn.Query{}); err != nil {
			t.Fatalf("resolveResources: %v", err)
		}
	}
	if calls != 3 {
		t.Errorf("catalog calls = %d, want 3 (cache disabled)", calls)
	}
}

func TestResolveResources_ConcurrentCallsSingleFetch(t *testing.T) {
	var calls int32
	srv := newCatalogServer(t, &calls, nil)
	defer srv.Close()

	c := New(WithCatalogBaseURL(srv.URL+"/"), WithResourceCacheTTL(time.Hour))

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.resolveResources(context.Background(), muninn.Query{}); err != nil {
				t.Errorf("resolveResources: %v", err)
			}
		}()
	}
	wg.Wait()

	if calls != 1 {
		t.Errorf("catalog calls = %d, want 1 (concurrent cold-cache calls collapse into one fetch)", calls)
	}
}

func TestResolveResources_RefreshErrorAfterExpiryPropagatesAndDoesNotServeStale(t *testing.T) {
	var calls int32
	var failing atomic.Bool
	srv := newCatalogServer(t, &calls, failing.Load)
	defer srv.Close()

	c := New(WithCatalogBaseURL(srv.URL+"/"), WithResourceCacheTTL(time.Millisecond))

	if _, err := c.resolveResources(context.Background(), muninn.Query{}); err != nil {
		t.Fatalf("initial resolveResources: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	failing.Store(true)

	if _, err := c.resolveResources(context.Background(), muninn.Query{}); err == nil {
		t.Error("resolveResources: want error on refresh failure, got nil (should not serve stale cache silently)")
	}
}
