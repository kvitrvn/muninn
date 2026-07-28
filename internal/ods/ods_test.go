package ods

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kvitrvn/muninn"
	"github.com/kvitrvn/muninn/internal/httpx"
)

// newPagingServer simulates the Opendatasoft API: total records served in pages
// according to the offset parameter, with a constant total_count.
func newPagingServer(t *testing.T, total int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if limit <= 0 {
			limit = 10
		}
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))

		results := make([]map[string]any, 0, limit)
		for i := offset; i < offset+limit && i < total; i++ {
			results = append(results, map[string]any{"idweb": fmt.Sprintf("rec-%d", i)})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"total_count": total,
			"results":     results,
		})
	}))
}

// testClient builds an ods.Client pointed at srv with a trivial mapper.
func testClient(url string) *Client {
	return &Client{
		Source:  "test",
		BaseURL: url,
		HTTP:    http.DefaultClient,
		Map: func(rec map[string]any) muninn.Tender {
			id, _ := rec["idweb"].(string)
			return muninn.Tender{Sources: []muninn.SourceReference{{Provider: "test", ID: id}}}
		},
		Where: KeywordClause,
	}
}

func TestSearch_Paginates(t *testing.T) {
	srv := newPagingServer(t, 250)
	defer srv.Close()

	got, err := testClient(srv.URL).Search(context.Background(), muninn.Query{Keywords: []string{"GED"}})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got.Items) != 250 {
		t.Errorf("retrieved = %d, want 250", len(got.Items))
	}
	if got.Items[0].Sources[0].ID != "rec-0" || got.Items[249].Sources[0].ID != "rec-249" {
		t.Errorf("bounds = %q..%q", got.Items[0].Sources[0].ID, got.Items[249].Sources[0].ID)
	}
	if got.Total != 250 || !got.TotalExact || got.Truncated {
		t.Errorf("metadata = %+v", got)
	}
}

func TestCount(t *testing.T) {
	srv := newPagingServer(t, 250)
	defer srv.Close()

	n, err := testClient(srv.URL).Count(context.Background(), muninn.Query{Keywords: []string{"GED"}})
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if n != 250 {
		t.Errorf("Count = %d, want 250", n)
	}
}

func TestSearch_TruncatedBeyondWindow(t *testing.T) {
	// total_count > pagination window (10,000): Search returns the paginable
	// records and marks the structured result as truncated.
	srv := newPagingServer(t, 25000)
	defer srv.Close()

	got, err := testClient(srv.URL).Search(context.Background(), muninn.Query{Keywords: []string{"GED"}})

	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if got.Total != 25000 || !got.TotalExact || !got.Truncated {
		t.Errorf("metadata = %+v", got)
	}
	if len(got.Items) != maxOffsetWindow {
		t.Errorf("retrieved = %d, want %d (cap)", len(got.Items), maxOffsetWindow)
	}
}

// TestSearch_RetriesTransientServerError verifies the ods layer retries a
// transient 500 (via the shared httpx.Do) instead of failing the whole Search.
func TestSearch_RetriesTransientServerError(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&calls, 1) <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"total_count": 1,
			"results":     []map[string]any{{"idweb": "rec-0"}},
		})
	}))
	defer srv.Close()

	c := testClient(srv.URL)
	c.Retry = httpx.RetryPolicy{MaxAttempts: 4, BaseDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond}

	got, err := c.Search(context.Background(), muninn.Query{Keywords: []string{"GED"}})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got.Items) != 1 || got.Items[0].Sources[0].ID != "rec-0" {
		t.Fatalf("got %+v, want [rec-0]", got)
	}
	if calls != 3 {
		t.Errorf("calls = %d, want 3 (2 failures + 1 success)", calls)
	}
}

func TestKeywordClause(t *testing.T) {
	tests := []struct {
		name string
		q    muninn.Query
		want string
	}{
		{"empty", muninn.Query{}, ""},
		{
			"full-text OR (default)",
			muninn.Query{Keywords: []string{"GED", "gestion documentaire"}},
			`("GED" OR "gestion documentaire")`,
		},
		{
			"objet AND",
			muninn.Query{Keywords: []string{"IA", "données personnelles"}, ObjetOnly: true, MatchAll: true},
			`(objet like "IA" AND objet like "données personnelles")`,
		},
		{
			"objet OR",
			muninn.Query{Keywords: []string{"GED", "SAE"}, ObjetOnly: true},
			`(objet like "GED" OR objet like "SAE")`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := KeywordClause(tt.q); got != tt.want {
				t.Errorf("KeywordClause() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAnd(t *testing.T) {
	got := And(`("GED")`, "", `code_departement="75"`)
	want := `("GED") AND code_departement="75"`
	if got != want {
		t.Errorf("And() = %q, want %q", got, want)
	}
}
