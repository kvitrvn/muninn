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
//     by issuing one query each and unioning; MatchAll is applied client-side.
//   - Large yearly files are not served by the tabular API; only daily/monthly
//     resources are queryable, which bounds how far back a query reaches.
//   - The dataset has no department column; direct calls reject that filter.
//   - Full-text requests are approximate because only objet can be searched.
package beauamp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kvitrvn/muninn"
	"github.com/kvitrvn/muninn/internal/httpx"
	"github.com/kvitrvn/muninn/retry"
)

const (
	defaultTabularBase = "https://tabular-api.data.gouv.fr/api/resources/"
	defaultCatalogBase = "https://www.data.gouv.fr/api/1/datasets/"
	defaultSlug        = "base-etendue-amelioree-et-unifiee-des-annonces-des-marches-publics"
	// searchField is the column filtered on; the tabular API has no full-text.
	searchField = "objet"
	// pageSize is the number of rows requested per tabular API page.
	pageSize = 100
	// maxFetch bounds Search when the caller sets no CandidateLimit, to avoid
	// pulling an entire resource.
	maxFetch = 10000
	// defaultResourceCacheTTL bounds how long the resolved monthly-resource
	// catalog is reused before being refetched. The catalog only changes once a
	// month (a new monthly CSV appears), so this mostly collapses repeated
	// Search calls within a process into a single catalog fetch.
	defaultResourceCacheTTL = time.Hour
)

var errResourceNotIndexed = errors.New("beauamp: resource not indexed")

// Client queries BEAUAMP through the data.gouv.fr tabular API.
type Client struct {
	tabularBase string
	catalogBase string
	slug        string
	http        *http.Client
	resources   []string // explicit resource ids; empty means resolve from catalog
	retry       retry.Policy

	cacheTTL time.Duration // <= 0 disables caching

	cacheMu      sync.Mutex
	cachedMonths []monthlyResource
	cachedAt     time.Time
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

// WithRetryPolicy overrides the retry/backoff policy applied to requests
// against both the tabular API and the catalog when the server responds with
// 429/5xx. The default is retry.DefaultRetryPolicy.
func WithRetryPolicy(p retry.Policy) Option {
	return func(c *Client) { c.retry = p }
}

// WithResourceCacheTTL overrides how long the resolved monthly-resource
// catalog (see resolveResources) is cached before being refetched. The
// default is one hour. A value <= 0 disables caching, so every Search
// call resolves the catalog fresh — useful for a one-shot CLI or a test that
// wants to observe every catalog request.
func WithResourceCacheTTL(d time.Duration) Option {
	return func(c *Client) { c.cacheTTL = d }
}

// New creates a BEAUAMP client.
func New(opts ...Option) *Client {
	c := &Client{
		tabularBase: defaultTabularBase,
		catalogBase: defaultCatalogBase,
		slug:        defaultSlug,
		http:        &http.Client{Timeout: 30 * time.Second},
		cacheTTL:    defaultResourceCacheTTL,
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

// Capabilities declares the guarantees of the tabular BEAUAMP source.
func (c *Client) Capabilities() muninn.Capabilities {
	return muninn.Capabilities{
		muninn.FilterTitleKeywords: muninn.Exact,
		muninn.FilterFullText:      muninn.Approximate,
		muninn.FilterPublication:   muninn.Approximate,
		muninn.FilterCPV:           muninn.Exact,
		muninn.FilterAmount:        muninn.Approximate,
		muninn.FilterBuyerSIREN:    muninn.Exact,
		muninn.FilterSupplierSIREN: muninn.Approximate,
		muninn.FilterNoticeType:    muninn.Approximate,
		muninn.FilterStatusAwarded: muninn.Approximate,
	}
}

// Search fetches the matching notices across the resolved resources, maps them
// to Tenders, deduplicates them (a notice may recur across resources or match
// several keywords), and — when q.MatchAll is set — keeps only those whose objet
// contains every keyword. The advanced filters (CPV, amount range, buyer SIREN)
// are pushed into each per-keyword tabular query so the API narrows the page
// before it ships. ProviderResult.Truncated is set when CandidateLimit or
// maxFetch is reached before exhausting the matches.
func (c *Client) Search(ctx context.Context, q muninn.Query) (muninn.ProviderResult, error) {
	if err := muninn.ValidateProviderQuery(q); err != nil {
		return muninn.ProviderResult{}, err
	}
	if err := q.Validate(); err != nil {
		return muninn.ProviderResult{}, err
	}
	if err := muninn.ValidateCapabilities(q, c.Capabilities()); err != nil {
		return muninn.ProviderResult{}, err
	}
	resources, err := c.resolveResources(ctx, q)
	if err != nil {
		return muninn.ProviderResult{}, err
	}
	terms := keywords(q)
	cpvFilters := tabularCPVFilters(q.CPVCodes)

	limit := maxFetch
	if q.CandidateLimit > 0 && q.CandidateLimit < limit {
		limit = q.CandidateLimit
	}

	var (
		out        []muninn.Tender
		indexByKey = map[string]int{}
		rawTotal   int
		truncated  bool
	)
resourcesLoop:
	for _, res := range resources {
		for _, term := range terms {
			for _, cpvFilter := range cpvFilters {
				for page := 1; ; page++ {
					resp, err := c.fetchPage(ctx, res, term, cpvFilter, pageSize, page, q)
					if err != nil {
						if errors.Is(err, errResourceNotIndexed) {
							continue resourcesLoop
						}
						return muninn.ProviderResult{
							Items:      out,
							Total:      len(out),
							TotalExact: false,
							Truncated:  true,
						}, err
					}
					if page == 1 {
						rawTotal += resp.Meta.Total
					}
					for _, rec := range resp.Data {
						t := mapRecord(rec)
						if q.MatchAll && !containsAll(t.Objet, q.Keywords) {
							continue
						}
						key := t.DedupKey()
						if index, exists := indexByKey[key]; exists {
							out[index] = muninn.MergeTenders([]muninn.Tender{out[index], t})[0]
							continue
						}
						indexByKey[key] = len(out)
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
			break
		}
	}

	at := q.OpenAt
	if at.IsZero() {
		at = time.Now()
	}
	out = muninn.FilterTenders(out, q, at)
	total := len(out)
	if truncated && rawTotal > total {
		total = rawTotal
	}
	return muninn.ProviderResult{
		Items:      out,
		Total:      total,
		TotalExact: !truncated,
		Truncated:  truncated,
	}, nil
}

type tabularCPVFilter struct {
	parameter string
	value     string
}

// tabularCPVFilters uses __in for canonical full CPV codes. Prefix searches
// fall back to __contains because the tabular API exposes no starts-with
// operator; FilterTenders applies the authoritative prefix check afterwards.
func tabularCPVFilters(codes []string) []tabularCPVFilter {
	var prefixes []string
	seen := map[string]bool{}
	allFull := true
	for _, code := range codes {
		code = strings.TrimSpace(code)
		if code == "" || seen[code] {
			continue
		}
		seen[code] = true
		prefixes = append(prefixes, code)
		allFull = allFull && isFullCPV(code)
	}
	if len(prefixes) == 0 {
		return []tabularCPVFilter{{}}
	}
	if allFull {
		return []tabularCPVFilter{{
			parameter: "cpv__in",
			value:     strings.Join(prefixes, ","),
		}}
	}
	filters := make([]tabularCPVFilter, 0, len(prefixes))
	for _, prefix := range prefixes {
		filters = append(filters, tabularCPVFilter{
			parameter: "cpv__contains",
			value:     prefix,
		})
	}
	return filters
}

func isFullCPV(value string) bool {
	if len(value) != 8 {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
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
// and supplier SIREN). The advanced filters are pushed server-side via the
// tabular API's per-column operators so the API returns only matching rows.
func (c *Client) fetchPage(
	ctx context.Context,
	resourceID, keyword string,
	cpvFilter tabularCPVFilter,
	size, page int,
	q muninn.Query,
) (tabularResponse, error) {
	params := url.Values{}
	if keyword != "" {
		params.Set(searchField+"__contains", keyword)
	}
	if cpvFilter.parameter != "" {
		params.Set(cpvFilter.parameter, cpvFilter.value)
	}
	// Amount range: BEAUAMP exposes several amount columns; the tabular API
	// only filters per column, so we narrow on the first present one. We pick
	// "valeur_totale" as the canonical column — the mapper falls back to other
	// columns when this one is missing, so a false positive (an awarded amount
	// recorded on a sibling column only) is the only consequence.
	if q.MontantMin > 0 {
		params.Set("valeur_totale__greater", strconv.FormatFloat(q.MontantMin, 'f', -1, 64))
	}
	if q.MontantMax > 0 {
		params.Set("valeur_totale__less", strconv.FormatFloat(q.MontantMax, 'f', -1, 64))
	}
	if s := strings.TrimSpace(q.BuyerSIREN); s != "" {
		params.Set("siren_acheteur__exact", s)
	}
	if s := strings.TrimSpace(q.SupplierSIREN); s != "" {
		params.Set("siren_fournisseur__exact", s)
	}
	params.Set("page_size", strconv.Itoa(size))
	params.Set("page", strconv.Itoa(page))

	reqURL := c.tabularBase + resourceID + "/data/?" + params.Encode()
	resp, err := httpx.Do(ctx, c.http, c.retry, func(ctx context.Context) (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	})
	if err != nil {
		return tabularResponse{}, fmt.Errorf("beauamp: request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return tabularResponse{}, fmt.Errorf("%w: %s", errResourceNotIndexed, body)
	}
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
