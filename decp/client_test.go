package decp

import (
	"testing"

	"github.com/kvitrvn/muninn"
	"github.com/kvitrvn/muninn/internal/ods"
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

	if got.Source != "decp" {
		t.Errorf("Source = %q", got.Source)
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

func TestSIRENClause(t *testing.T) {
	if got := ods.SIRENClause(muninn.Query{BuyerSIREN: "111"}, "col"); got != `col = "111"` {
		t.Errorf("SIRENClause = %q", got)
	}
	if got := ods.SIRENClause(muninn.Query{}, "col"); got != "" {
		t.Errorf("empty SIRENClause = %q", got)
	}
}
