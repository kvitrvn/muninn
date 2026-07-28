package muninn

import "fmt"

const (
	// DefaultPageSize is used by Engine when Query.PageSize is zero.
	DefaultPageSize = 50
	// MaxPageSize prevents a caller from accidentally materializing an
	// unbounded response.
	MaxPageSize = 200
)

// SortField selects the deterministic ordering applied after federation.
type SortField string

const (
	SortByPublication SortField = "publication"
	SortByDeadline    SortField = "deadline"
)

// SortDirection controls ascending or descending ordering.
type SortDirection string

const (
	SortAscending  SortDirection = "asc"
	SortDescending SortDirection = "desc"
)

// Sort describes the ordering of a federated result. The zero value means
// publication date, newest first.
type Sort struct {
	Field     SortField
	Direction SortDirection
}

func (s Sort) normalized() Sort {
	if s.Field == "" {
		s.Field = SortByPublication
	}
	if s.Direction == "" {
		if s.Field == SortByDeadline {
			s.Direction = SortAscending
		} else {
			s.Direction = SortDescending
		}
	}
	return s
}

func (s Sort) validate() error {
	s = s.normalized()
	if s.Field != SortByPublication && s.Field != SortByDeadline {
		return &ValidationError{Field: "Sort.Field", Problem: fmt.Sprintf("unsupported value %q", s.Field)}
	}
	if s.Direction != SortAscending && s.Direction != SortDescending {
		return &ValidationError{Field: "Sort.Direction", Problem: fmt.Sprintf("unsupported value %q", s.Direction)}
	}
	return nil
}

// WarningCode is a stable machine-readable reason why a federated response is
// partial or approximate.
type WarningCode string

const (
	WarningProviderError     WarningCode = "provider_error"
	WarningUnsupportedFilter WarningCode = "unsupported_filter"
	WarningApproximateFilter WarningCode = "approximate_filter"
	WarningTruncated         WarningCode = "truncated"
)

// Warning describes a non-fatal source-level problem.
type Warning struct {
	Provider string
	Code     WarningCode
	Message  string
	Err      error
}

// SearchResult is the stable public response returned by Engine.
type SearchResult struct {
	Items      []Tender
	Total      int
	TotalExact bool
	NextCursor string
	Partial    bool
	Warnings   []Warning
}
