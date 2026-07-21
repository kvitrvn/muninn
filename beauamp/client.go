// Package beauamp implements muninn.Provider for BEAUAMP (Base Étendue,
// Améliorée et Unifiée des Annonces des Marchés Publics), published on
// data.gouv.fr. BEAUAMP is BOAMP consolidated into a tabular form and enriched
// with SIRENE matching and award results, so a single record already carries
// the buyer, the winning supplier and an (indicative) amount — no eForms
// parsing needed.
//
// It is queried through the data.gouv.fr tabular API, which serves one file
// (resource) at a time. The provider resolves the relevant monthly resources
// from the dataset catalog (see WithResources to pin them explicitly) and
// aggregates across them.
//
// Caveats:
//   - BEAUAMP data is declared "à valeur indicative"; the original BOAMP notice
//     remains authoritative, and DECP is the reference for awarded amounts.
//   - The tabular API only filters per column, so searches run on the objet
//     field; there is no full-text mode. Multiple keywords are combined with OR
//     by issuing one query each and unioning; MatchAll is applied client-side in
//     Search (Count reports the OR upper bound — see Count).
//   - Large yearly files are not served by the tabular API; only daily/monthly
//     resources are queryable, which bounds how far back a query reaches.
package beauamp

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

const (
	defaultTabularBase = "https://tabular-api.data.gouv.fr/api/resources/"
	defaultCatalogBase = "https://www.data.gouv.fr/api/1/datasets/"
	defaultSlug        = "base-etendue-amelioree-et-unifiee-des-annonces-des-marches-publics"
	// searchField is the column filtered on; the tabular API has no full-text.
	searchField = "objet"
	// pageSize is the number of rows requested per tabular API page.
	pageSize = 100
	// maxFetch bounds Search when the caller sets no Query.Limit, to avoid
	// pulling an entire resource.
	maxFetch = 10000
)

// Client queries BEAUAMP through the data.gouv.fr tabular API.
type Client struct {
	tabularBase string
	catalogBase string
	slug        string
	http        *http.Client
	resources   []string // explicit resource ids; empty means resolve from catalog
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient injects a custom *http.Client.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) { c.http = h }
}

// WithTabularBaseURL overrides the tabular API base (useful for tests).
func WithTabularBaseURL(u string) Option {
	return func(c *Client) { c.tabularBase = u }
}

// WithCatalogBaseURL overrides the data.gouv.fr catalog base (useful for tests).
func WithCatalogBaseURL(u string) Option {
	return func(c *Client) { c.catalogBase = u }
}

// WithDatasetSlug overrides the dataset slug used for resource resolution.
func WithDatasetSlug(s string) Option {
	return func(c *Client) { c.slug = s }
}

// WithResources pins the exact tabular resource ids to query, skipping catalog
// resolution. Use it to target specific months, or to stay fully offline in
// tests.
func WithResources(ids ...string) Option {
	return func(c *Client) { c.resources = ids }
}

// New creates a BEAUAMP client.
func New(opts ...Option) *Client {
	c := &Client{
		tabularBase: defaultTabularBase,
		catalogBase: defaultCatalogBase,
		slug:        defaultSlug,
		http:        &http.Client{Timeout: 30 * time.Second},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Compile-time check: *Client satisfies the muninn.Provider contract.
var _ muninn.Provider = (*Client)(nil)

// Name implements muninn.Provider.
func (c *Client) Name() string { return "beauamp" }

// Count returns an estimate of the matching notices: the sum, over the resolved
// resources and each keyword, of the tabular API's total for that filter. For
// several keywords this is an OR upper bound (a notice matching two keywords is
// counted twice) and MatchAll is not applied — use Search for an exact,
// deduplicated, AND-aware result set. The advanced filters (CPV, amount range,
// buyer SIREN) are pushed into every per-keyword request, so the upper bound
// holds with them applied as well.
func (c *Client) Count(ctx context.Context, q muninn.Query) (int, error) {
	resources, err := c.resolveResources(ctx, q)
	if err != nil {
		return 0, err
	}
	terms := keywords(q)

	total := 0
	for _, res := range resources {
		for _, term := range terms {
			page, err := c.fetchPage(ctx, res, term, 1, 1, q)
			if err != nil {
				return 0, err
			}
			total += page.Meta.Total
		}
	}
	return total, nil
}

// Search fetches the matching notices across the resolved resources, maps them
// to Tenders, deduplicates them (a notice may recur across resources or match
// several keywords), and — when q.MatchAll is set — keeps only those whose objet
// contains every keyword. The advanced filters (CPV, amount range, buyer SIREN)
// are pushed into each per-keyword tabular query so the API narrows the page
// before it ships. It returns a *muninn.ErrTruncated when the fetch hit the
// q.Limit / maxFetch bound before exhausting the matches.
func (c *Client) Search(ctx context.Context, q muninn.Query) ([]muninn.Tender, error) {
	resources, err := c.resolveResources(ctx, q)
	if err != nil {
		return nil, err
	}
	terms := keywords(q)

	limit := maxFetch
	if q.Limit > 0 && q.Limit < limit {
		limit = q.Limit
	}

	var (
		out       []muninn.Tender
		seen      = map[string]bool{}
		total     int
		truncated bool
	)
	for _, res := range resources {
		for _, term := range terms {
			for page := 1; ; page++ {
				resp, err := c.fetchPage(ctx, res, term, pageSize, page, q)
				if err != nil {
					return out, err
				}
				total += resp.Meta.Total
				for _, rec := range resp.Data {
					t := mapRecord(rec)
					if q.MatchAll && !containsAll(t.Objet, q.Keywords) {
						continue
					}
					key := t.DedupKey()
					if seen[key] {
						continue
					}
					seen[key] = true
					out = append(out, t)
					if len(out) >= limit {
						truncated = true
						break
					}
				}
				if truncated || len(resp.Data) < pageSize || page*pageSize >= resp.Meta.Total {
					break
				}
			}
			if truncated {
				break
			}
		}
		if truncated {
			break
		}
	}

	if truncated {
		return out, &muninn.ErrTruncated{Retrieved: len(out), Total: total}
	}
	return out, nil
}

// keywords returns the trimmed non-empty keywords, or a single empty term so the
// callers issue one unfiltered query when no keyword is given.
func keywords(q muninn.Query) []string {
	var terms []string
	for _, k := range q.Keywords {
		if k = strings.TrimSpace(k); k != "" {
			terms = append(terms, k)
		}
	}
	if len(terms) == 0 {
		return []string{""}
	}
	return terms
}

// containsAll reports whether objet contains every keyword, case-insensitively.
func containsAll(objet string, keywords []string) bool {
	lower := strings.ToLower(objet)
	for _, k := range keywords {
		if k = strings.TrimSpace(k); k != "" && !strings.Contains(lower, strings.ToLower(k)) {
			return false
		}
	}
	return true
}

// tabularResponse mirrors the data.gouv.fr tabular API shape.
type tabularResponse struct {
	Data []map[string]any `json:"data"`
	Meta struct {
		Page     int `json:"page"`
		PageSize int `json:"page_size"`
		Total    int `json:"total"`
	} `json:"meta"`
}

// fetchPage requests one page of a resource, optionally filtered by a keyword on
// the objet column plus the advanced criteria (CPV prefix, amount range, buyer
// SIREN). The advanced filters are pushed server-side via the tabular API's
// per-column operators so the API returns only matching rows.
func (c *Client) fetchPage(ctx context.Context, resourceID, keyword string, size, page int, q muninn.Query) (tabularResponse, error) {
	params := url.Values{}
	if keyword != "" {
		params.Set(searchField+"__contains", keyword)
	}
	// CPV: the API supports a "starts_with" operator on text columns.
	if len(q.CPVCodes) > 0 {
		var prefixes []string
		for _, c := range q.CPVCodes {
			c = strings.TrimSpace(c)
			if c == "" {
				continue
			}
			prefixes = append(prefixes, c)
		}
		switch len(prefixes) {
		case 1:
			params.Set("cpv__startswith", prefixes[0])
		default:
			// Multiple CPV prefixes combine as OR via the `or` parameter.
			var parts []string
			for _, p := range prefixes {
				parts = append(parts, fmt.Sprintf(`cpv__startswith="%s"`, p))
			}
			params.Set("or", "("+strings.Join(parts, ",")+")")
		}
	}
	// Amount range: BEAUAMP exposes several amount columns; the tabular API
	// only filters per column, so we narrow on the first present one. We pick
	// "valeur_totale" as the canonical column — the mapper falls back to other
	// columns when this one is missing, so a false positive (an awarded amount
	// recorded on a sibling column only) is the only consequence.
	if q.MontantMin > 0 {
		params.Set("valeur_totale__gte", strconv.FormatFloat(q.MontantMin, 'f', -1, 64))
	}
	if q.MontantMax > 0 {
		params.Set("valeur_totale__lte", strconv.FormatFloat(q.MontantMax, 'f', -1, 64))
	}
	if s := strings.TrimSpace(q.BuyerSIREN); s != "" {
		params.Set("siren_acheteur", s)
	}
	params.Set("page_size", strconv.Itoa(size))
	params.Set("page", strconv.Itoa(page))

	reqURL := c.tabularBase + resourceID + "/data/?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return tabularResponse{}, fmt.Errorf("beauamp: build request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return tabularResponse{}, fmt.Errorf("beauamp: request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return tabularResponse{}, fmt.Errorf("beauamp: unexpected status %d: %s", resp.StatusCode, body)
	}
	var parsed tabularResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return tabularResponse{}, fmt.Errorf("beauamp: decode response: %w", err)
	}
	return parsed, nil
}
