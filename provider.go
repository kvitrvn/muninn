package muninn

import (
	"context"
	"time"
)

// Query describes a public procurement search, independent of the source.
// An empty/nil field means "no filter on this axis"; the criteria combine.
type Query struct {
	// Keywords are the searched terms (e.g. "GED", "gestion documentaire").
	// See ObjetOnly and MatchAll for how they are combined and applied.
	Keywords []string

	// ObjetOnly restricts keywords to the notice title only (the objet field),
	// which is far more precise. When false, the search is full-text over the
	// whole notice (high recall but noisy).
	ObjetOnly bool

	// MatchAll combines keywords with AND (intersection) instead of OR
	// (union, the default).
	MatchAll bool

	// Departements filters by the buyer's department code (e.g. "75", "69").
	Departements []string

	DateFrom time.Time
	DateTo   time.Time

	// Limit caps the number of results returned by a provider
	// (0 = provider default).
	Limit int
}

// Provider is the common contract implemented by every procurement data source
// (BOAMP today; DECP, PLACE... later). Implementing it is enough to plug a new
// source into the library.
type Provider interface {
	// Name returns the provider's short identifier (e.g. "boamp").
	Name() string
	// Search queries the source and returns the Tenders matching q.
	Search(ctx context.Context, q Query) ([]Tender, error)
}
