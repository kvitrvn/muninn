package decp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kvitrvn/muninn"
	"github.com/kvitrvn/muninn/internal/ods"
	"github.com/kvitrvn/muninn/retry"
)

func TestMapRecord_Award(t *testing.T) {
	rec := map[string]any{
		"id":                          "20242400000028",
		"objet":                       "Solution de gestion électronique de documents",
		"codecpv":                     "72000000-5",
		"procedure":                   "Appel d'offres ouvert",
		"acheteur_id":                 "21050023700016",
		"titulaire_id_1":              "49884169100039",
		"titulaire_typeidentifiant_1": "SIRET",
		"montant":                     754853.43,
		"datenotification":            "2024-04-25",
	}

	got := mapRecord(rec)

	if got.Sources[0].Provider != "decp" {
		t.Errorf("source = %+v", got.Sources[0])
	}
	if got.AvisType != muninn.AvisAttribution {
		t.Errorf("AvisType = %v, want AvisAttribution", got.AvisType)
	}
	if got.Buyer.SIRET != "21050023700016" || got.Buyer.SIREN9() != "210500237" {
		t.Errorf("Buyer = %+v", got.Buyer)
	}
	if got.Supplier.SIRET != "49884169100039" {
		t.Errorf("Supplier.SIRET = %q, want the titulaire SIRET", got.Supplier.SIRET)
	}
	if got.MontantEstime != 754853.43 {
		t.Errorf("MontantEstime = %v, want 754853.43", got.MontantEstime)
	}
	if got.Procedure != muninn.ProcedureOuverte {
		t.Errorf("Procedure = %v, want ProcedureOuverte", got.Procedure)
	}
	if got.DatePublication.Format("2006-01-02") != "2024-04-25" {
		t.Errorf("DatePublication = %v", got.DatePublication)
	}
}

// A non-SIRET titulaire identifier (e.g. "CDL") must not be mistaken for a
// supplier SIRET.
func TestMapRecord_NonSIRETSupplier(t *testing.T) {
	rec := map[string]any{
		"id":                          "x",
		"titulaire_id_1":              "CDL",
		"titulaire_typeidentifiant_1": "CDL",
	}
	if got := mapRecord(rec); got.Supplier.SIRET != "" {
		t.Errorf("Supplier.SIRET = %q, want empty", got.Supplier.SIRET)
	}
}

func TestReadAmount(t *testing.T) {
	if got := readAmount(1234.5); got != 1234.5 {
		t.Errorf("float = %v", got)
	}
	if got := readAmount("1234.5"); got != 1234.5 {
		t.Errorf("string = %v", got)
	}
	if got := readAmount(nil); got != 0 {
		t.Errorf("nil = %v", got)
	}
}

func TestBuildWhere_AdvancedFilters(t *testing.T) {
	got := buildWhere(muninn.Query{
		CPVCodes:   []string{"72", "3019"},
		MontantMin: 40000,
		MontantMax: 500000,
		BuyerSIREN: "210500237",
	})
	want := `(codecpv starts with "72" OR codecpv starts with "3019") AND (montant >= 40000 AND montant <= 500000) AND acheteur_id starts with "210500237"`
	if got != want {
		t.Errorf("buildWhere() = %q\nwant: %q", got, want)
	}
}

func TestBuildWhere_NoAdvancedFilters(t *testing.T) {
	// Without advanced filters, buildWhere must not emit any clause for them.
	got := buildWhere(muninn.Query{Keywords: []string{"GED"}})
	if got != `("GED")` {
		t.Errorf("buildWhere() = %q, want %q", got, `("GED")`)
	}
}

func TestCPVClause(t *testing.T) {
	if got := ods.CPVClause(muninn.Query{CPVCodes: []string{"72"}}, "codecpv"); got != `(codecpv starts with "72")` {
		t.Errorf("CPVClause = %q", got)
	}
	if got := ods.CPVClause(muninn.Query{}, "codecpv"); got != "" {
		t.Errorf("empty CPVClause = %q", got)
	}
}

func TestAmountClause(t *testing.T) {
	if got := ods.AmountClause(muninn.Query{MontantMin: 100, MontantMax: 1000}, "m"); got != `(m >= 100 AND m <= 1000)` {
		t.Errorf("AmountClause = %q", got)
	}
	if got := ods.AmountClause(muninn.Query{MontantMin: 100}, "m"); got != `(m >= 100)` {
		t.Errorf("AmountClause min only = %q", got)
	}
	if got := ods.AmountClause(muninn.Query{}, "m"); got != "" {
		t.Errorf("empty AmountClause = %q", got)
	}
}

// TestSearch_RetriesTransientServerError verifies WithRetryPolicy (exported
// via the public retry package, since decp is backed by internal/ods.Client)
// actually retries a transient 500 instead of failing Search outright.
func TestSearch_RetriesTransientServerError(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&calls, 1) <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"total_count": 1,
			"results":     []map[string]any{{"id": "rec-0"}},
		})
	}))
	defer srv.Close()

	c := New(
		WithBaseURL(srv.URL),
		WithHTTPClient(http.DefaultClient),
		WithRetryPolicy(retry.Policy{MaxAttempts: 4, BaseDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond}),
	)
	got, err := c.Search(context.Background(), muninn.Query{Keywords: []string{"GED"}})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got.Items) != 1 || got.Items[0].Sources[0].ID != "rec-0" {
		t.Fatalf("got %+v, want [rec-0]", got)
	}
	if calls != 3 {
		t.Errorf("calls = %d, want 3 (2 failures + 1 success)", calls)
	}
}

func TestSIRENClause(t *testing.T) {
	if got := ods.SIRENClause(muninn.Query{BuyerSIREN: "111"}, "col"); got != `col = "111"` {
		t.Errorf("SIRENClause = %q", got)
	}
	if got := ods.SIRENClause(muninn.Query{}, "col"); got != "" {
		t.Errorf("empty SIRENClause = %q", got)
	}
}
