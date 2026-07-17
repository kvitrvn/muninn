// Package decp implements muninn.Provider for the DECP dataset (Données
// Essentielles de la Commande Publique), the mandatory open data of awarded
// public contracts of 40,000 € HT or more.
//
// Unlike BOAMP (which announces tenders), DECP describes the award: the winning
// contractor (titulaire) and the binding contract amount. It is therefore the
// authoritative source for depth — who won, for how much. It is exposed through
// the same Opendatasoft Explore API v2.1 as BOAMP, so the request plumbing is
// shared via internal/ods.
//
// Caveat: DECP records carry SIRET identifiers but not the buyer/supplier
// names; combine with BEAUAMP (which has the names) when human-readable labels
// are needed.
package decp

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/kvitrvn/muninn"
	"github.com/kvitrvn/muninn/internal/ods"
)

const (
	baseHost       = "https://data.economie.gouv.fr/api/explore/v2.1/catalog/datasets/"
	defaultDataset = "decp-2022-marches-valides"
)

func datasetURL(id string) string { return baseHost + id + "/records" }

// Client queries the DECP dataset.
type Client struct {
	ods *ods.Client
}

// Option configures a Client.
type Option func(*ods.Client)

// WithHTTPClient injects a custom *http.Client.
func WithHTTPClient(h *http.Client) Option {
	return func(c *ods.Client) { c.HTTP = h }
}

// WithBaseURL overrides the full records endpoint (useful for tests against a
// mock server).
func WithBaseURL(u string) Option {
	return func(c *ods.Client) { c.BaseURL = u }
}

// WithDataset selects a different DECP dataset id on data.economie.gouv.fr
// (e.g. a newer "decp-v3-marches-valides"); the default is decp-2022-marches-valides.
func WithDataset(id string) Option {
	return func(c *ods.Client) { c.BaseURL = datasetURL(id) }
}

// New creates a DECP client.
func New(opts ...Option) *Client {
	inner := &ods.Client{
		Source:  "decp",
		BaseURL: datasetURL(defaultDataset),
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
func (c *Client) Name() string { return "decp" }

// Count returns the total number of awarded contracts matching q.
func (c *Client) Count(ctx context.Context, q muninn.Query) (int, error) {
	return c.ods.Count(ctx, q)
}

// Search implements muninn.Provider by paginating the awarded contracts
// matching q. Like every ODS-backed provider it may return a
// *muninn.ErrTruncated beyond the API pagination window.
func (c *Client) Search(ctx context.Context, q muninn.Query) ([]muninn.Tender, error) {
	return c.ods.Search(ctx, q)
}

// buildWhere combines the keyword clause with a notification-date bound. DECP
// has no simple department field, so q.Departements is ignored here.
func buildWhere(q muninn.Query) string {
	var date []string
	if !q.DateFrom.IsZero() {
		date = append(date, fmt.Sprintf(`datenotification >= "%s"`, q.DateFrom.Format("2006-01-02")))
	}
	if !q.DateTo.IsZero() {
		date = append(date, fmt.Sprintf(`datenotification <= "%s"`, q.DateTo.Format("2006-01-02")))
	}
	return ods.And(ods.KeywordClause(q), strings.Join(date, " AND "))
}

// mapRecord translates a raw DECP record into a muninn.Tender. Every DECP record
// is an award, so AvisType is always AvisAttribution.
func mapRecord(rec map[string]any) muninn.Tender {
	t := muninn.Tender{
		Source:    "decp",
		AvisType:  muninn.AvisAttribution,
		RawFields: rec,
	}

	if v, ok := rec["id"].(string); ok {
		t.SourceID = v
	}
	if v, ok := rec["objet"].(string); ok {
		t.Objet = v
		t.Titre = v
	}
	if v, ok := rec["codecpv"].(string); ok && v != "" {
		t.CPVCodes = []string{v}
	}
	if v, ok := rec["acheteur_id"].(string); ok {
		t.Buyer.SIRET = v
	}
	// The winning contractor: the first titulaire slot, only when identified by
	// SIRET (some records use non-SIRET schemes like "CDL").
	if typ, _ := rec["titulaire_typeidentifiant_1"].(string); typ == "SIRET" {
		if v, ok := rec["titulaire_id_1"].(string); ok {
			t.Supplier.SIRET = v
		}
	}
	t.MontantEstime = readAmount(rec["montant"])
	if v, ok := rec["datenotification"].(string); ok {
		if parsed, err := ods.ParseDate(v); err == nil {
			t.DatePublication = parsed
		}
	}
	t.Procedure = mapProcedure(rec)
	return t
}

// readAmount reads the "montant" field, tolerating both a JSON number and a
// numeric string.
func readAmount(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case string:
		var f float64
		if _, err := fmt.Sscanf(n, "%f", &f); err == nil {
			return f
		}
	}
	return 0
}

// mapProcedure maps the free-text "procedure" label to a ProcedureType. The
// "procédure adaptée" (MAPA) label has no dedicated enum value and maps to
// ProcedureInconnue.
func mapProcedure(rec map[string]any) muninn.ProcedureType {
	lower := strings.ToLower(strings.TrimSpace(fmt.Sprint(rec["procedure"])))
	switch {
	case strings.Contains(lower, "ouvert"):
		return muninn.ProcedureOuverte
	case strings.Contains(lower, "restreint"):
		return muninn.ProcedureRestreinte
	case strings.Contains(lower, "dialogue"):
		return muninn.ProcedureDialogueCompetitif
	case strings.Contains(lower, "concours"):
		return muninn.ProcedureConcours
	case strings.Contains(lower, "négoci"), strings.Contains(lower, "negoci"):
		if strings.Contains(lower, "sans publicité") || strings.Contains(lower, "sans mise en concurrence") {
			return muninn.ProcedureNegocieeSansPublicite
		}
		return muninn.ProcedureNegocieeAvecPublicite
	default:
		return muninn.ProcedureInconnue
	}
}
