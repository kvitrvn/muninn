// Package boamp implements muninn.Provider for the BOAMP API (DILA), exposed
// through an Opendatasoft platform (Explore API v2.1).
//
// The top-level field names used here (idweb, objet, nomacheteur,
// code_departement, dateparution, datelimitereponse, type_marche,
// nature_categorise_libelle, donnees, url_avis) are confirmed against the live
// dataset schema (see FetchSchema).
//
// The nested "donnees" field has several observed shapes: legacy BOAMP,
// FNSimple and UBL/eForms. CPV extraction covers all three, while procedure and
// engagement mapping remain best-effort because those vocabularies can evolve.
package boamp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/kvitrvn/muninn"
	"github.com/kvitrvn/muninn/internal/ods"
	"github.com/kvitrvn/muninn/retry"
	"github.com/kvitrvn/muninn/search"
)

const defaultBaseURL = "https://boamp-datadila.opendatasoft.com/api/explore/v2.1/catalog/datasets/boamp/records"

// Client queries the BOAMP API. The Opendatasoft pagination and request
// plumbing lives in internal/ods; this package supplies the base URL, the
// where-clause field names, and the record mapping.
type Client struct {
	ods                  *ods.Client
	supplierNameResolver SupplierNameResolver
}

// Option configures a Client.
type Option func(*Client)

// SupplierNameResolver resolves the known names of a legal entity from its
// SIREN. BOAMP uses them to find legacy award notices that identify winners by
// name only. The default resolver calls the official French company-search API.
type SupplierNameResolver func(ctx context.Context, siren string) ([]string, error)

// WithHTTPClient injects a custom *http.Client (timeouts, instrumented
// transport, proxy...).
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) { c.ods.HTTP = h }
}

// WithBaseURL overrides the base URL (useful for tests against a mock server).
func WithBaseURL(u string) Option {
	return func(c *Client) { c.ods.BaseURL = u }
}

// WithRetryPolicy overrides the retry/backoff policy applied when the API
// responds with 429/5xx. The default is retry.DefaultRetryPolicy.
func WithRetryPolicy(p retry.Policy) Option {
	return func(c *Client) { c.ods.Retry = p }
}

// WithSupplierNameResolver overrides the SIREN-to-name resolver. It is useful
// for offline applications and deterministic tests.
func WithSupplierNameResolver(resolver SupplierNameResolver) Option {
	return func(c *Client) { c.supplierNameResolver = resolver }
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
	client := &Client{ods: inner}
	client.supplierNameResolver = client.resolveSupplierNames
	for _, opt := range opts {
		opt(client)
	}
	return client
}

// Compile-time check: *Client satisfies the muninn.Provider contract.
var _ muninn.Provider = (*Client)(nil)

// Name implements muninn.Provider.
func (c *Client) Name() string { return "boamp" }

// Capabilities declares BOAMP's query guarantees. CPV and buyer identifiers
// remain approximate because recent eForms payloads are mapped best-effort.
func (c *Client) Capabilities() muninn.Capabilities {
	return muninn.Capabilities{
		muninn.FilterTitleKeywords: muninn.Exact,
		muninn.FilterFullText:      muninn.Exact,
		muninn.FilterDepartments:   muninn.Exact,
		muninn.FilterPublication:   muninn.Exact,
		muninn.FilterDeadline:      muninn.Exact,
		muninn.FilterCPV:           muninn.Approximate,
		muninn.FilterBuyerSIREN:    muninn.Approximate,
		muninn.FilterSupplierSIREN: muninn.Approximate,
		muninn.FilterNoticeType:    muninn.Exact,
		muninn.FilterStatusOpen:    muninn.Exact,
		muninn.FilterStatusClosed:  muninn.Exact,
		muninn.FilterStatusAwarded: muninn.Exact,
	}
}

// Search implements muninn.Provider. It fetches every record matching q by
// paginating; keywords and the date/department filters are pushed server-side
// via the `where` clause (the API's `q` parameter is ignored in v2.1). CPV and
// buyer SIREN are mapped best-effort from the nested "donnees" blob and then
// filtered client-side. ProviderResult reports source totals and truncation.
//
// Caveat: mapRecord never populates Tender.MontantEstime (BOAMP notices
// structurally rarely disclose a reliable amount, and no confirmed top-level
// or "donnees" field carries one — see the package doc's eForms caveat).
// Consequently, amount filters are declared unsupported and direct calls
// reject them. Engine routes such searches to capable providers.
func (c *Client) Search(ctx context.Context, q muninn.Query) (muninn.ProviderResult, error) {
	if err := muninn.ValidateProviderQuery(q); err != nil {
		return muninn.ProviderResult{}, err
	}
	if err := q.Validate(); err != nil {
		return muninn.ProviderResult{}, err
	}
	if err := muninn.ValidateCapabilities(q, c.Capabilities()); err != nil {
		return muninn.ProviderResult{}, err
	}
	var (
		supplierNames []string
		resolverErr   error
	)
	if siren := strings.TrimSpace(q.SupplierSIREN); siren != "" && c.supplierNameResolver != nil {
		supplierNames, resolverErr = c.supplierNameResolver(ctx, siren)
	}

	native := *c.ods
	native.Where = func(nativeQuery muninn.Query) string {
		return buildWhereWithSupplierNames(nativeQuery, supplierNames)
	}
	result, err := native.Search(ctx, q)
	if siren := strings.TrimSpace(q.SupplierSIREN); siren != "" {
		for index := range result.Items {
			result.Items[index].Suppliers = identifySuppliers(
				result.Items[index].Suppliers,
				siren,
				supplierNames,
			)
		}
	}
	result.Items = search.AdvancedFilter{
		CPVCodes:      q.CPVCodes,
		BuyerSIREN:    q.BuyerSIREN,
		SupplierSIREN: q.SupplierSIREN,
	}.Apply(result.Items)
	at := q.OpenAt
	if at.IsZero() {
		at = time.Now()
	}
	result.Items = muninn.FilterTenders(result.Items, q, at)
	if len(q.CPVCodes) > 0 || q.BuyerSIREN != "" || q.SupplierSIREN != "" ||
		len(q.NoticeTypes) > 0 || len(q.Statuses) > 0 ||
		!q.DeadlineFrom.IsZero() || !q.DeadlineTo.IsZero() {
		if !result.Truncated {
			result.Total = len(result.Items)
			result.TotalExact = true
		} else {
			result.TotalExact = false
		}
	}
	return result, errors.Join(err, resolverErr)
}

// buildWhere builds the full ODSQL `where` clause, combining with AND the
// keyword clause (what actually filters, since the v2.1 API ignores `q`) and
// the structured filters (departments, dates) on confirmed top-level fields.
// The amount range and buyer SIREN are post-filtered in Search because BOAMP
// exposes neither as a top-level column (the amount is rarely disclosed and the
// buyer id lives in the nested "donnees" blob). An empty Query returns "" (no
// filter → the whole dataset).
func buildWhere(q muninn.Query) string {
	return buildWhereWithSupplierNames(q, nil)
}

func buildWhereWithSupplierNames(q muninn.Query, supplierNames []string) string {
	return ods.And(
		ods.KeywordClause(q),
		cpvSearchClause(q.CPVCodes),
		supplierIdentityClause(q.SupplierSIREN, supplierNames),
		deptClause(q),
		dateClause(q),
	)
}

// cpvSearchClause narrows BOAMP's JSON-stringified donnees field before the
// exact client-side CPV prefix filter runs. ODS search() is intentionally only
// a candidate selector: it is fuzzy, while AdvancedFilter remains authoritative.
func cpvSearchClause(prefixes []string) string {
	var parts []string
	for _, prefix := range prefixes {
		if prefix = strings.TrimSpace(prefix); prefix != "" {
			parts = append(parts, fmt.Sprintf(`search(donnees, "%s")`, ods.Escape(prefix)))
		}
	}
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	default:
		return "(" + strings.Join(parts, " OR ") + ")"
	}
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

// dateClause bounds publication and response deadline dates and notice types.
func dateClause(q muninn.Query) string {
	var parts []string
	if !q.PublishedFrom.IsZero() {
		parts = append(parts, fmt.Sprintf(`dateparution >= "%s"`, q.PublishedFrom.Format("2006-01-02")))
	}
	if !q.PublishedTo.IsZero() {
		parts = append(parts, fmt.Sprintf(`dateparution <= "%s"`, q.PublishedTo.Format("2006-01-02")))
	}
	if !q.DeadlineFrom.IsZero() {
		parts = append(parts, fmt.Sprintf(`datelimitereponse >= "%s"`, q.DeadlineFrom.Format("2006-01-02")))
	}
	if !q.DeadlineTo.IsZero() {
		parts = append(parts, fmt.Sprintf(`datelimitereponse <= "%s"`, q.DeadlineTo.Format("2006-01-02")))
	}
	if len(q.NoticeTypes) > 0 {
		var noticeParts []string
		for _, noticeType := range q.NoticeTypes {
			switch noticeType {
			case muninn.AvisAppelConcurrence:
				noticeParts = append(noticeParts, `nature_categorise_libelle="Avis de marché"`)
			case muninn.AvisAttribution:
				noticeParts = append(noticeParts, `nature_categorise_libelle="Résultat de marché"`)
			case muninn.AvisRectificatif:
				noticeParts = append(noticeParts, `nature_categorise_libelle like "rectifi"`)
			}
		}
		if len(noticeParts) > 0 {
			parts = append(parts, "("+strings.Join(noticeParts, " OR ")+")")
		}
	}
	return strings.Join(parts, " AND ")
}

// mapRecord translates a raw Opendatasoft record into a muninn.Tender. The
// top-level fields are read directly; the finer procedure/CPV detail is read
// from the nested "donnees" field (JSON-stringified from the original
// XML/eForms).
func mapRecord(rec map[string]any) muninn.Tender {
	t := muninn.Tender{
		Sources: []muninn.SourceReference{{Provider: "boamp", RawFields: rec}},
	}

	if v, ok := rec["idweb"].(string); ok {
		t.Sources[0].ID = v
	} else if v, ok := rec["id"].(string); ok {
		t.Sources[0].ID = v
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
		t.Sources[0].URL = v
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
	t.Suppliers = mapTopLevelSuppliers(rec["titulaire"])

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
			// Best-effort: extract the buyer's SIREN/SIRET from the eForms
			// identification block when present, so the post-fetch filter can
			// narrow on it. Only one SIRET typically shows up here, but BOAMP
			// may surface SIRET-like fields under different keys across versions.
			if t.Buyer.SIREN == "" && t.Buyer.SIRET == "" {
				if id := digDict(nested, "ORGANISME", "ACHETEUR", "IDENTIFICATION"); id != nil {
					if v, ok := id["SIREN"].(string); ok && v != "" {
						t.Buyer.SIREN = v
					}
					if v, ok := id["SIRET"].(string); ok && v != "" && t.Buyer.SIRET == "" {
						t.Buyer.SIRET = v
					}
				}
			}
			t.Suppliers = mergeSupplierLists(t.Suppliers, mapNestedSuppliers(nested))
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

// extractCPV reads BOAMP's legacy OBJET.CPV structure, FNSimple codeCPV
// sections and the UBL/eForms commodity classifications used by recent
// notices. These formats collapse singleton XML elements to objects and
// repeated elements to arrays, so each repeatable level accepts either shape.
func extractCPV(nested map[string]any) []string {
	var codes []string
	seen := map[string]bool{}

	if objet, ok := digLocal(nested, "OBJET").(map[string]any); ok {
		codes = appendLegacyCPV(codes, seen, digLocal(objet, "CPV"))

		// Some older payloads expose LOT directly, while the observed BOAMP
		// shape is OBJET.LOTS.LOT. Keep both for backwards compatibility.
		codes = appendLegacyLotCPV(codes, seen, digLocal(objet, "LOT"))
		for _, lots := range objectRecords(digLocal(objet, "LOTS")) {
			codes = appendLegacyLotCPV(codes, seen, digLocal(lots, "LOT"))
		}
	}

	if fns := digLocal(nested, "FNSimple"); fns != nil {
		codes = appendFNSCPV(codes, seen, fns)
	}

	if eforms, ok := digLocal(nested, "EFORMS").(map[string]any); ok {
		for _, documentValue := range eforms {
			for _, document := range objectRecords(documentValue) {
				codes = appendEFormsProjectCPV(codes, seen, digLocal(document, "ProcurementProject"))
				for _, lot := range objectRecords(digLocal(document, "ProcurementProjectLot")) {
					codes = appendEFormsProjectCPV(codes, seen, digLocal(lot, "ProcurementProject"))
				}
			}
		}
	}

	return codes
}

// appendFNSCPV walks initial, rectificatif and attribution FNS documents,
// including their lots, and only interprets values nested below codeCPV as CPV
// candidates. This avoids treating unrelated eight-digit identifiers as CPVs.
func appendFNSCPV(codes []string, seen map[string]bool, value any) []string {
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			codes = appendFNSCPV(codes, seen, item)
		}
	case map[string]any:
		for key, item := range typed {
			if strings.EqualFold(localName(key), "codeCPV") {
				codes = appendFNSCPVValues(codes, seen, item)
				continue
			}
			codes = appendFNSCPV(codes, seen, item)
		}
	}
	return codes
}

func appendFNSCPVValues(codes []string, seen map[string]bool, value any) []string {
	switch typed := value.(type) {
	case string:
		if isCPVCode(typed) {
			codes = appendCPVCode(codes, seen, typed)
		}
	case []any:
		for _, item := range typed {
			codes = appendFNSCPVValues(codes, seen, item)
		}
	case map[string]any:
		for _, item := range typed {
			codes = appendFNSCPVValues(codes, seen, item)
		}
	}
	return codes
}

func isCPVCode(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != 8 {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func appendLegacyLotCPV(codes []string, seen map[string]bool, value any) []string {
	for _, lot := range objectRecords(value) {
		codes = appendLegacyCPV(codes, seen, digLocal(lot, "CPV"))
	}
	return codes
}

func appendLegacyCPV(codes []string, seen map[string]bool, value any) []string {
	if code, ok := value.(string); ok {
		return appendCPVCode(codes, seen, code)
	}
	for _, cpv := range objectRecords(value) {
		codes = appendCPVValue(codes, seen, digLocal(cpv, "PRINCIPAL"))
	}
	return codes
}

func appendEFormsProjectCPV(codes []string, seen map[string]bool, value any) []string {
	for _, project := range objectRecords(value) {
		for _, classification := range objectRecords(digLocal(project, "MainCommodityClassification")) {
			codes = appendEFormsCPVValue(codes, seen, digLocal(classification, "ItemClassificationCode"))
		}
		for _, classification := range objectRecords(digLocal(project, "AdditionalCommodityClassification")) {
			codes = appendEFormsCPVValue(codes, seen, digLocal(classification, "ItemClassificationCode"))
		}
	}
	return codes
}

func appendEFormsCPVValue(codes []string, seen map[string]bool, value any) []string {
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			codes = appendEFormsCPVValue(codes, seen, item)
		}
	case map[string]any:
		if listName, _ := typed["@listName"].(string); listName != "" &&
			!strings.EqualFold(strings.TrimSpace(listName), "cpv") {
			return codes
		}
		codes = appendCPVCode(codes, seen, textValue(typed))
	case string:
		codes = appendCPVCode(codes, seen, typed)
	}
	return codes
}

func appendCPVValue(codes []string, seen map[string]bool, value any) []string {
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			codes = appendCPVValue(codes, seen, item)
		}
	default:
		codes = appendCPVCode(codes, seen, textValue(typed))
	}
	return codes
}

func appendCPVCode(codes []string, seen map[string]bool, code string) []string {
	code = strings.TrimSpace(code)
	if code == "" || seen[code] {
		return codes
	}
	seen[code] = true
	return append(codes, code)
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
