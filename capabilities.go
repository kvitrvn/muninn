package muninn

import "fmt"

// Filter identifies one independently supportable query feature.
type Filter string

const (
	FilterTitleKeywords Filter = "title_keywords"
	FilterFullText      Filter = "full_text"
	FilterDepartments   Filter = "departments"
	FilterPublication   Filter = "publication_date"
	FilterDeadline      Filter = "deadline"
	FilterCPV           Filter = "cpv"
	FilterAmount        Filter = "amount"
	FilterBuyerSIREN    Filter = "buyer_siren"
	FilterNoticeType    Filter = "notice_type"
	FilterStatusOpen    Filter = "status_open"
	FilterStatusClosed  Filter = "status_closed"
	FilterStatusAwarded Filter = "status_awarded"
)

// SupportLevel describes how reliably a provider can honor a filter.
type SupportLevel uint8

const (
	Unsupported SupportLevel = iota
	Approximate
	Exact
)

// Capabilities declares provider behavior. Missing entries are unsupported.
type Capabilities map[Filter]SupportLevel

// Support returns the declared support level for a filter.
func (c Capabilities) Support(filter Filter) SupportLevel {
	return c[filter]
}

// UnsupportedFilterError is returned when a provider is called directly with
// a criterion it cannot honor.
type UnsupportedFilterError struct {
	Filter Filter
}

func (e *UnsupportedFilterError) Error() string {
	return fmt.Sprintf("muninn: unsupported filter %s", e.Filter)
}

// ValidateCapabilities checks that caps can execute every criterion in q.
// Approximate support is accepted and discoverable through Capabilities.
func ValidateCapabilities(q Query, caps Capabilities) error {
	for _, filter := range q.requiredFilters() {
		if caps.Support(filter) == Unsupported {
			return &UnsupportedFilterError{Filter: filter}
		}
	}
	return nil
}

func (q Query) requiredFilters() []Filter {
	var out []Filter
	if len(q.Keywords) > 0 {
		if q.ObjetOnly {
			out = append(out, FilterTitleKeywords)
		} else {
			out = append(out, FilterFullText)
		}
	}
	if len(q.Departements) > 0 {
		out = append(out, FilterDepartments)
	}
	if !q.PublishedFrom.IsZero() || !q.PublishedTo.IsZero() {
		out = append(out, FilterPublication)
	}
	if !q.DeadlineFrom.IsZero() || !q.DeadlineTo.IsZero() {
		out = append(out, FilterDeadline)
	}
	if len(q.CPVCodes) > 0 {
		out = append(out, FilterCPV)
	}
	if q.MontantMin > 0 || q.MontantMax > 0 {
		out = append(out, FilterAmount)
	}
	if q.BuyerSIREN != "" {
		out = append(out, FilterBuyerSIREN)
	}
	if len(q.NoticeTypes) > 0 {
		out = append(out, FilterNoticeType)
	}
	for _, status := range q.Statuses {
		switch status {
		case StatusOpen:
			out = appendUniqueFilter(out, FilterStatusOpen)
		case StatusClosed:
			out = appendUniqueFilter(out, FilterStatusClosed)
		case StatusAwarded:
			out = appendUniqueFilter(out, FilterStatusAwarded)
		}
	}
	return out
}

func appendUniqueFilter(filters []Filter, filter Filter) []Filter {
	for _, existing := range filters {
		if existing == filter {
			return filters
		}
	}
	return append(filters, filter)
}
