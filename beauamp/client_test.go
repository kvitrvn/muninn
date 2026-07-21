package beauamp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/kvitrvn/muninn"
)

func TestMapRecord(t *testing.T) {
	rec := map[string]any{
		"id_boamp_attribution":           "26-53756",
		"objet":                          "Maintenance du logiciel de GED",
		"cpv":                            "72000000",
		"procedure":                      "negociee",
		"decision":                       "attribue",
		"siren_acheteur":                 "267500452",
		"nom_declare_acheteur":           "AP-HP",
		"siren_fournisseur":              "428692701",
		"nom_declare_fournisseur":        "ENNOV",
		"valeur_max_totale_accord_cadre": float64(840136),
		"date_avis_attribution":          "2026-06-03",
	}

	got := mapRecord(rec)

	if got.Source != "beauamp" || got.SourceID != "26-53756" {
		t.Errorf("Source/ID = %q/%q", got.Source, got.SourceID)
	}
	if got.AvisType != muninn.AvisAttribution {
		t.Errorf("AvisType = %v, want AvisAttribution", got.AvisType)
	}
	if got.Buyer.SIREN != "267500452" || got.Buyer.Nom != "AP-HP" {
		t.Errorf("Buyer = %+v", got.Buyer)
	}
	if got.Supplier.SIREN != "428692701" || got.Supplier.Nom != "ENNOV" {
		t.Errorf("Supplier = %+v", got.Supplier)
	}
	if got.MontantEstime != 840136 {
		t.Errorf("MontantEstime = %v", got.MontantEstime)
	}
	if got.Procedure != muninn.ProcedureNegocieeAvecPublicite {
		t.Errorf("Procedure = %v", got.Procedure)
	}
	if got.DatePublication.Format("2006-01-02") != "2026-06-03" {
		t.Errorf("DatePublication = %v", got.DatePublication)
	}
}

// newTabularServer serves a fixed set of rows, honoring objet__contains and
// page/page_size like the data.gouv.fr tabular API. CPV/amount/SIREN filters
// are also applied server-side so the test mirrors the real per-column push.
func newTabularServer(t *testing.T, rows []map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		term := q.Get("objet__contains")
		wantCPV := q.Get("cpv__startswith")
		minAmt := q.Get("valeur_totale__gte")
		maxAmt := q.Get("valeur_totale__lte")
		wantSIREN := q.Get("siren_acheteur")

		var matched []map[string]any
		for _, row := range rows {
			if term != "" && !strings.Contains(strings.ToLower(anyStr(row["objet"])), strings.ToLower(term)) {
				continue
			}
			if wantCPV != "" && !strings.HasPrefix(anyStr(row["cpv"]), wantCPV) {
				continue
			}
			if minAmt != "" {
				amt := anyFloat(row["valeur_totale"])
				if amt < toFloat(minAmt) {
					continue
				}
			}
			if maxAmt != "" {
				amt := anyFloat(row["valeur_totale"])
				if amt > toFloat(maxAmt) {
					continue
				}
			}
			if wantSIREN != "" && anyStr(row["siren_acheteur"]) != wantSIREN {
				continue
			}
			matched = append(matched, row)
		}
		size, _ := strconv.Atoi(q.Get("page_size"))
		if size <= 0 {
			size = 20
		}
		page, _ := strconv.Atoi(q.Get("page"))
		if page <= 0 {
			page = 1
		}
		start := (page - 1) * size
		end := start + size
		var pageRows []map[string]any
		if start < len(matched) {
			if end > len(matched) {
				end = len(matched)
			}
			pageRows = matched[start:end]
		}

		resp := map[string]any{
			"data": pageRows,
			"meta": map[string]any{"page": page, "page_size": size, "total": len(matched)},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

// anyStr returns the value as a string, tolerating numeric or boolean types so
// tests can use literal "72000000" or float64(840136) interchangeably.
func anyStr(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case int:
		return strconv.Itoa(x)
	}
	return ""
}

// anyFloat reads a value as a float64, returning 0 when absent or unparseable.
func anyFloat(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case int:
		return float64(x)
	case string:
		f, _ := strconv.ParseFloat(x, 64)
		return f
	}
	return 0
}

func toFloat(s string) float64 {
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

func testClient(url string) *Client {
	return New(WithTabularBaseURL(url+"/"), WithResources("res-1"))
}

func TestSearch_ORUnionAndDedup(t *testing.T) {
	rows := []map[string]any{
		{"id_boamp_attribution": "a", "objet": "Solution GED", "siren_acheteur": "111111111", "cpv": "72000000"},
		{"id_boamp_attribution": "b", "objet": "Archivage électronique", "siren_acheteur": "222222222", "cpv": "72500000"},
		// Same buyer+CPV as "a": must be deduplicated by DedupKey.
		{"id_boamp_attribution": "a2", "objet": "GED complémentaire", "siren_acheteur": "111111111", "cpv": "72000000"},
	}
	srv := newTabularServer(t, rows)
	defer srv.Close()

	got, err := testClient(srv.URL).Search(context.Background(),
		muninn.Query{Keywords: []string{"GED", "archivage"}})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	// "GED" matches a and a2 (deduped to one), "archivage" matches b → 2 tenders.
	if len(got) != 2 {
		t.Fatalf("got %d tenders, want 2: %+v", len(got), got)
	}
}

func TestSearch_MatchAllClientSide(t *testing.T) {
	rows := []map[string]any{
		{"id_boamp_attribution": "a", "objet": "Solution GED et archivage électronique", "siren_acheteur": "1", "cpv": "72"},
		{"id_boamp_attribution": "b", "objet": "Solution GED seule", "siren_acheteur": "2", "cpv": "73"},
	}
	srv := newTabularServer(t, rows)
	defer srv.Close()

	got, err := testClient(srv.URL).Search(context.Background(),
		muninn.Query{Keywords: []string{"GED", "archivage"}, MatchAll: true})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 1 || got[0].SourceID != "a" {
		t.Fatalf("MatchAll = %+v, want only record a", got)
	}
}

func TestCount_SumsTotals(t *testing.T) {
	rows := []map[string]any{
		{"objet": "GED un", "siren_acheteur": "1", "cpv": "72"},
		{"objet": "GED deux", "siren_acheteur": "2", "cpv": "73"},
		{"objet": "archivage", "siren_acheteur": "3", "cpv": "74"},
	}
	srv := newTabularServer(t, rows)
	defer srv.Close()

	n, err := testClient(srv.URL).Count(context.Background(),
		muninn.Query{Keywords: []string{"GED", "archivage"}})
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	// GED total (2) + archivage total (1) = 3 (OR upper bound).
	if n != 3 {
		t.Errorf("Count = %d, want 3", n)
	}
}

func TestSearch_AdvancedFiltersPushed(t *testing.T) {
	rows := []map[string]any{
		{"id_boamp_attribution": "a", "objet": "GED", "siren_acheteur": "111111111", "cpv": "72000000", "valeur_totale": float64(50000)},
		{"id_boamp_attribution": "b", "objet": "GED", "siren_acheteur": "222222222", "cpv": "30190000", "valeur_totale": float64(500000)},
		{"id_boamp_attribution": "c", "objet": "GED", "siren_acheteur": "222222222", "cpv": "72000000", "valeur_totale": float64(5000000)},
	}
	srv := newTabularServer(t, rows)
	defer srv.Close()

	got, err := testClient(srv.URL).Search(context.Background(),
		muninn.Query{
			Keywords:   []string{"GED"},
			CPVCodes:   []string{"72"},
			MontantMin: 100000,
			BuyerSIREN: "222222222",
		})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	// Only "c" matches all four filters (cpv 72*, montant >= 100k, SIREN 222).
	if len(got) != 1 || got[0].SourceID != "c" {
		t.Errorf("got %d tenders, want only c: %+v", len(got), got)
	}
}

func TestParseMonthlyTitle(t *testing.T) {
	cases := map[string]struct {
		year, month int
		ok          bool
	}{
		"beauamp_juin_2026_1.1.0.csv":     {2026, 6, true},
		"beauamp_décembre_2025_1.1.0.csv": {2025, 12, true},
		"beauamp_2025_1.1.0.csv":          {0, 0, false}, // yearly
		"beauamp-16-07-2026.csv":          {0, 0, false}, // daily
	}
	for title, want := range cases {
		y, m, ok := parseMonthlyTitle(title)
		if ok != want.ok || (ok && (y != want.year || m != want.month)) {
			t.Errorf("%s → (%d,%d,%v), want (%d,%d,%v)", title, y, m, ok, want.year, want.month, want.ok)
		}
	}
}
