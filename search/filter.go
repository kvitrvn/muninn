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

// AdvancedFilter mirrors the advanced criteria of muninn.Query (CPV prefixes,
// amount range, buyer SIREN). It is applied client-side after a Search that
// could not push every filter server-side. A zero-value AdvancedFilter is a
// no-op.
type AdvancedFilter struct {
	CPVCodes   []string
	MontantMin float64
	MontantMax float64
	BuyerSIREN string
}

// Apply returns the tenders satisfying every populated criterion. Empty
// criteria are skipped, so a partially populated filter only narrows on the
// axes that are set.
func (f AdvancedFilter) Apply(tenders []muninn.Tender) []muninn.Tender {
	if len(f.CPVCodes) == 0 && f.MontantMin <= 0 && f.MontantMax <= 0 && strings.TrimSpace(f.BuyerSIREN) == "" {
		return tenders
	}
	out := make([]muninn.Tender, 0, len(tenders))
	for _, t := range tenders {
		if !f.matchesCPV(t) {
			continue
		}
		if !f.matchesAmount(t) {
			continue
		}
		if !f.matchesSIREN(t) {
			continue
		}
		out = append(out, t)
	}
	return out
}

func (f AdvancedFilter) matchesCPV(t muninn.Tender) bool {
	if len(f.CPVCodes) == 0 {
		return true
	}
	for _, want := range f.CPVCodes {
		for _, have := range t.CPVCodes {
			if strings.HasPrefix(have, want) {
				return true
			}
		}
	}
	return false
}

func (f AdvancedFilter) matchesAmount(t muninn.Tender) bool {
	if f.MontantMin <= 0 && f.MontantMax <= 0 {
		return true
	}
	if f.MontantMin > 0 && t.MontantEstime < f.MontantMin {
		return false
	}
	if f.MontantMax > 0 && t.MontantEstime > f.MontantMax {
		return false
	}
	return true
}

func (f AdvancedFilter) matchesSIREN(t muninn.Tender) bool {
	want := strings.TrimSpace(f.BuyerSIREN)
	if want == "" {
		return true
	}
	return t.Buyer.SIREN9() == want
}