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
	"net/http"
	"strings"
	"time"

	"github.com/kvitrvn/muninn"
	"github.com/kvitrvn/muninn/internal/ods"
)

const defaultBaseURL = "https://boamp-datadila.opendatasoft.com/api/explore/v2.1/catalog/datasets/boamp/records"

// Client queries the BOAMP API. The Opendatasoft pagination and request
// plumbing lives in internal/ods; this package supplies the base URL, the
// where-clause field names, and the record mapping.
type Client struct {
	ods *ods.Client
}

// Option configures a Client.
type Option func(*ods.Client)

// WithHTTPClient injects a custom *http.Client (timeouts, instrumented
// transport, proxy...).
func WithHTTPClient(h *http.Client) Option {
	return func(c *ods.Client) { c.HTTP = h }
}

// WithBaseURL overrides the base URL (useful for tests against a mock server).
func WithBaseURL(u string) Option {
	return func(c *ods.Client) { c.BaseURL = u }
}

// New creates a BOAMP client.
func New(opts ...Option) *Client {
	inner := &ods.Client{
		Source:  "boamp",
		BaseURL: defaultBaseURL,
		HTTP:    &http.Client{Timeout: 30 * time.Second},
		Map:     mapRecord,
		Where:   buildWhere,
	}
	for _, opt := range opts {
		opt(inner)
	}
	return &Client{ods: inner}
}

// Compile-time check: *Client satisfies the muninn.Provider contract.
var _ muninn.Provider = (*Client)(nil)

// Name implements muninn.Provider.
func (c *Client) Name() string { return "boamp" }

// Count returns the total number of tenders matching q without fetching the
// records. This is the preferred way to estimate "how many tenders?": the
// total_count returned by the API is not capped by the 10,000 pagination
// window.
func (c *Client) Count(ctx context.Context, q muninn.Query) (int, error) {
	return c.ods.Count(ctx, q)
}

// Search implements muninn.Provider. It fetches every record matching q by
// paginating; keywords and the date/department filters are pushed server-side
// via the `where` clause (the API's `q` parameter is ignored in v2.1). When the
// total exceeds what can be paginated, it returns the fetched records AND a
// *muninn.ErrTruncated (detectable via errors.As) carrying the real total.
func (c *Client) Search(ctx context.Context, q muninn.Query) ([]muninn.Tender, error) {
	return c.ods.Search(ctx, q)
}

// buildWhere builds the full ODSQL `where` clause, combining with AND the
// keyword clause (what actually filters, since the v2.1 API ignores `q`) and
// the structured filters (departments, dates) on confirmed top-level fields. An
// empty Query returns "" (no filter → the whole dataset).
func buildWhere(q muninn.Query) string {
	return ods.And(ods.KeywordClause(q), deptClause(q), dateClause(q))
}

// deptClause filters on the confirmed top-level code_departement field.
func deptClause(q muninn.Query) string {
	if len(q.Departements) == 0 {
		return ""
	}
	var parts []string
	for _, d := range q.Departements {
		parts = append(parts, fmt.Sprintf(`code_departement="%s"`, ods.Escape(d)))
	}
	return "(" + strings.Join(parts, " OR ") + ")"
}

// dateClause bounds the publication date (dateparution) by q.DateFrom/DateTo.
func dateClause(q muninn.Query) string {
	var parts []string
	if !q.DateFrom.IsZero() {
		parts = append(parts, fmt.Sprintf(`dateparution >= "%s"`, q.DateFrom.Format("2006-01-02")))
	}
	if !q.DateTo.IsZero() {
		parts = append(parts, fmt.Sprintf(`dateparution <= "%s"`, q.DateTo.Format("2006-01-02")))
	}
	return strings.Join(parts, " AND ")
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
		if parsed, err := ods.ParseDate(v); err == nil {
			t.DatePublication = parsed
		}
	}
	if v, ok := rec["datelimitereponse"].(string); ok {
		if parsed, err := ods.ParseDate(v); err == nil {
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
