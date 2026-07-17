// Package consolidate combines several muninn providers into one, deduplicating
// and enriching their results.
//
// The typical setup pairs BEAUAMP (structured notices, with buyer and supplier
// names) with DECP (authoritative awarded amounts): querying both and merging by
// buyer SIREN and CPV yields notices that carry human-readable parties AND the
// reference amount. Records seen in several sources collapse into one Tender via
// Tender.DedupKey.
package consolidate

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/kvitrvn/muninn"
)

// Consolidator runs several providers for the same query and merges their
// results. It is itself a muninn.Provider.
type Consolidator struct {
	providers []muninn.Provider
}

// New builds a Consolidator over the given providers, queried in order.
func New(providers ...muninn.Provider) *Consolidator {
	return &Consolidator{providers: providers}
}

// Compile-time check: *Consolidator satisfies the muninn.Provider contract.
var _ muninn.Provider = (*Consolidator)(nil)

// Name implements muninn.Provider.
func (c *Consolidator) Name() string { return "consolidate" }

// Search queries every provider with q, then merges the combined results (see
// Merge). A provider hard error aborts and is returned with its provider name.
// Truncation is non-fatal: if any provider truncated, the merged records are
// returned together with a *muninn.ErrTruncated aggregating the shortfalls.
func (c *Consolidator) Search(ctx context.Context, q muninn.Query) ([]muninn.Tender, error) {
	var (
		all              []muninn.Tender
		retrieved, total int
		truncated        bool
	)
	for _, p := range c.providers {
		res, err := p.Search(ctx, q)
		var tr *muninn.ErrTruncated
		switch {
		case errors.As(err, &tr):
			truncated = true
			retrieved += tr.Retrieved
			total += tr.Total
		case err != nil:
			return nil, fmt.Errorf("consolidate: %s: %w", p.Name(), err)
		}
		all = append(all, res...)
	}

	merged := Merge(all)
	if truncated {
		return merged, &muninn.ErrTruncated{Retrieved: retrieved, Total: total}
	}
	return merged, nil
}

// Merge groups tenders by Tender.DedupKey and folds each group into a single
// enriched Tender. Field precedence favors completeness: the first non-empty
// value wins for text fields, CPV codes are unioned, an award type wins over an
// unknown one, and the amount prefers the DECP value (legally binding) over any
// other source's indicative figure. The output order is stable (by dedup key).
func Merge(tenders []muninn.Tender) []muninn.Tender {
	groups := map[string][]muninn.Tender{}
	var order []string
	for _, t := range tenders {
		k := t.DedupKey()
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], t)
	}
	sort.Strings(order)

	out := make([]muninn.Tender, 0, len(order))
	for _, k := range order {
		out = append(out, mergeGroup(groups[k]))
	}
	return out
}

// mergeGroup folds tenders that share a dedup key into one.
func mergeGroup(group []muninn.Tender) muninn.Tender {
	out := group[0]
	sources := map[string]bool{out.Source: true}
	for _, t := range group[1:] {
		sources[t.Source] = true
		out = enrich(out, t)
	}
	out.Source = joinSources(sources)
	return out
}

// enrich folds b into a, taking b's information only where it adds something.
func enrich(a, b muninn.Tender) muninn.Tender {
	// Amount: a DECP figure is authoritative and always wins; otherwise fill in
	// when a has none.
	switch {
	case b.Source == "decp" && b.MontantEstime > 0:
		a.MontantEstime = b.MontantEstime
	case a.MontantEstime == 0:
		a.MontantEstime = b.MontantEstime
	}

	a.Buyer = mergeBuyer(a.Buyer, b.Buyer)
	a.Supplier = mergeBuyer(a.Supplier, b.Supplier)

	if a.Titre == "" {
		a.Titre = b.Titre
	}
	if a.Objet == "" {
		a.Objet = b.Objet
	}
	if a.URL == "" {
		a.URL = b.URL
	}
	if a.SourceID == "" {
		a.SourceID = b.SourceID
	}
	if a.DatePublication.IsZero() {
		a.DatePublication = b.DatePublication
	}
	if a.DateLimiteReponse.IsZero() {
		a.DateLimiteReponse = b.DateLimiteReponse
	}
	if a.Procedure == muninn.ProcedureInconnue {
		a.Procedure = b.Procedure
	}
	if a.Engagement == muninn.EngagementInconnu {
		a.Engagement = b.Engagement
	}
	if a.AvisType == muninn.AvisInconnu || b.AvisType == muninn.AvisAttribution {
		if b.AvisType != muninn.AvisInconnu {
			a.AvisType = b.AvisType
		}
	}
	a.CPVCodes = unionStrings(a.CPVCodes, b.CPVCodes)
	return a
}

// mergeBuyer keeps the first non-empty value for each field, so a party known by
// name in one source and by SIRET in another ends up complete.
func mergeBuyer(a, b muninn.Buyer) muninn.Buyer {
	if a.Nom == "" {
		a.Nom = b.Nom
	}
	if a.SIREN == "" {
		a.SIREN = b.SIREN
	}
	if a.SIRET == "" {
		a.SIRET = b.SIRET
	}
	if a.Ville == "" {
		a.Ville = b.Ville
	}
	if a.CodeDepartement == "" {
		a.CodeDepartement = b.CodeDepartement
	}
	return a
}

// unionStrings appends the values of b not already in a, preserving order.
func unionStrings(a, b []string) []string {
	seen := map[string]bool{}
	for _, s := range a {
		seen[s] = true
	}
	for _, s := range b {
		if !seen[s] {
			seen[s] = true
			a = append(a, s)
		}
	}
	return a
}

// joinSources renders the contributing source set as a stable "a+b" string.
func joinSources(set map[string]bool) string {
	names := make([]string, 0, len(set))
	for s := range set {
		if s != "" {
			names = append(names, s)
		}
	}
	sort.Strings(names)
	return strings.Join(names, "+")
}
