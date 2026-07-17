// Package boamp implements muninn.Provider for the BOAMP API (DILA), exposed
// through an Opendatasoft platform (Explore API v2.1).
//
// The top-level field names used here (idweb, objet, nomacheteur,
// code_departement, dateparution, datelimitereponse, type_marche,
// nature_categorise_libelle, donnees, url_avis) are confirmed against the live
// dataset schema (see FetchSchema).
//
// Caveat: the eForms (UBL) format is mandatory for every BOAMP notice since
// 2024-01-31. The parsing of the nested "donnees" field below
// (mapProcedureFromNested, mapEngagementFromNested, extractCPV) follows the
// older pre-eForms structure and is best-effort: for a recent notice the real
// shape of "donnees" is likely different (UBL/eForms vocabulary). The
// procedure/engagement mapping should be validated against a real recent
// record before being relied upon in production.
package boamp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/kvitrvn/muninn"
)

const defaultBaseURL = "https://boamp-datadila.opendatasoft.com/api/explore/v2.1/catalog/datasets/boamp/records"

// Opendatasoft Explore API v2.1 constraints (verified live):
//   - maxPageSize: limit is capped at 100 per request.
//   - maxOffsetWindow: offset+limit must stay <= 10000. Beyond that, the
//     /exports endpoint is required (out of scope for now).
const (
	maxPageSize     = 100
	maxOffsetWindow = 10000
)

// Client queries the BOAMP API.
type Client struct {
	baseURL string
	http    *http.Client
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient injects a custom *http.Client (timeouts, instrumented
// transport, proxy...).
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) { c.http = h }
}

// WithBaseURL overrides the base URL (useful for tests against a mock server).
func WithBaseURL(u string) Option {
	return func(c *Client) { c.baseURL = u }
}

// New creates a BOAMP client.
func New(opts ...Option) *Client {
	c := &Client{
		baseURL: defaultBaseURL,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Compile-time check: *Client satisfies the muninn.Provider contract.
var _ muninn.Provider = (*Client)(nil)

// Name implements muninn.Provider.
func (c *Client) Name() string { return "boamp" }

// odsResponse mirrors the generic shape of an Explore API v2.1 response.
type odsResponse struct {
	TotalCount int              `json:"total_count"`
	Results    []map[string]any `json:"results"`
}

// Count returns the total number of tenders matching q without fetching the
// records (a single request, limit=1). This is the preferred way to estimate
// "how many tenders?": the total_count returned by the API is not capped by the
// 10,000 pagination window.
func (c *Client) Count(ctx context.Context, q muninn.Query) (int, error) {
	resp, err := c.fetchPage(ctx, buildWhere(q), 1, 0)
	if err != nil {
		return 0, err
	}
	return resp.TotalCount, nil
}

// Search implements muninn.Provider. It fetches ALL records matching q by
// paginating (pages of maxPageSize), within the offset window allowed by the
// API (maxOffsetWindow) and, if q.Limit > 0, that explicit cap.
//
// Keywords and the date/department filters are pushed server-side via the
// `where` clause (the API's `q` parameter is ignored in v2.1).
//
// When the total exceeds what can be paginated, Search returns the fetched
// records AND an *ErrTruncated (detectable via errors.As) carrying the real
// total: the caller may ignore it and use the subset, or switch to Count / the
// /exports endpoint.
func (c *Client) Search(ctx context.Context, q muninn.Query) ([]muninn.Tender, error) {
	where := buildWhere(q)

	// Effective upper bound: API window, tightened by q.Limit when provided.
	hardCap := maxOffsetWindow
	if q.Limit > 0 && q.Limit < hardCap {
		hardCap = q.Limit
	}

	var (
		tenders    []muninn.Tender
		totalCount int
	)
	for offset := 0; offset < hardCap; offset += maxPageSize {
		pageSize := maxPageSize
		if remaining := hardCap - offset; remaining < pageSize {
			pageSize = remaining
		}

		resp, err := c.fetchPage(ctx, where, pageSize, offset)
		if err != nil {
			return tenders, err
		}
		totalCount = resp.TotalCount

		for _, rec := range resp.Results {
			tenders = append(tenders, mapRecord(rec))
		}
		// Last page reached (fewer records than requested, or nothing left
		// server-side).
		if len(resp.Results) < pageSize || len(tenders) >= totalCount {
			break
		}
	}

	if totalCount > len(tenders) {
		return tenders, &ErrTruncated{Retrieved: len(tenders), Total: totalCount}
	}
	return tenders, nil
}

// ErrTruncated signals that Search could not fetch every record (total greater
// than the API pagination window or the requested q.Limit). Total holds the
// real number of matching tenders.
type ErrTruncated struct {
	Retrieved int
	Total     int
}

func (e *ErrTruncated) Error() string {
	return fmt.Sprintf("boamp: truncated results: %d retrieved out of %d (pagination cap %d)",
		e.Retrieved, e.Total, maxOffsetWindow)
}

// fetchPage performs a single API request and decodes the response. Shared by
// Search and Count so there is only one HTTP path.
func (c *Client) fetchPage(ctx context.Context, where string, limit, offset int) (odsResponse, error) {
	params := url.Values{}
	if where != "" {
		params.Set("where", where)
	}
	params.Set("limit", strconv.Itoa(limit))
	if offset > 0 {
		params.Set("offset", strconv.Itoa(offset))
	}

	reqURL := c.baseURL + "?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return odsResponse{}, fmt.Errorf("boamp: build request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return odsResponse{}, fmt.Errorf("boamp: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return odsResponse{}, fmt.Errorf("boamp: unexpected status %d: %s", resp.StatusCode, body)
	}

	var parsed odsResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return odsResponse{}, fmt.Errorf("boamp: decode response: %w", err)
	}
	return parsed, nil
}

// buildKeywordClause turns q.Keywords into a parenthesized ODSQL clause. Two
// independent knobs:
//   - ObjetOnly: `objet like "kw"` (phrase in the title, precise) vs `"kw"`
//     (full-text over all fields, broad and noisy).
//   - MatchAll: terms joined with AND (intersection) vs OR (union, default).
//
// Returns "" when there is no usable keyword.
func buildKeywordClause(q muninn.Query) string {
	var parts []string
	for _, k := range q.Keywords {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		if q.ObjetOnly {
			parts = append(parts, fmt.Sprintf(`objet like "%s"`, escapeODSQL(k)))
		} else {
			parts = append(parts, fmt.Sprintf(`"%s"`, escapeODSQL(k)))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	sep := " OR "
	if q.MatchAll {
		sep = " AND "
	}
	return "(" + strings.Join(parts, sep) + ")"
}

// buildWhere builds the full ODSQL `where` clause, combining with AND:
//   - a keyword clause (see buildKeywordClause) — this is what actually filters,
//     since the v2.1 API ignores the `q` parameter;
//   - the structured filters (departments, dates) on confirmed top-level fields.
//
// An empty Query returns "" (no filter → the whole dataset).
func buildWhere(q muninn.Query) string {
	var clauses []string

	if kw := buildKeywordClause(q); kw != "" {
		clauses = append(clauses, kw)
	}

	if len(q.Departements) > 0 {
		var deptClauses []string
		for _, d := range q.Departements {
			deptClauses = append(deptClauses, fmt.Sprintf(`code_departement="%s"`, escapeODSQL(d)))
		}
		clauses = append(clauses, "("+strings.Join(deptClauses, " OR ")+")")
	}
	if !q.DateFrom.IsZero() {
		clauses = append(clauses, fmt.Sprintf(`dateparution >= "%s"`, q.DateFrom.Format("2006-01-02")))
	}
	if !q.DateTo.IsZero() {
		clauses = append(clauses, fmt.Sprintf(`dateparution <= "%s"`, q.DateTo.Format("2006-01-02")))
	}
	return strings.Join(clauses, " AND ")
}

func escapeODSQL(s string) string {
	return strings.ReplaceAll(s, `"`, `\"`)
}

// mapRecord translates a raw Opendatasoft record into a muninn.Tender. The
// top-level fields are read directly; the finer procedure/CPV detail is read
// from the nested "donnees" field (JSON-stringified from the original
// XML/eForms).
func mapRecord(rec map[string]any) muninn.Tender {
	t := muninn.Tender{
		Source:    "boamp",
		RawFields: rec,
	}

	if v, ok := rec["idweb"].(string); ok {
		t.SourceID = v
	} else if v, ok := rec["id"].(string); ok {
		t.SourceID = v
	}
	if v, ok := rec["objet"].(string); ok {
		t.Objet = v
		t.Titre = v
	}
	if v, ok := rec["nomacheteur"].(string); ok {
		t.Buyer.Nom = v
	}
	// code_departement is an array on the API side (e.g. ["35"]), but some
	// fixtures/older formats provide it as a string: handle both.
	switch v := rec["code_departement"].(type) {
	case string:
		t.Buyer.CodeDepartement = v
	case []any:
		if len(v) > 0 {
			if s, ok := v[0].(string); ok {
				t.Buyer.CodeDepartement = s
			}
		}
	}
	if v, ok := rec["url_avis"].(string); ok {
		t.URL = v
	}
	if v, ok := rec["dateparution"].(string); ok {
		if parsed, err := parseODSDate(v); err == nil {
			t.DatePublication = parsed
		}
	}
	if v, ok := rec["datelimitereponse"].(string); ok {
		if parsed, err := parseODSDate(v); err == nil {
			t.DateLimiteReponse = parsed
		}
	}

	t.AvisType = mapAvisType(rec)
	t.Procedure = mapProcedure(rec)
	t.Engagement = mapEngagement(rec)

	// The finer detail not covered by top-level fields (CPV in particular) lives
	// in "donnees", a JSON-stringified blob — parsed only when present and valid,
	// and best-effort (see the package doc on the eForms uncertainty).
	if raw, ok := rec["donnees"].(string); ok && raw != "" {
		var nested map[string]any
		if err := json.Unmarshal([]byte(raw), &nested); err == nil {
			t.CPVCodes = extractCPV(nested)
			// If the top-level fields yielded nothing, fall back to the nested
			// pre-eForms structure as a last resort.
			if t.Procedure == muninn.ProcedureInconnue {
				t.Procedure = mapProcedureFromNested(nested)
			}
			if t.Engagement == muninn.EngagementInconnu {
				t.Engagement = mapEngagementFromNested(nested)
			}
		}
	}

	return t
}

// parseODSDate tries the two date formats seen on the Opendatasoft API (date
// only, or date+time ISO8601).
func parseODSDate(v string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, nil
	}
	return time.Parse("2006-01-02", v)
}

// mapAvisType relies on nature_categorise_libelle, whose observed values are
// "Avis de marché" and "Résultat de marché".
func mapAvisType(rec map[string]any) muninn.AvisType {
	raw, _ := rec["nature_categorise_libelle"].(string)
	switch {
	case strings.Contains(raw, "Résultat"):
		return muninn.AvisAttribution
	case strings.Contains(raw, "Avis de marché"):
		return muninn.AvisAppelConcurrence
	case strings.Contains(strings.ToLower(raw), "rectifi"):
		return muninn.AvisRectificatif
	default:
		return muninn.AvisInconnu
	}
}

// mapProcedure relies on the top-level fields procedure_libelle and
// type_procedure. Caveat: these fields exist, but their exact values for an
// eForms notice are not yet confirmed — the labels matched below are reasonable
// guesses to validate against a real recent record.
func mapProcedure(rec map[string]any) muninn.ProcedureType {
	raw, _ := rec["procedure_libelle"].(string)
	if raw == "" {
		raw, _ = rec["type_procedure"].(string)
	}
	lower := strings.ToLower(strings.TrimSpace(raw))
	switch {
	case strings.Contains(lower, "ouvert"):
		return muninn.ProcedureOuverte
	case strings.Contains(lower, "restreint"):
		return muninn.ProcedureRestreinte
	case strings.Contains(lower, "dialogue"):
		return muninn.ProcedureDialogueCompetitif
	case strings.Contains(lower, "concours"):
		return muninn.ProcedureConcours
	case strings.Contains(lower, "négocié"), strings.Contains(lower, "negocie"):
		if strings.Contains(lower, "sans publicité") || strings.Contains(lower, "sans mise en concurrence") {
			return muninn.ProcedureNegocieeSansPublicite
		}
		return muninn.ProcedureNegocieeAvecPublicite
	default:
		return muninn.ProcedureInconnue
	}
}

// mapEngagement relies on the top-level type_marche field. Same caveat as
// mapProcedure about exact values.
func mapEngagement(rec map[string]any) muninn.EngagementType {
	raw, _ := rec["type_marche"].(string)
	if raw == "" {
		raw, _ = rec["type_marche_facette"].(string)
	}
	lower := strings.ToLower(raw)
	switch {
	case strings.Contains(lower, "accord-cadre") && strings.Contains(lower, "bons de commande"):
		return muninn.EngagementAccordCadreBC
	case strings.Contains(lower, "accord-cadre") && strings.Contains(lower, "marchés subséquents"):
		return muninn.EngagementAccordCadreMS
	case strings.Contains(lower, "accord-cadre"):
		return muninn.EngagementAccordCadreBC
	case strings.Contains(lower, "ferme"), strings.Contains(lower, "marché") && !strings.Contains(lower, "accord"):
		return muninn.EngagementFerme
	default:
		return muninn.EngagementInconnu
	}
}

// mapProcedureFromNested looks up OBJET.PROCEDURE.TYPE_PROCEDURE, whose
// OUVERT/RESTREINT/NEGOCIE sub-key indicates the type. Keying on structure
// rather than free-text labels is more robust to spelling variations.
func mapProcedureFromNested(nested map[string]any) muninn.ProcedureType {
	tp := digDict(nested, "OBJET", "PROCEDURE", "TYPE_PROCEDURE")
	if tp == nil {
		return muninn.ProcedureInconnue
	}
	if _, ok := tp["OUVERT"]; ok {
		return muninn.ProcedureOuverte
	}
	if _, ok := tp["RESTREINT"]; ok {
		return muninn.ProcedureRestreinte
	}
	if _, ok := tp["NEGOCIE"]; ok {
		// The with/without-advertising distinction could not be confirmed;
		// refine if needed (likely a sub-key of NEGOCIE).
		return muninn.ProcedureNegocieeAvecPublicite
	}
	return muninn.ProcedureInconnue
}

// mapEngagementFromNested detects a framework agreement via the ACCORD_CADRE_OUI
// flag. The purchase-order vs subsequent-contract distinction is not confirmed;
// EngagementAccordCadreBC is used by default in that case.
func mapEngagementFromNested(nested map[string]any) muninn.EngagementType {
	blob, _ := json.Marshal(nested)
	s := string(blob)
	if strings.Contains(s, "ACCORD_CADRE_OUI") || strings.Contains(s, "ACCORD_CADRE") {
		return muninn.EngagementAccordCadreBC
	}
	if digDict(nested, "OBJET") != nil {
		return muninn.EngagementFerme
	}
	return muninn.EngagementInconnu
}

// extractCPV reads OBJET.CPV.PRINCIPAL (single-lot) and, when present, the
// per-lot CPV codes under OBJET.LOT[].CPV.PRINCIPAL (multi-lot contracts).
func extractCPV(nested map[string]any) []string {
	var codes []string
	if cpv := digDict(nested, "OBJET", "CPV"); cpv != nil {
		if principal, ok := cpv["PRINCIPAL"].(string); ok && principal != "" {
			codes = append(codes, principal)
		}
	}
	if lots, ok := digAny(nested, "OBJET", "LOT").([]any); ok {
		for _, lot := range lots {
			lotMap, ok := lot.(map[string]any)
			if !ok {
				continue
			}
			if cpv, ok := lotMap["CPV"].(map[string]any); ok {
				if principal, ok := cpv["PRINCIPAL"].(string); ok && principal != "" {
					codes = append(codes, principal)
				}
			}
		}
	}
	return codes
}

// digDict walks a chain of nested keys and returns the last level if it is a
// map[string]any, otherwise nil.
func digDict(m map[string]any, keys ...string) map[string]any {
	v := digAny(m, keys...)
	if d, ok := v.(map[string]any); ok {
		return d
	}
	return nil
}

// digAny walks a chain of nested keys without assuming the type of the last
// level (useful for lists, e.g. OBJET.LOT[]).
func digAny(m map[string]any, keys ...string) any {
	var cur any = m
	for _, k := range keys {
		d, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = d[k]
	}
	return cur
}
