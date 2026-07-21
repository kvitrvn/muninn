package boamp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

// Pagination, Count and truncation are exercised generically against the shared
// Opendatasoft plumbing in internal/ods; here we only cover the BOAMP-specific
// record mapping and where-clause building.

// TestSearch_AdvancedFiltersPostFetch verifies the CPV / amount / SIREN filters
// are applied client-side after the API paginates, since BOAMP exposes none of
// them as a top-level column.
func TestSearch_AdvancedFiltersPostFetch(t *testing.T) {
	rows := []map[string]any{
		{
			"idweb":       "a",
			"objet":       "GED un",
			"nomacheteur": "Acheteur A",
			"donnees":     `{"OBJET":{"CPV":{"PRINCIPAL":"72000000"}},"ORGANISME":{"ACHETEUR":{"IDENTIFICATION":{"SIREN":"111111111"}}}}`,
		},
		{
			"idweb":       "b",
			"objet":       "Fournitures",
			"nomacheteur": "Acheteur B",
			"donnees":     `{"OBJET":{"CPV":{"PRINCIPAL":"30190000"}},"ORGANISME":{"ACHETEUR":{"IDENTIFICATION":{"SIREN":"222222222"}}}}`,
		},
		{
			"idweb":       "c",
			"objet":       "GED deux",
			"nomacheteur": "Acheteur A",
			"donnees":     `{"OBJET":{"CPV":{"PRINCIPAL":"72500000"}},"ORGANISME":{"ACHETEUR":{"IDENTIFICATION":{"SIREN":"111111111"}}}}`,
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"total_count": len(rows),
			"results":     rows,
		})
	}))
	defer srv.Close()

	c := New(WithBaseURL(srv.URL), WithHTTPClient(http.DefaultClient))
	got, err := c.Search(context.Background(), muninn.Query{
		CPVCodes:   []string{"72"},
		BuyerSIREN: "111111111",
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	// Only "a" and "c" match CPV 72* and buyer SIREN 111111111.
	if len(got) != 2 {
		t.Fatalf("got %d tenders, want 2: %+v", len(got), got)
	}
	ids := map[string]bool{}
	for _, x := range got {
		ids[x.SourceID] = true
	}
	if !ids["a"] || !ids["c"] {
		t.Errorf("ids = %v, want {a, c}", ids)
	}
}

func TestBuildWhere_AdvancedFiltersIgnoredAtWhereLevel(t *testing.T) {
	// BOAMP cannot push CPV/amount/SIREN as a server-side where clause: they
	// live in the nested "donnees" blob. The where clause is unchanged when
	// those filters are set.
	got := buildWhere(muninn.Query{
		Keywords:   []string{"GED"},
		CPVCodes:   []string{"72"},
		MontantMin: 100000,
		BuyerSIREN: "111111111",
	})
	want := `("GED")`
	if got != want {
		t.Errorf("buildWhere() = %q, want %q (advanced filters stay client-side)", got, want)
	}
}
