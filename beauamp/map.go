package beauamp

import (
	"strings"

	"github.com/kvitrvn/muninn"
)

// mapRecord translates a raw BEAUAMP row into a muninn.Tender. BEAUAMP is
// already structured and SIRENE-matched, so buyer and supplier come straight
// from dedicated columns.
func mapRecord(rec map[string]any) muninn.Tender {
	t := muninn.Tender{
		Source:    "beauamp",
		AvisType:  mapAvisType(rec),
		RawFields: rec,
	}

	t.SourceID = firstString(rec, "id_boamp_attribution", "id_boamp_contrat", "__id")
	if v := str(rec["objet"]); v != "" {
		t.Objet = v
		t.Titre = v
	}
	if v := str(rec["cpv"]); v != "" {
		t.CPVCodes = []string{v}
	}

	t.Buyer = muninn.Buyer{
		Nom:   firstString(rec, "nom_declare_acheteur", "nom_siren_acheteur"),
		SIREN: str(rec["siren_acheteur"]),
		Ville: str(rec["nom_commune_acheteur"]),
	}
	t.Supplier = muninn.Buyer{
		Nom:   firstString(rec, "nom_declare_fournisseur", "nom_siren_fournisseur"),
		SIREN: str(rec["siren_fournisseur"]),
		Ville: str(rec["nom_commune_fournisseur"]),
	}

	// Amount: BEAUAMP spreads it across several columns depending on the contract
	// shape; take the first present, most specific first.
	t.MontantEstime = firstNumber(rec,
		"valeur_totale", "valeur_max_totale_accord_cadre", "valeur_totale_estimee", "valeur_estimee_lot")

	if d := firstString(rec, "date_avis_attribution", "date_avis_marche"); d != "" {
		if parsed, err := parseDate(d); err == nil {
			t.DatePublication = parsed
		}
	}
	t.Procedure = mapProcedure(str(rec["procedure"]))
	return t
}

// mapAvisType uses the decision column ("attribue" for an award).
func mapAvisType(rec map[string]any) muninn.AvisType {
	switch strings.ToLower(str(rec["decision"])) {
	case "attribue", "attribué":
		return muninn.AvisAttribution
	case "":
		return muninn.AvisInconnu
	default:
		return muninn.AvisAppelConcurrence
	}
}

// mapProcedure maps the lowercase procedure slug used by BEAUAMP (e.g.
// "ouverte", "negociee") to a ProcedureType. "adaptee" (MAPA) has no dedicated
// value and maps to ProcedureInconnue.
func mapProcedure(slug string) muninn.ProcedureType {
	switch strings.ToLower(strings.TrimSpace(slug)) {
	case "ouverte":
		return muninn.ProcedureOuverte
	case "restreinte":
		return muninn.ProcedureRestreinte
	case "negociee", "négociée":
		return muninn.ProcedureNegocieeAvecPublicite
	case "dialogue_competitif", "dialogue competitif":
		return muninn.ProcedureDialogueCompetitif
	case "concours":
		return muninn.ProcedureConcours
	default:
		return muninn.ProcedureInconnue
	}
}
