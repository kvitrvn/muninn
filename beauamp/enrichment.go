package beauamp

import (
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/kvitrvn/muninn"
	"github.com/kvitrvn/muninn/internal/httpx"
)

const (
	maxCandidateAge    = 730 * 24 * time.Hour
	enrichmentPageSize = 200
)

var _ muninn.Enricher = (*Client)(nil)

type enrichmentResource struct {
	id       string
	url      string
	title    string
	from     time.Time
	to       time.Time
	freshAt  time.Time
	daily    bool
	coverage bool
}

// Enrich adds auditable BEAUAMP attribution context to already paginated
// tenders. BEAUAMP remains indicative: this method never mutates items and
// never feeds a composite candidate to muninn.MergeTenders.
func (c *Client) Enrich(
	ctx context.Context,
	items []muninn.Tender,
	options muninn.EnrichmentOptions,
	openAt time.Time,
) (muninn.EnrichmentResult, error) {
	if err := options.Validate(); err != nil {
		return muninn.EnrichmentResult{}, err
	}
	if options.HistoryMonths == 0 {
		options.HistoryMonths = muninn.DefaultEnrichmentHistoryMonths
	}
	if options.HistoryLimit == 0 {
		options.HistoryLimit = muninn.DefaultEnrichmentHistoryLimit
	}
	if options.CandidateLimit == 0 {
		options.CandidateLimit = muninn.DefaultEnrichmentCandidateLimit
	}
	if openAt.IsZero() {
		openAt = time.Now().UTC()
	}
	result := muninn.EnrichmentResult{
		Items: make([]muninn.TenderEnrichment, len(items)),
		Coverage: muninn.EnrichmentCoverage{
			RequestedFrom: openAt.AddDate(0, -options.HistoryMonths, 0),
			RequestedTo:   openAt,
		},
	}
	for index := range items {
		result.Items[index].TenderKey = items[index].DedupKey()
	}
	if len(items) == 0 {
		return result, nil
	}

	sirens := buyerSIRENs(items)
	if len(sirens) == 0 {
		return result, nil
	}

	resources, warnings, err := c.resolveEnrichmentResources(
		ctx,
		result.Coverage.RequestedFrom,
		result.Coverage.RequestedTo,
	)
	if err != nil {
		return result, err
	}
	result.Warnings = append(result.Warnings, warnings...)
	result.Partial = len(warnings) > 0

	rows, availableResources, fetchWarnings, truncated := c.fetchEnrichmentRows(
		ctx,
		resources,
		sirens,
		options.CandidateLimit,
	)
	result.Coverage = coverageFor(availableResources, result.Coverage)
	result.Warnings = append(result.Warnings, fetchWarnings...)
	result.Partial = result.Partial || len(fetchWarnings) > 0 || truncated
	if truncated {
		result.Warnings = append(result.Warnings, muninn.Warning{
			Provider: c.Name(),
			Code:     muninn.WarningTruncated,
			Message:  fmt.Sprintf("enrichment candidate limit %d reached", options.CandidateLimit),
		})
	}

	related := aggregateRows(rows)
	for index, primary := range items {
		result.Items[index] = enrichTender(primary, related, options, openAt)
	}
	return result, nil
}

func buyerSIRENs(items []muninn.Tender) []string {
	seen := map[string]bool{}
	var out []string
	for _, item := range items {
		if siren := item.Buyer.SIREN9(); siren != "" && !seen[siren] {
			seen[siren] = true
			out = append(out, siren)
		}
	}
	sort.Strings(out)
	return out
}

func (c *Client) resolveEnrichmentResources(
	ctx context.Context,
	from, to time.Time,
) ([]enrichmentResource, []muninn.Warning, error) {
	if len(c.resources) > 0 {
		resources := make([]enrichmentResource, 0, len(c.resources))
		for _, id := range c.resources {
			resources = append(resources, enrichmentResource{id: id})
		}
		return resources, nil, nil
	}

	reqURL := c.catalogBase + c.slug + "/"
	resp, err := httpx.Do(ctx, c.http, c.retry, func(ctx context.Context) (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	})
	if err != nil {
		return nil, nil, fmt.Errorf("beauamp: enrichment catalog request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("beauamp: unexpected enrichment catalog status %d", resp.StatusCode)
	}
	var catalog catalogResponse
	if err := json.NewDecoder(resp.Body).Decode(&catalog); err != nil {
		return nil, nil, fmt.Errorf("beauamp: decode enrichment catalog: %w", err)
	}

	var resources []enrichmentResource
	coveredMonths := map[string]bool{}
	seenIDs := map[string]bool{}
	for _, raw := range catalog.Resources {
		if !strings.EqualFold(raw.Format, "csv") || raw.ID == "" || seenIDs[raw.ID] {
			continue
		}
		resource := enrichmentResource{
			id:      raw.ID,
			url:     firstNonEmptyString(raw.URL, raw.Latest),
			title:   raw.Title,
			freshAt: parseTimestamp(firstNonEmptyString(raw.LastModified, raw.CreatedAt)),
		}
		if year, month, ok := parseMonthlyTitle(raw.Title); ok {
			resource.from, resource.to = monthBounds(year, time.Month(month))
			resource.coverage = true
			if !intervalsOverlap(resource.from, resource.to, from, to) {
				continue
			}
		} else if day, ok := parseDailyTitle(raw.Title); ok {
			if day.Year() != to.Year() || day.Month() != to.Month() {
				continue
			}
			resource.from, resource.to = day, day
			resource.daily = true
			resource.coverage = true
		} else if year, ok := parseYearlyTitle(raw.Title); ok {
			resource.from = time.Date(year, time.January, 1, 0, 0, 0, 0, time.UTC)
			resource.to = resource.from.AddDate(1, 0, 0).Add(-time.Nanosecond)
			resource.coverage = true
			if !intervalsOverlap(resource.from, resource.to, from, to) {
				continue
			}
		} else {
			continue
		}
		coverageFrom := resource.from
		if coverageFrom.Before(from) {
			coverageFrom = from
		}
		coverageTo := resource.to
		if coverageTo.After(to) {
			coverageTo = to
		}
		for month := monthStart(coverageFrom); !month.After(monthStart(coverageTo)); month = month.AddDate(0, 1, 0) {
			coveredMonths[monthKey(month)] = true
		}
		seenIDs[raw.ID] = true
		resources = append(resources, resource)
	}

	sort.Slice(resources, func(i, j int) bool {
		if !resources[i].to.Equal(resources[j].to) {
			return resources[i].to.After(resources[j].to)
		}
		if resources[i].daily != resources[j].daily {
			return resources[i].daily
		}
		return resources[i].id < resources[j].id
	})

	var warnings []muninn.Warning
	for month := monthStart(from); !month.After(monthStart(to)); month = month.AddDate(0, 1, 0) {
		if coveredMonths[monthKey(month)] {
			continue
		}
		warnings = append(warnings, muninn.Warning{
			Provider: c.Name(),
			Code:     muninn.WarningCoverageGap,
			Message:  "no BEAUAMP resource for " + month.Format("2006-01"),
		})
	}
	if len(resources) == 0 {
		warnings = append(warnings, muninn.Warning{
			Provider: c.Name(),
			Code:     muninn.WarningCoverageGap,
			Message:  "no BEAUAMP resource overlaps the requested enrichment period",
		})
	}
	return resources, warnings, nil
}

func parseDailyTitle(title string) (time.Time, bool) {
	lower := strings.ToLower(title)
	index := strings.Index(lower, "beauamp-")
	if index < 0 {
		return time.Time{}, false
	}
	value := lower[index+len("beauamp-"):]
	if dot := strings.IndexByte(value, '.'); dot >= 0 {
		value = value[:dot]
	}
	if extra := strings.IndexByte(value, '_'); extra >= 0 {
		value = value[:extra]
	}
	parsed, err := time.Parse("02-01-2006", value)
	return parsed, err == nil
}

func parseYearlyTitle(title string) (int, bool) {
	lower := strings.ToLower(strings.TrimSpace(title))
	if strings.HasPrefix(lower, "beauamp_") {
		parts := strings.Split(lower, "_")
		if len(parts) >= 2 {
			year, err := strconv.Atoi(parts[1])
			return year, err == nil && year >= 2000 && year <= 2100
		}
	}
	if len(lower) >= 4 && strings.Contains(lower, "beauamp") {
		year, err := strconv.Atoi(lower[:4])
		return year, err == nil && year >= 2000 && year <= 2100
	}
	return 0, false
}

func monthBounds(year int, month time.Month) (time.Time, time.Time) {
	from := time.Date(year, month, 1, 0, 0, 0, 0, time.UTC)
	return from, from.AddDate(0, 1, 0).Add(-time.Nanosecond)
}

func monthStart(value time.Time) time.Time {
	return time.Date(value.Year(), value.Month(), 1, 0, 0, 0, 0, time.UTC)
}

func monthKey(value time.Time) string { return value.Format("2006-01") }

func intervalsOverlap(aFrom, aTo, bFrom, bTo time.Time) bool {
	return !aTo.Before(bFrom) && !aFrom.After(bTo)
}

func parseTimestamp(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed
		}
	}
	return time.Time{}
}

func coverageFor(
	resources []enrichmentResource,
	coverage muninn.EnrichmentCoverage,
) muninn.EnrichmentCoverage {
	for _, resource := range resources {
		if resource.coverage {
			from := resource.from
			to := resource.to
			if from.Before(coverage.RequestedFrom) {
				from = coverage.RequestedFrom
			}
			if to.After(coverage.RequestedTo) {
				to = coverage.RequestedTo
			}
			if coverage.AvailableFrom.IsZero() || from.Before(coverage.AvailableFrom) {
				coverage.AvailableFrom = from
			}
			if coverage.AvailableTo.IsZero() || to.After(coverage.AvailableTo) {
				coverage.AvailableTo = to
			}
		}
		if resource.freshAt.After(coverage.FreshAt) {
			coverage.FreshAt = resource.freshAt
		}
	}
	return coverage
}

func (c *Client) fetchEnrichmentRows(
	ctx context.Context,
	resources []enrichmentResource,
	sirens []string,
	limit int,
) ([]map[string]any, []enrichmentResource, []muninn.Warning, bool) {
	var (
		rows               []map[string]any
		availableResources []enrichmentResource
		warnings           []muninn.Warning
		seen               = map[string]bool{}
		truncated          bool
	)
	for resourceIndex, resource := range resources {
		resourceRows, resourceTruncated, err := c.fetchEnrichmentResource(
			ctx,
			resource,
			sirens,
			limit-len(rows),
		)
		if err != nil {
			warnings = append(warnings, muninn.Warning{
				Provider: c.Name(),
				Code:     muninn.WarningResourceError,
				Message:  fmt.Sprintf("resource %s could not be read", resource.id),
				Err:      err,
			})
			continue
		}
		availableResources = append(availableResources, resource)
		for _, row := range resourceRows {
			key := rowIdentity(row)
			if seen[key] {
				continue
			}
			seen[key] = true
			rows = append(rows, row)
			if len(rows) >= limit {
				truncated = resourceTruncated || resourceIndex < len(resources)-1
				break
			}
		}
		if resourceTruncated || truncated {
			truncated = true
			break
		}
	}
	return rows, availableResources, warnings, truncated
}

func (c *Client) fetchEnrichmentResource(
	ctx context.Context,
	resource enrichmentResource,
	sirens []string,
	limit int,
) ([]map[string]any, bool, error) {
	if limit <= 0 {
		return nil, true, nil
	}
	var rows []map[string]any
	size := min(enrichmentPageSize, limit)
	for page := 1; ; page++ {
		params := url.Values{}
		params.Set("siren_acheteur__in", strings.Join(sirens, ","))
		params.Set("page_size", strconv.Itoa(size))
		params.Set("page", strconv.Itoa(page))
		reqURL := c.tabularBase + resource.id + "/data/?" + params.Encode()
		resp, err := httpx.Do(ctx, c.http, c.retry, func(ctx context.Context) (*http.Request, error) {
			return http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		})
		if err != nil {
			return rows, false, fmt.Errorf("beauamp: enrichment request failed: %w", err)
		}
		if resp.StatusCode == http.StatusNotFound {
			resp.Body.Close()
			if resource.daily && resource.url != "" {
				return c.streamCSVResource(ctx, resource.url, sirens, limit)
			}
			return rows, false, errResourceNotIndexed
		}
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
			resp.Body.Close()
			return rows, false, fmt.Errorf("beauamp: unexpected enrichment status %d: %s", resp.StatusCode, body)
		}
		var parsed tabularResponse
		err = json.NewDecoder(resp.Body).Decode(&parsed)
		resp.Body.Close()
		if err != nil {
			return rows, false, fmt.Errorf("beauamp: decode enrichment response: %w", err)
		}
		rows = append(rows, parsed.Data...)
		if len(rows) >= limit {
			return rows[:limit], parsed.Meta.Total > limit, nil
		}
		if len(parsed.Data) < size || page*size >= parsed.Meta.Total {
			return rows, false, nil
		}
	}
}

func (c *Client) streamCSVResource(
	ctx context.Context,
	resourceURL string,
	sirens []string,
	limit int,
) ([]map[string]any, bool, error) {
	resp, err := httpx.Do(ctx, c.http, c.retry, func(ctx context.Context) (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodGet, resourceURL, nil)
	})
	if err != nil {
		return nil, false, fmt.Errorf("beauamp: CSV request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("beauamp: unexpected CSV status %d", resp.StatusCode)
	}

	reader := csv.NewReader(resp.Body)
	reader.ReuseRecord = true
	header, err := reader.Read()
	if err != nil {
		return nil, false, fmt.Errorf("beauamp: read CSV header: %w", err)
	}
	header = append([]string(nil), header...)
	index := -1
	for i, field := range header {
		if field == "siren_acheteur" {
			index = i
			break
		}
	}
	if index < 0 {
		return nil, false, errors.New("beauamp: CSV has no siren_acheteur column")
	}
	wanted := map[string]bool{}
	for _, siren := range sirens {
		wanted[siren] = true
	}

	var rows []map[string]any
	for {
		record, err := reader.Read()
		if errors.Is(err, io.EOF) {
			return rows, false, nil
		}
		if err != nil {
			return rows, false, fmt.Errorf("beauamp: read CSV row: %w", err)
		}
		if index >= len(record) || !wanted[record[index]] {
			continue
		}
		row := make(map[string]any, len(header))
		for i, field := range header {
			if i < len(record) {
				row[field] = record[i]
			}
		}
		rows = append(rows, row)
		if len(rows) >= limit {
			return rows, true, nil
		}
	}
}

func rowIdentity(row map[string]any) string {
	keys := []string{
		"id_boamp_attribution", "id_boamp_contrat", "id_lot", "numero_lot",
		"siret_fournisseur", "siren_fournisseur", "nom_declare_fournisseur",
		"cpv", "cpv_supp", "code_cpv", "objet", "objet_lot", "decision",
		"date_avis_attribution", "date_avis_marche",
		"valeur_totale", "prix_attribution_lot", "valeur_estimee_lot", "valeur_lot",
	}
	values := make([]string, 0, len(keys))
	for _, key := range keys {
		values = append(values, key+"="+fmt.Sprint(row[key]))
	}
	data := []byte(strings.Join(values, "\x1f"))
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func aggregateRows(rows []map[string]any) []muninn.Tender {
	byID := map[string][]muninn.Tender{}
	var order []string
	for _, row := range rows {
		if firstString(row, "__id", "id") == "" {
			row["__id"] = rowIdentity(row)
		}
		tender := mapRecord(row)
		id := firstString(row, "id_boamp_attribution")
		if id == "" {
			id = firstNonEmptyString(
				firstString(row, "id_boamp_contrat"),
				tender.Sources[0].RecordID,
				rowIdentity(row),
			)
		}
		if _, exists := byID[id]; !exists {
			order = append(order, id)
		}
		byID[id] = append(byID[id], tender)
	}

	var out []muninn.Tender
	for _, id := range order {
		merged := muninn.MergeTenders(byID[id])
		if len(merged) == 0 {
			continue
		}
		// All rows share the attribution grain. MergeTenders normally returns
		// one value through the shared SourceReference.ID; be defensive when an
		// incomplete native row lacks that identifier.
		tender := merged[0]
		for _, sibling := range merged[1:] {
			combined := muninn.MergeTenders([]muninn.Tender{tender, sibling})
			if len(combined) == 1 {
				tender = combined[0]
			}
		}
		out = append(out, tender)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].DatePublication.Equal(out[j].DatePublication) {
			return out[i].DatePublication.After(out[j].DatePublication)
		}
		return out[i].DedupKey() < out[j].DedupKey()
	})
	return out
}

func enrichTender(
	primary muninn.Tender,
	related []muninn.Tender,
	options muninn.EnrichmentOptions,
	openAt time.Time,
) muninn.TenderEnrichment {
	out := muninn.TenderEnrichment{TenderKey: primary.DedupKey()}
	boampID := nativeBOAMPID(primary)
	exactKeys := map[string]bool{}

	for _, tender := range related {
		attributionIDs, contractIDs := beauampIDs(tender)
		evidence := relationEvidence(primary, tender, boampID, attributionIDs, contractIDs)
		switch {
		case boampID != "" && containsString(attributionIDs, boampID):
			out.ExactRelations = append(out.ExactRelations, muninn.RelatedTender{
				Tender: tender, Relation: muninn.RelationSameAwardNotice,
				Confidence: muninn.ConfidenceExact, Evidence: evidence,
			})
			exactKeys[tender.DedupKey()] = true
		case boampID != "" && containsString(contractIDs, boampID):
			out.ExactRelations = append(out.ExactRelations, muninn.RelatedTender{
				Tender: tender, Relation: muninn.RelationReportedContract,
				Confidence: muninn.ConfidenceSourceReported, Evidence: evidence,
			})
			exactKeys[tender.DedupKey()] = true
		case compositeCandidate(primary, tender, evidence):
			out.Candidates = append(out.Candidates, muninn.RelatedTender{
				Tender: tender, Relation: muninn.RelationCompositeCandidate,
				Confidence: muninn.ConfidenceCandidate, Evidence: evidence,
			})
		}
		if len(contractIDs) > 1 {
			out.Conflicts = append(out.Conflicts, muninn.EnrichmentConflict{
				Code:      "identifier_conflict",
				Message:   "BEAUAMP rows report several contract notice identifiers for one award notice",
				RelatedID: tender.Sources[0].ID,
			})
		}
	}

	sortRelated(out.ExactRelations)
	sortRelated(out.Candidates)
	for _, relation := range out.ExactRelations {
		if primary.StatusAt(openAt) == muninn.StatusOpen &&
			relation.Tender.StatusAt(openAt) == muninn.StatusAwarded {
			out.Conflicts = append(out.Conflicts, muninn.EnrichmentConflict{
				Code:          "lifecycle_conflict",
				Message:       "BEAUAMP reports an award while the authoritative BOAMP notice is still open",
				BOAMPStatus:   muninn.StatusOpen,
				BEAUAMPStatus: muninn.StatusAwarded,
				RelatedID:     relation.Tender.Sources[0].ID,
			})
		}
	}

	historyFrom := openAt.AddDate(0, -options.HistoryMonths, 0)
	for _, tender := range related {
		if len(out.BuyerHistory) >= options.HistoryLimit {
			break
		}
		if exactKeys[tender.DedupKey()] || tender.AvisType != muninn.AvisAttribution {
			continue
		}
		if tender.Buyer.SIREN9() == "" || tender.Buyer.SIREN9() != primary.Buyer.SIREN9() {
			continue
		}
		if !cpvOverlap(primary.CPVCodes, tender.CPVCodes) {
			continue
		}
		if tender.DatePublication.Before(historyFrom) || !tender.DatePublication.Before(openAt) {
			continue
		}
		out.BuyerHistory = append(out.BuyerHistory, tender)
	}
	return out
}

func nativeBOAMPID(tender muninn.Tender) string {
	for _, source := range tender.Sources {
		if strings.EqualFold(source.Provider, "boamp") && source.ID != "" {
			return source.ID
		}
	}
	return ""
}

func beauampIDs(tender muninn.Tender) ([]string, []string) {
	var attributionIDs, contractIDs []string
	for _, source := range tender.Sources {
		if !strings.EqualFold(source.Provider, "beauamp") {
			continue
		}
		attributionIDs = appendUnique(attributionIDs, firstString(source.RawFields, "id_boamp_attribution"))
		contractIDs = appendUnique(contractIDs, firstString(source.RawFields, "id_boamp_contrat"))
	}
	sort.Strings(attributionIDs)
	sort.Strings(contractIDs)
	return attributionIDs, contractIDs
}

func relationEvidence(
	primary, candidate muninn.Tender,
	boampID string,
	attributionIDs, contractIDs []string,
) muninn.RelationEvidence {
	overlap := cpvIntersection(primary.CPVCodes, candidate.CPVCodes)
	gap := candidate.DatePublication.Sub(primary.DatePublication)
	gapDays := 0
	temporal := !primary.DatePublication.IsZero() && !candidate.DatePublication.IsZero() &&
		gap > 0 && gap <= maxCandidateAge
	if !primary.DatePublication.IsZero() && !candidate.DatePublication.IsZero() {
		gapDays = int(gap / (24 * time.Hour))
	}
	return muninn.RelationEvidence{
		BOAMPID:              boampID,
		BEAUAMPAttributionID: firstSliceValue(attributionIDs),
		BEAUAMPContractID:    firstSliceValue(contractIDs),
		BuyerSIREN:           candidate.Buyer.SIREN9(),
		BuyerSIRENEstimated:  buyerSIRENEstimated(candidate),
		CPVRoots:             overlap,
		ObjectSimilarity:     jaccardObject(primary, candidate),
		PublicationGapDays:   gapDays,
		TemporalConsistent:   temporal,
	}
}

func compositeCandidate(
	primary, candidate muninn.Tender,
	evidence muninn.RelationEvidence,
) bool {
	return primary.Buyer.SIREN9() != "" &&
		primary.Buyer.SIREN9() == candidate.Buyer.SIREN9() &&
		len(evidence.CPVRoots) > 0 &&
		evidence.TemporalConsistent &&
		evidence.ObjectSimilarity >= 0.85
}

func buyerSIRENEstimated(tender muninn.Tender) bool {
	for _, source := range tender.Sources {
		raw := source.RawFields
		switch known := raw["siren_acheteur_connu"].(type) {
		case bool:
			if !known {
				return true
			}
		case string:
			if strings.EqualFold(strings.TrimSpace(known), "false") ||
				strings.TrimSpace(known) == "0" ||
				strings.EqualFold(strings.TrimSpace(known), "non") {
				return true
			}
		}
		for _, key := range []string{
			"siren_acheteur_estime",
			"siren_acheteur_estimé",
			"acheteur_estime",
			"acheteur_estimé",
		} {
			switch value := raw[key].(type) {
			case bool:
				if value {
					return true
				}
			case string:
				value = strings.ToLower(strings.TrimSpace(value))
				if value == "true" || value == "1" || value == "oui" || value == "estimé" || value == "estime" {
					return true
				}
			}
		}
		for _, key := range []string{"source_siren_acheteur", "qualite_siren_acheteur"} {
			if strings.Contains(strings.ToLower(str(raw[key])), "estim") {
				return true
			}
		}
	}
	return false
}

func cpvOverlap(a, b []string) bool { return len(cpvIntersection(a, b)) > 0 }

func cpvIntersection(a, b []string) []string {
	seen := map[string]bool{}
	for _, code := range a {
		if root := cpvRoot8(code); root != "" {
			seen[root] = true
		}
	}
	var out []string
	for _, code := range b {
		root := cpvRoot8(code)
		if root != "" && seen[root] && !containsString(out, root) {
			out = append(out, root)
		}
	}
	sort.Strings(out)
	return out
}

func cpvRoot8(value string) string {
	value = strings.TrimSpace(value)
	if len(value) < 8 {
		return ""
	}
	root := value[:8]
	for _, r := range root {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return root
}

func jaccardObject(a, b muninn.Tender) float64 {
	left := tokenSet(firstNonEmptyString(a.Objet, a.Titre))
	right := tokenSet(firstNonEmptyString(b.Objet, b.Titre))
	if len(left) == 0 || len(right) == 0 {
		return 0
	}
	intersection := 0
	for token := range left {
		if right[token] {
			intersection++
		}
	}
	union := len(left) + len(right) - intersection
	return float64(intersection) / float64(union)
}

func tokenSet(value string) map[string]bool {
	var normalized strings.Builder
	space := true
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			normalized.WriteRune(r)
			space = false
		} else if !space {
			normalized.WriteByte(' ')
			space = true
		}
	}
	out := map[string]bool{}
	for _, token := range strings.Fields(normalized.String()) {
		out[token] = true
	}
	return out
}

func sortRelated(values []muninn.RelatedTender) {
	sort.SliceStable(values, func(i, j int) bool {
		if !values[i].Tender.DatePublication.Equal(values[j].Tender.DatePublication) {
			return values[i].Tender.DatePublication.After(values[j].Tender.DatePublication)
		}
		return values[i].Tender.DedupKey() < values[j].Tender.DedupKey()
	})
}

func appendUnique(values []string, value string) []string {
	if value != "" && !containsString(values, value) {
		return append(values, value)
	}
	return values
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func firstSliceValue(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}
