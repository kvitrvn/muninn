package ods

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/kvitrvn/muninn"
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
			return muninn.Tender{SourceID: id}
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
	if len(got) != 250 {
		t.Errorf("retrieved = %d, want 250", len(got))
	}
	if got[0].SourceID != "rec-0" || got[249].SourceID != "rec-249" {
		t.Errorf("bounds = %q..%q", got[0].SourceID, got[249].SourceID)
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
	// total_count > pagination window (10,000): Search must return the paginable
	// records AND a *muninn.ErrTruncated carrying the real total.
	srv := newPagingServer(t, 25000)
	defer srv.Close()

	got, err := testClient(srv.URL).Search(context.Background(), muninn.Query{Keywords: []string{"GED"}})

	var truncated *muninn.ErrTruncated
	if !errors.As(err, &truncated) {
		t.Fatalf("want *muninn.ErrTruncated, got %v", err)
	}
	if truncated.Total != 25000 {
		t.Errorf("Total = %d, want 25000", truncated.Total)
	}
	if len(got) != maxOffsetWindow {
		t.Errorf("retrieved = %d, want %d (cap)", len(got), maxOffsetWindow)
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
