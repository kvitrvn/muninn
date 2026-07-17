// Package search provides client-side filters over already-fetched
// []muninn.Tender, complementing the filters each provider pushes server-side
// (useful when a source does not natively support every criterion, or to refine
// results after aggregation).
package search

import (
	"strings"

	"github.com/kvitrvn/muninn"
)

// Filter applies keyword/CPV criteria to a list of Tenders and returns the
// matching subset.
type Filter struct {
	Keywords []string // matches if at least one keyword is present (OR)
	CPVCodes []string // matches if at least one CPV prefix matches
}

// Apply returns the tenders satisfying the filter. A Filter with no criterion
// returns the list unchanged.
func (f Filter) Apply(tenders []muninn.Tender) []muninn.Tender {
	if len(f.Keywords) == 0 && len(f.CPVCodes) == 0 {
		return tenders
	}
	out := make([]muninn.Tender, 0, len(tenders))
	for _, t := range tenders {
		if f.matches(t) {
			out = append(out, t)
		}
	}
	return out
}

func (f Filter) matches(t muninn.Tender) bool {
	if len(f.Keywords) > 0 && !f.matchesKeywords(t) {
		return false
	}
	if len(f.CPVCodes) > 0 && !f.matchesCPV(t) {
		return false
	}
	return true
}

func (f Filter) matchesKeywords(t muninn.Tender) bool {
	haystack := strings.ToLower(t.Titre + " " + t.Objet)
	for _, kw := range f.Keywords {
		if strings.Contains(haystack, strings.ToLower(kw)) {
			return true
		}
	}
	return false
}

func (f Filter) matchesCPV(t muninn.Tender) bool {
	for _, want := range f.CPVCodes {
		for _, have := range t.CPVCodes {
			if strings.HasPrefix(have, want) {
				return true
			}
		}
	}
	return false
}
