// Package consolidate provides a provider-compatible merger. New applications
// normally use muninn.Engine directly; this package remains useful when a
// merged set must itself be exposed as one muninn.Provider.
package consolidate

import (
	"context"
	"fmt"
	"sync"

	"github.com/kvitrvn/muninn"
)

// Consolidator queries providers concurrently and merges their records.
type Consolidator struct {
	providers []muninn.Provider
}

func New(providers ...muninn.Provider) *Consolidator {
	return &Consolidator{providers: append([]muninn.Provider(nil), providers...)}
}

var _ muninn.Provider = (*Consolidator)(nil)

func (c *Consolidator) Name() string { return "consolidate" }

// Capabilities returns the intersection of provider capabilities. A filter is
// exact only when every provider supports it exactly; any approximate support
// makes the consolidated capability approximate.
func (c *Consolidator) Capabilities() muninn.Capabilities {
	if len(c.providers) == 0 {
		return muninn.Capabilities{}
	}
	out := muninn.Capabilities{}
	filters := []muninn.Filter{
		muninn.FilterTitleKeywords,
		muninn.FilterFullText,
		muninn.FilterDepartments,
		muninn.FilterPublication,
		muninn.FilterDeadline,
		muninn.FilterCPV,
		muninn.FilterAmount,
		muninn.FilterBuyerSIREN,
		muninn.FilterNoticeType,
		muninn.FilterStatusOpen,
		muninn.FilterStatusClosed,
		muninn.FilterStatusAwarded,
	}
	for _, filter := range filters {
		level := muninn.Exact
		for _, provider := range c.providers {
			support := provider.Capabilities().Support(filter)
			if support == muninn.Unsupported {
				level = muninn.Unsupported
				break
			}
			if support == muninn.Approximate {
				level = muninn.Approximate
			}
		}
		if level != muninn.Unsupported {
			out[filter] = level
		}
	}
	return out
}

func (c *Consolidator) Search(ctx context.Context, q muninn.Query) (muninn.ProviderResult, error) {
	if len(c.providers) == 0 {
		return muninn.ProviderResult{}, muninn.ErrNoProviders
	}
	if err := muninn.ValidateProviderQuery(q); err != nil {
		return muninn.ProviderResult{}, err
	}
	if err := q.Validate(); err != nil {
		return muninn.ProviderResult{}, err
	}
	if err := muninn.ValidateCapabilities(q, c.Capabilities()); err != nil {
		return muninn.ProviderResult{}, err
	}

	results := make([]muninn.ProviderResult, len(c.providers))
	errs := make([]error, len(c.providers))
	var wg sync.WaitGroup
	for index, provider := range c.providers {
		wg.Add(1)
		go func(index int, provider muninn.Provider) {
			defer wg.Done()
			results[index], errs[index] = provider.Search(ctx, q)
		}(index, provider)
	}
	wg.Wait()

	var (
		all       []muninn.Tender
		truncated bool
		exact     = true
	)
	for index, provider := range c.providers {
		if errs[index] != nil {
			return muninn.ProviderResult{}, fmt.Errorf("consolidate: %s: %w", provider.Name(), errs[index])
		}
		all = append(all, results[index].Items...)
		truncated = truncated || results[index].Truncated
		exact = exact && results[index].TotalExact
	}
	items := muninn.MergeTenders(all)
	return muninn.ProviderResult{
		Items:      items,
		Total:      len(items),
		TotalExact: exact && !truncated,
		Truncated:  truncated,
	}, nil
}

// Merge is kept as a focused convenience for callers with already-fetched
// records.
func Merge(tenders []muninn.Tender) []muninn.Tender {
	return muninn.MergeTenders(tenders)
}
