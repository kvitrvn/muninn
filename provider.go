package muninn

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Query describes a federated procurement search. Criteria are combined with
// AND; values inside a slice (departments, CPV codes, notice types, statuses)
// are combined with OR.
type Query struct {
	Keywords []string

	// ObjetOnly restricts keyword matching to the normalized title/object.
	// When false, providers may use their native full-text search.
	ObjetOnly bool
	// MatchAll requires every keyword instead of at least one.
	MatchAll bool

	Departements []string

	PublishedFrom time.Time
	PublishedTo   time.Time
	DeadlineFrom  time.Time
	DeadlineTo    time.Time

	CPVCodes   []string
	MontantMin float64
	MontantMax float64
	BuyerSIREN string
	// SupplierSIREN matches any awarded contractor (titulaire) by its stable
	// 9-digit legal-entity identifier, including establishments with another
	// SIRET.
	SupplierSIREN string
	// SupplierSIRET matches one awarded contractor (titulaire) by its exact
	// 14-digit establishment identifier.
	SupplierSIRET string

	NoticeTypes []AvisType
	Statuses    []TenderStatus

	// OpenAt is the instant used to derive open/closed statuses. Its zero value
	// means "now"; Engine freezes that instant in the pagination cursor.
	OpenAt time.Time

	Sort     Sort
	PageSize int
	Cursor   string

	// CandidateLimit caps how many records each provider may collect before
	// federation. Zero uses the provider's safe default.
	CandidateLimit int

	// Enrichment enables optional, source-specific context on the page returned
	// by Engine. A nil value preserves the regular federated-search behaviour.
	// Enrichment never changes Items, Total, TotalExact, Partial or the main
	// Warnings collection.
	Enrichment *EnrichmentOptions
}

// Validate checks query invariants without performing I/O.
func (q Query) Validate() error {
	switch {
	case !q.PublishedFrom.IsZero() && !q.PublishedTo.IsZero() && q.PublishedFrom.After(q.PublishedTo):
		return &ValidationError{Field: "PublishedFrom", Problem: "must not be after PublishedTo"}
	case !q.DeadlineFrom.IsZero() && !q.DeadlineTo.IsZero() && q.DeadlineFrom.After(q.DeadlineTo):
		return &ValidationError{Field: "DeadlineFrom", Problem: "must not be after DeadlineTo"}
	case q.MontantMin < 0:
		return &ValidationError{Field: "MontantMin", Problem: "must be non-negative"}
	case q.MontantMax < 0:
		return &ValidationError{Field: "MontantMax", Problem: "must be non-negative"}
	case q.MontantMin > 0 && q.MontantMax > 0 && q.MontantMin > q.MontantMax:
		return &ValidationError{Field: "MontantMin", Problem: "must not exceed MontantMax"}
	case q.PageSize < 0:
		return &ValidationError{Field: "PageSize", Problem: "must be non-negative"}
	case q.PageSize > MaxPageSize:
		return &ValidationError{Field: "PageSize", Problem: fmt.Sprintf("must not exceed %d", MaxPageSize)}
	case q.CandidateLimit < 0:
		return &ValidationError{Field: "CandidateLimit", Problem: "must be non-negative"}
	}
	if q.Enrichment != nil {
		if err := q.Enrichment.Validate(); err != nil {
			return err
		}
	}

	if s := strings.TrimSpace(q.BuyerSIREN); s != "" && !isDigits(s, 9) {
		return &ValidationError{Field: "BuyerSIREN", Problem: "must contain exactly 9 digits"}
	}
	if s := strings.TrimSpace(q.SupplierSIREN); s != "" && !isDigits(s, 9) {
		return &ValidationError{Field: "SupplierSIREN", Problem: "must contain exactly 9 digits"}
	}
	if s := strings.TrimSpace(q.SupplierSIRET); s != "" && !isDigits(s, 14) {
		return &ValidationError{Field: "SupplierSIRET", Problem: "must contain exactly 14 digits"}
	}
	for _, typ := range q.NoticeTypes {
		if typ <= AvisInconnu || typ > AvisRectificatif {
			return &ValidationError{Field: "NoticeTypes", Problem: fmt.Sprintf("contains invalid value %d", typ)}
		}
	}
	for _, status := range q.Statuses {
		if status <= StatusUnknown || status > StatusAwarded {
			return &ValidationError{Field: "Statuses", Problem: fmt.Sprintf("contains invalid value %d", status)}
		}
	}
	if err := q.Sort.validate(); err != nil {
		return err
	}
	return nil
}

// ValidateProviderQuery rejects pagination and ordering fields that are owned
// by Engine. Built-in providers call it so direct usage never ignores them.
func ValidateProviderQuery(q Query) error {
	switch {
	case q.Cursor != "":
		return &ValidationError{Field: "Cursor", Problem: "is supported by Engine only"}
	case q.PageSize != 0:
		return &ValidationError{Field: "PageSize", Problem: "is supported by Engine only"}
	case q.Sort.Field != "" || q.Sort.Direction != "":
		return &ValidationError{Field: "Sort", Problem: "is supported by Engine only"}
	case q.Enrichment != nil:
		return &ValidationError{Field: "Enrichment", Problem: "is supported by Engine only"}
	default:
		return nil
	}
}

func isDigits(s string, length int) bool {
	if len(s) != length {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// ValidationError identifies one invalid query field.
type ValidationError struct {
	Field   string
	Problem string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("muninn: invalid query field %s: %s", e.Field, e.Problem)
}

// ProviderResult is the normalized response produced by one data source.
// Total describes matches from that provider before cross-source merging.
type ProviderResult struct {
	Items      []Tender
	Total      int
	TotalExact bool
	Truncated  bool
}

// Provider is the contract implemented by every procurement source.
type Provider interface {
	Name() string
	Capabilities() Capabilities
	Search(ctx context.Context, q Query) (ProviderResult, error)
}

// Enricher is an optional secondary source detected by Engine. Implementations
// receive only the already sorted and paginated primary results. They must not
// mutate those tenders or use their data to alter the primary search result.
type Enricher interface {
	Name() string
	Enrich(
		ctx context.Context,
		items []Tender,
		options EnrichmentOptions,
		openAt time.Time,
	) (EnrichmentResult, error)
}
