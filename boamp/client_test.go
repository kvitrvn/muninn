package boamp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/kvitrvn/muninn"
)

// This fixture reproduces the shape of a BOAMP record: top-level fields
// (idweb/objet/nomacheteur/dateparution/nature_categorise_libelle) and the
// nested "donnees" field (XML-to-JSON) with OBJET.PROCEDURE.TYPE_PROCEDURE.OUVERT
// and OBJET.CPV.PRINCIPAL. It validates the mapping logic, not the exact shape
// of "donnees" in every case (multi-lot notably).
const fixtureRecord = `{
	"idweb": "23-123456",
	"objet": "Acquisition d'une solution de gestion électronique de documents (GED)",
	"nomacheteur": "Ministère de la Transition Numérique",
	"code_departement": "75",
	"dateparution": "2026-07-01",
	"datelimitereponse": "2026-08-15",
	"nature_categorise_libelle": "Avis de marché",
	"type_marche": "Marché",
	"url_avis": "https://www.boamp.fr/avis/23-123456",
	"donnees": "{\"OBJET\":{\"PROCEDURE\":{\"TYPE_PROCEDURE\":{\"OUVERT\":\"\"}},\"CPV\":{\"PRINCIPAL\":\"72000000\"}}}"
}`

func TestMapRecord_ContractNotice(t *testing.T) {
	var rec map[string]any
	if err := json.Unmarshal([]byte(fixtureRecord), &rec); err != nil {
		t.Fatalf("fixture invalide: %v", err)
	}

	got := mapRecord(rec)

	if got.SourceID != "23-123456" {
		t.Errorf("SourceID = %q, attendu %q", got.SourceID, "23-123456")
	}
	if got.Buyer.Nom != "Ministère de la Transition Numérique" {
		t.Errorf("Buyer.Nom = %q", got.Buyer.Nom)
	}
	if got.AvisType != muninn.AvisAppelConcurrence {
		t.Errorf("AvisType = %v, attendu AvisAppelConcurrence", got.AvisType)
	}
	if got.Procedure != muninn.ProcedureOuverte {
		t.Errorf("Procedure = %v, attendu ProcedureOuverte", got.Procedure)
	}
	if len(got.CPVCodes) != 1 || got.CPVCodes[0] != "72000000" {
		t.Errorf("CPVCodes = %v, attendu [72000000]", got.CPVCodes)
	}
	if got.DatePublication.Format("2006-01-02") != "2026-07-01" {
		t.Errorf("DatePublication = %v", got.DatePublication)
	}
	if got.DateLimiteReponse.Format("2006-01-02") != "2026-08-15" {
		t.Errorf("DateLimiteReponse = %v", got.DateLimiteReponse)
	}
}

func TestMapRecord_FrameworkAgreement(t *testing.T) {
	rec := map[string]any{
		"idweb":       "23-999999",
		"objet":       "Accord-cadre logiciel libre souveraineté numérique",
		"nomacheteur": "Région Test",
		"donnees":     `{"OBJET":{"PROCEDURE":{"TYPE_PROCEDURE":{"RESTREINT":""}},"ACCORD_CADRE_OUI":""}}`,
	}

	got := mapRecord(rec)

	if got.Procedure != muninn.ProcedureRestreinte {
		t.Errorf("Procedure = %v, attendu ProcedureRestreinte", got.Procedure)
	}
	if got.Engagement != muninn.EngagementAccordCadreBC {
		t.Errorf("Engagement = %v, attendu EngagementAccordCadreBC", got.Engagement)
	}
}

// The API returns code_departement as an array (e.g. ["35"]); verify the
// mapping handles it and does not silently fail.
func TestMapRecord_CodeDepartementArray(t *testing.T) {
	rec := map[string]any{
		"idweb":            "24-000001",
		"objet":            "GED",
		"code_departement": []any{"35", "44"},
	}
	got := mapRecord(rec)
	if got.Buyer.CodeDepartement != "35" {
		t.Errorf("CodeDepartement = %q, attendu %q", got.Buyer.CodeDepartement, "35")
	}
}

func TestBuildWhere(t *testing.T) {
	tests := []struct {
		name string
		q    muninn.Query
		want string
	}{
		{
			name: "vide",
			q:    muninn.Query{},
			want: "",
		},
		{
			name: "mots-clés en OR (plein-texte, défaut)",
			q:    muninn.Query{Keywords: []string{"GED", "gestion documentaire"}},
			want: `("GED" OR "gestion documentaire")`,
		},
		{
			name: "objet + ET",
			q:    muninn.Query{Keywords: []string{"IA", "données personnelles"}, ObjetOnly: true, MatchAll: true},
			want: `(objet like "IA" AND objet like "données personnelles")`,
		},
		{
			name: "objet + OU",
			q:    muninn.Query{Keywords: []string{"GED", "SAE"}, ObjetOnly: true},
			want: `(objet like "GED" OR objet like "SAE")`,
		},
		{
			name: "mots-clés + département en AND",
			q:    muninn.Query{Keywords: []string{"GED"}, Departements: []string{"75"}},
			want: `("GED") AND (code_departement="75")`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := buildWhere(tt.q); got != tt.want {
				t.Errorf("buildWhere() = %q, attendu %q", got, tt.want)
			}
		})
	}
}

// newPagingServer simulates the Opendatasoft API: total records served in pages
// according to the offset parameter, with a constant total_count.
func newPagingServer(t *testing.T, total int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if limit <= 0 {
			limit = 10
		}
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))

		results := make([]map[string]any, 0, limit)
		for i := offset; i < offset+limit && i < total; i++ {
			results = append(results, map[string]any{
				"idweb": fmt.Sprintf("rec-%d", i),
				"objet": "GED",
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"total_count": total,
			"results":     results,
		})
	}))
}

func TestSearch_Paginates(t *testing.T) {
	srv := newPagingServer(t, 250)
	defer srv.Close()

	client := New(WithBaseURL(srv.URL))
	got, err := client.Search(context.Background(), muninn.Query{Keywords: []string{"GED"}})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 250 {
		t.Errorf("récupérés = %d, attendu 250", len(got))
	}
	// Check order/completeness via the first and last SourceID.
	if got[0].SourceID != "rec-0" || got[249].SourceID != "rec-249" {
		t.Errorf("bornes = %q..%q", got[0].SourceID, got[249].SourceID)
	}
}

func TestCount(t *testing.T) {
	srv := newPagingServer(t, 250)
	defer srv.Close()

	client := New(WithBaseURL(srv.URL))
	n, err := client.Count(context.Background(), muninn.Query{Keywords: []string{"GED"}})
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if n != 250 {
		t.Errorf("Count = %d, attendu 250", n)
	}
}

func TestSearch_TruncatedBeyondWindow(t *testing.T) {
	// total_count > pagination window (10,000): Search must return the
	// paginable records AND an *ErrTruncated carrying the real total.
	srv := newPagingServer(t, 25000)
	defer srv.Close()

	client := New(WithBaseURL(srv.URL))
	got, err := client.Search(context.Background(), muninn.Query{Keywords: []string{"GED"}})

	var truncated *ErrTruncated
	if !errors.As(err, &truncated) {
		t.Fatalf("attendu *ErrTruncated, obtenu %v", err)
	}
	if truncated.Total != 25000 {
		t.Errorf("Total = %d, attendu 25000", truncated.Total)
	}
	if len(got) != maxOffsetWindow {
		t.Errorf("récupérés = %d, attendu %d (plafond)", len(got), maxOffsetWindow)
	}
}
