package muninn

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	// ErrNoProviders means Engine was constructed without a source.
	ErrNoProviders = errors.New("muninn: no providers configured")
	// ErrNoCapableProvider means every provider was incompatible with at least
	// one requested filter.
	ErrNoCapableProvider = errors.New("muninn: no provider supports the query")
)

// Engine federates providers, consolidates their records, applies normalized
// filters, and returns a deterministic page.
type Engine struct {
	providers []Provider
	now       func() time.Time
}

// NewEngine constructs a federated search engine.
func NewEngine(providers ...Provider) *Engine {
	kept := make([]Provider, 0, len(providers))
	for _, provider := range providers {
		if provider != nil {
			kept = append(kept, provider)
		}
	}
	return &Engine{
		providers: kept,
		now:       time.Now,
	}
}

// Search queries compatible providers concurrently. A provider failure is
// non-fatal when another provider succeeds; the response is then marked
// partial and carries a machine-readable warning.
func (e *Engine) Search(ctx context.Context, q Query) (SearchResult, error) {
	if len(e.providers) == 0 {
		return SearchResult{}, ErrNoProviders
	}

	cursor, err := decodeCursor(q.Cursor)
	if err != nil {
		return SearchResult{}, err
	}
	evaluatedAt := q.OpenAt
	if evaluatedAt.IsZero() {
		if cursor.EvaluatedAt != 0 {
			evaluatedAt = time.Unix(0, cursor.EvaluatedAt).UTC()
		} else {
			evaluatedAt = e.now().UTC()
		}
		q.OpenAt = evaluatedAt
	}
	if err := q.Validate(); err != nil {
		return SearchResult{}, err
	}

	q.Sort = q.Sort.normalized()
	fingerprint := queryFingerprint(q)
	if cursor.Query != "" && cursor.Query != fingerprint {
		return SearchResult{}, &ValidationError{Field: "Cursor", Problem: "does not belong to this query"}
	}

	type providerCall struct {
		index    int
		provider Provider
		query    Query
		warnings []Warning
		skip     bool
	}
	calls := make([]providerCall, len(e.providers))
	for index, provider := range e.providers {
		calls[index] = providerCall{index: index, provider: provider, query: q}
		var supportedStatuses []TenderStatus
		for _, status := range q.Statuses {
			filter := statusFilter(status)
			support := provider.Capabilities().Support(filter)
			if support == Unsupported {
				calls[index].warnings = append(calls[index].warnings, Warning{
					Provider: provider.Name(),
					Code:     WarningUnsupportedFilter,
					Message:  fmt.Sprintf("status %s is unsupported by this provider", status),
				})
				continue
			}
			if support == Approximate {
				calls[index].warnings = append(calls[index].warnings, Warning{
					Provider: provider.Name(),
					Code:     WarningApproximateFilter,
					Message:  fmt.Sprintf("status %s is approximate", status),
				})
			}
			supportedStatuses = append(supportedStatuses, status)
		}
		if len(q.Statuses) > 0 {
			calls[index].query.Statuses = supportedStatuses
			if len(supportedStatuses) == 0 {
				calls[index].skip = true
			}
		}
		for _, filter := range q.requiredFilters() {
			if isStatusFilter(filter) {
				continue
			}
			switch provider.Capabilities().Support(filter) {
			case Unsupported:
				calls[index].skip = true
				calls[index].warnings = append(calls[index].warnings, Warning{
					Provider: provider.Name(),
					Code:     WarningUnsupportedFilter,
					Message:  fmt.Sprintf("provider skipped: filter %s is unsupported", filter),
				})
			case Approximate:
				calls[index].warnings = append(calls[index].warnings, Warning{
					Provider: provider.Name(),
					Code:     WarningApproximateFilter,
					Message:  fmt.Sprintf("filter %s is approximate", filter),
				})
			}
		}
	}

	type providerResponse struct {
		result ProviderResult
		err    error
	}
	responses := make([]providerResponse, len(calls))
	var wg sync.WaitGroup
	for _, call := range calls {
		if call.skip {
			continue
		}
		wg.Add(1)
		go func(call providerCall) {
			defer wg.Done()
			providerQuery := call.query
			providerQuery.Cursor = ""
			providerQuery.PageSize = 0
			providerQuery.Sort = Sort{}
			responses[call.index].result, responses[call.index].err = call.provider.Search(ctx, providerQuery)
		}(call)
	}
	wg.Wait()

	if err := ctx.Err(); err != nil {
		return SearchResult{}, fmt.Errorf("muninn: search canceled: %w", err)
	}

	var (
		all       []Tender
		warnings  []Warning
		hardErrs  []error
		attempted int
		succeeded int
	)
	for _, call := range calls {
		warnings = append(warnings, call.warnings...)
		if call.skip {
			continue
		}
		attempted++
		response := responses[call.index]
		if response.err != nil {
			warnings = append(warnings, Warning{
				Provider: call.provider.Name(),
				Code:     WarningProviderError,
				Message:  "provider search failed",
				Err:      response.err,
			})
			hardErrs = append(hardErrs, fmt.Errorf("%s: %w", call.provider.Name(), response.err))
		} else {
			succeeded++
		}
		if response.result.Truncated {
			warnings = append(warnings, Warning{
				Provider: call.provider.Name(),
				Code:     WarningTruncated,
				Message:  fmt.Sprintf("provider returned %d of %d matches", len(response.result.Items), response.result.Total),
			})
		}
		all = append(all, response.result.Items...)
	}

	if attempted == 0 {
		return SearchResult{Partial: true, Warnings: warnings}, ErrNoCapableProvider
	}
	if succeeded == 0 && len(all) == 0 {
		return SearchResult{Partial: true, Warnings: warnings}, fmt.Errorf("muninn: all providers failed: %w", errors.Join(hardErrs...))
	}

	items := MergeTenders(all)
	items = FilterTenders(items, q, evaluatedAt)
	sortTenders(items, q.Sort)

	start := 0
	if cursor.Key != "" {
		start = -1
		for index := range items {
			if items[index].DedupKey() == cursor.Key {
				start = index + 1
				break
			}
		}
		if start < 0 {
			return SearchResult{}, &ValidationError{Field: "Cursor", Problem: "is stale"}
		}
	}

	pageSize := q.PageSize
	if pageSize == 0 {
		pageSize = DefaultPageSize
	}
	end := min(start+pageSize, len(items))
	page := append([]Tender(nil), items[start:end]...)

	var next string
	if end < len(items) && len(page) > 0 {
		next, err = encodeCursor(pageCursor{
			Version:     1,
			Query:       fingerprint,
			Key:         page[len(page)-1].DedupKey(),
			EvaluatedAt: evaluatedAt.UnixNano(),
		})
		if err != nil {
			return SearchResult{}, fmt.Errorf("muninn: encode cursor: %w", err)
		}
	}

	partial := len(warnings) > 0
	return SearchResult{
		Items:      page,
		Total:      len(items),
		TotalExact: !partial,
		NextCursor: next,
		Partial:    partial,
		Warnings:   warnings,
	}, nil
}

func statusFilter(status TenderStatus) Filter {
	switch status {
	case StatusOpen:
		return FilterStatusOpen
	case StatusClosed:
		return FilterStatusClosed
	case StatusAwarded:
		return FilterStatusAwarded
	default:
		return ""
	}
}

func isStatusFilter(filter Filter) bool {
	return filter == FilterStatusOpen || filter == FilterStatusClosed || filter == FilterStatusAwarded
}

type pageCursor struct {
	Version     int    `json:"v"`
	Query       string `json:"q"`
	Key         string `json:"k"`
	EvaluatedAt int64  `json:"at"`
}

func decodeCursor(raw string) (pageCursor, error) {
	if raw == "" {
		return pageCursor{}, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return pageCursor{}, &ValidationError{Field: "Cursor", Problem: "is not valid base64url"}
	}
	var cursor pageCursor
	if err := json.Unmarshal(data, &cursor); err != nil || cursor.Version != 1 || cursor.Key == "" {
		return pageCursor{}, &ValidationError{Field: "Cursor", Problem: "has an invalid payload"}
	}
	return cursor, nil
}

func encodeCursor(cursor pageCursor) (string, error) {
	data, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func queryFingerprint(q Query) string {
	q.Cursor = ""
	q.PageSize = 0
	data, _ := json.Marshal(q)
	sum := sha256.Sum256(data)
	return base64.RawURLEncoding.EncodeToString(sum[:12])
}

func sortTenders(tenders []Tender, sorting Sort) {
	sorting = sorting.normalized()
	sort.SliceStable(tenders, func(i, j int) bool {
		a, b := tenders[i], tenders[j]
		var av, bv time.Time
		if sorting.Field == SortByDeadline {
			av, bv = a.DateLimiteReponse, b.DateLimiteReponse
		} else {
			av, bv = a.DatePublication, b.DatePublication
		}
		if av.IsZero() != bv.IsZero() {
			return !av.IsZero()
		}
		if !av.Equal(bv) {
			if sorting.Direction == SortAscending {
				return av.Before(bv)
			}
			return av.After(bv)
		}
		return strings.Compare(a.DedupKey(), b.DedupKey()) < 0
	})
}
