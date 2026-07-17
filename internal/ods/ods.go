// Package ods provides the shared plumbing for querying an Opendatasoft Explore
// API v2.1 dataset. It is used by the boamp and decp providers, which differ
// only by their base URL, their record mapping, and their where-clause field
// names — all injected into Client.
//
// The API's `q` (free-text) parameter is ignored in v2.1: filtering happens
// entirely through the ODSQL `where` clause built by each provider, typically
// with the KeywordClause helper below.
package ods

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/kvitrvn/muninn"
)

// Opendatasoft Explore API v2.1 constraints (verified live):
//   - maxPageSize: limit is capped at 100 per request.
//   - maxOffsetWindow: offset+limit must stay <= 10000. Beyond that, the
//     /exports endpoint is required (out of scope for now).
const (
	maxPageSize     = 100
	maxOffsetWindow = 10000
)

// Mapper converts a raw Opendatasoft record into a muninn.Tender.
type Mapper func(map[string]any) muninn.Tender

// Client queries a single Opendatasoft Explore v2.1 dataset. All fields are
// required; construct it directly from a provider package.
type Client struct {
	// Source is the provider name, used to prefix errors (e.g. "boamp").
	Source string
	// BaseURL is the full .../records endpoint of the dataset.
	BaseURL string
	// HTTP is the client used for requests.
	HTTP *http.Client
	// Map turns a raw record into a Tender.
	Map Mapper
	// Where builds the ODSQL `where` clause for a query (dataset-specific field
	// names live here). An empty return means "no filter".
	Where func(muninn.Query) string
}

// response mirrors the generic shape of an Explore API v2.1 response.
type response struct {
	TotalCount int              `json:"total_count"`
	Results    []map[string]any `json:"results"`
}

// Count returns the total number of records matching q without fetching them (a
// single request, limit=1). The total_count returned by the API is not capped
// by the 10,000 pagination window, so this is the reliable way to estimate a
// volume.
func (c *Client) Count(ctx context.Context, q muninn.Query) (int, error) {
	resp, err := c.fetchPage(ctx, c.Where(q), 1, 0)
	if err != nil {
		return 0, err
	}
	return resp.TotalCount, nil
}

// Search fetches all records matching q by paginating (pages of maxPageSize),
// within the API offset window (maxOffsetWindow) and, when q.Limit > 0, that
// explicit cap. When the total exceeds what can be paginated, it returns the
// fetched records AND a *muninn.ErrTruncated carrying the real total.
func (c *Client) Search(ctx context.Context, q muninn.Query) ([]muninn.Tender, error) {
	where := c.Where(q)

	// Effective upper bound: API window, tightened by q.Limit when provided.
	hardCap := maxOffsetWindow
	if q.Limit > 0 && q.Limit < hardCap {
		hardCap = q.Limit
	}

	var (
		tenders    []muninn.Tender
		totalCount int
	)
	for offset := 0; offset < hardCap; offset += maxPageSize {
		pageSize := maxPageSize
		if remaining := hardCap - offset; remaining < pageSize {
			pageSize = remaining
		}

		resp, err := c.fetchPage(ctx, where, pageSize, offset)
		if err != nil {
			return tenders, err
		}
		totalCount = resp.TotalCount

		for _, rec := range resp.Results {
			tenders = append(tenders, c.Map(rec))
		}
		// Last page reached (fewer records than requested, or nothing left
		// server-side).
		if len(resp.Results) < pageSize || len(tenders) >= totalCount {
			break
		}
	}

	if totalCount > len(tenders) {
		return tenders, &muninn.ErrTruncated{Retrieved: len(tenders), Total: totalCount}
	}
	return tenders, nil
}

// fetchPage performs a single API request and decodes the response. Shared by
// Search and Count so there is only one HTTP path.
func (c *Client) fetchPage(ctx context.Context, where string, limit, offset int) (response, error) {
	params := url.Values{}
	if where != "" {
		params.Set("where", where)
	}
	params.Set("limit", strconv.Itoa(limit))
	if offset > 0 {
		params.Set("offset", strconv.Itoa(offset))
	}

	reqURL := c.BaseURL + "?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return response{}, fmt.Errorf("%s: build request: %w", c.Source, err)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return response{}, fmt.Errorf("%s: request failed: %w", c.Source, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return response{}, fmt.Errorf("%s: unexpected status %d: %s", c.Source, resp.StatusCode, body)
	}

	var parsed response
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return response{}, fmt.Errorf("%s: decode response: %w", c.Source, err)
	}
	return parsed, nil
}

// KeywordClause turns q.Keywords into a parenthesized ODSQL clause. Two
// independent knobs:
//   - ObjetOnly: `objet like "kw"` (phrase in the title, precise) vs `"kw"`
//     (full-text over all fields, broad and noisy).
//   - MatchAll: terms joined with AND (intersection) vs OR (union, default).
//
// Returns "" when there is no usable keyword. The `objet` field exists on every
// Opendatasoft procurement dataset targeted here.
func KeywordClause(q muninn.Query) string {
	var parts []string
	for _, k := range q.Keywords {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		if q.ObjetOnly {
			parts = append(parts, fmt.Sprintf(`objet like "%s"`, Escape(k)))
		} else {
			parts = append(parts, fmt.Sprintf(`"%s"`, Escape(k)))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	sep := " OR "
	if q.MatchAll {
		sep = " AND "
	}
	return "(" + strings.Join(parts, sep) + ")"
}

// And joins the non-empty clauses with " AND ". Empty clauses are dropped, so a
// provider can pass optional filters without guarding each one.
func And(clauses ...string) string {
	kept := clauses[:0]
	for _, c := range clauses {
		if c != "" {
			kept = append(kept, c)
		}
	}
	return strings.Join(kept, " AND ")
}

// Escape quotes a double quote inside an ODSQL string literal.
func Escape(s string) string {
	return strings.ReplaceAll(s, `"`, `\"`)
}

// ParseDate tries the two date formats seen on the Opendatasoft API (date only,
// or date+time ISO8601).
func ParseDate(v string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, nil
	}
	return time.Parse("2006-01-02", v)
}
