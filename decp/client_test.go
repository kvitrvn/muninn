package decp

import (
	"testing"

	"github.com/kvitrvn/muninn"
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
