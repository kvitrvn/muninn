package muninn

import (
	"strings"
	"time"
)

// FilterTenders applies every criterion that can be checked on the normalized
// model. Providers still perform native filtering first; this second pass gives
// federated results consistent semantics.
func FilterTenders(tenders []Tender, q Query, at time.Time) []Tender {
	out := make([]Tender, 0, len(tenders))
	for _, tender := range tenders {
		if matchesTender(tender, q, at) {
			out = append(out, tender)
		}
	}
	return out
}

func matchesTender(t Tender, q Query, at time.Time) bool {
	if q.ObjetOnly && !matchesKeywords(t, q.Keywords, q.MatchAll) {
		return false
	}
	if !matchesAnyString(t.Buyer.CodeDepartement, q.Departements) {
		return false
	}
	if !withinDate(t.DatePublication, q.PublishedFrom, q.PublishedTo) {
		return false
	}
	if !withinDate(t.DateLimiteReponse, q.DeadlineFrom, q.DeadlineTo) {
		return false
	}
	if !matchesCPV(t.CPVCodes, q.CPVCodes) {
		return false
	}
	if !matchesAmount(t.MontantEstime, q.MontantMin, q.MontantMax) {
		return false
	}
	if q.BuyerSIREN != "" && t.Buyer.SIREN9() != strings.TrimSpace(q.BuyerSIREN) {
		return false
	}
	if !matchesNoticeType(t.AvisType, q.NoticeTypes) {
		return false
	}
	if !matchesStatus(t.StatusAt(at), q.Statuses) {
		return false
	}
	return true
}

func matchesKeywords(t Tender, keywords []string, all bool) bool {
	var terms []string
	for _, keyword := range keywords {
		if keyword = strings.TrimSpace(keyword); keyword != "" {
			terms = append(terms, strings.ToLower(keyword))
		}
	}
	if len(terms) == 0 {
		return true
	}
	haystack := strings.ToLower(firstNonEmpty(t.Objet, t.Titre))
	if all {
		for _, term := range terms {
			if !strings.Contains(haystack, term) {
				return false
			}
		}
		return true
	}
	for _, term := range terms {
		if strings.Contains(haystack, term) {
			return true
		}
	}
	return false
}

func matchesAnyString(value string, wants []string) bool {
	if len(wants) == 0 {
		return true
	}
	for _, want := range wants {
		if strings.TrimSpace(want) == value {
			return true
		}
	}
	return false
}

func withinDate(value, from, to time.Time) bool {
	if from.IsZero() && to.IsZero() {
		return true
	}
	if value.IsZero() {
		return false
	}
	value = dateOnly(value)
	if !from.IsZero() && value.Before(dateOnly(from)) {
		return false
	}
	return to.IsZero() || !value.After(dateOnly(to))
}

func matchesCPV(values, prefixes []string) bool {
	if len(prefixes) == 0 {
		return true
	}
	for _, prefix := range prefixes {
		prefix = strings.TrimSpace(prefix)
		for _, value := range values {
			if prefix != "" && strings.HasPrefix(cpvRoot(value), prefix) {
				return true
			}
		}
	}
	return false
}

func matchesAmount(value, minimum, maximum float64) bool {
	if minimum <= 0 && maximum <= 0 {
		return true
	}
	if value <= 0 {
		return false
	}
	if minimum > 0 && value < minimum {
		return false
	}
	return maximum <= 0 || value <= maximum
}

func matchesNoticeType(value AvisType, wants []AvisType) bool {
	if len(wants) == 0 {
		return true
	}
	for _, want := range wants {
		if value == want {
			return true
		}
	}
	return false
}

func matchesStatus(value TenderStatus, wants []TenderStatus) bool {
	if len(wants) == 0 {
		return true
	}
	for _, want := range wants {
		if value == want {
			return true
		}
	}
	return false
}
