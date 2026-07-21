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

	// CPVCodes filters by CPV code prefix (e.g. ["72"], ["3019"]). A match
	// is true when any code in the tender starts with any of the requested
	// prefixes. Pushed server-side when the source exposes CPV as a filterable
	// column, otherwise applied post-fetch.
	CPVCodes []string

	// MontantMin bounds the awarded amount in euros (inclusive). Zero means
	// no lower bound.
	MontantMin float64

	// MontantMax bounds the awarded amount in euros (inclusive). Zero means
	// no upper bound. When MontantMin and MontantMax are both zero the amount
	// is unfiltered; either bound may be set independently.
	MontantMax float64

	// BuyerSIREN filters by the buyer's 9-digit SIREN (exact match). Empty
	// means no filter. SIREN (not SIRET) is the stable identifier across
	// establishments of the same legal entity.
	BuyerSIREN string

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