package beauamp

import (
	"strings"

	"github.com/kvitrvn/muninn"
)

// mapRecord translates a raw BEAUAMP row into a muninn.Tender. BEAUAMP is
// already structured and SIRENE-matched, so buyer and supplier come straight
// from dedicated columns.
func mapRecord(rec map[string]any) muninn.Tender {
	attributionID := firstString(rec, "id_boamp_attribution")
	contractID := firstString(rec, "id_boamp_contrat")
	recordID := firstString(rec, "__id", "id")
	relatedIDs := uniqueNonEmpty(attributionID, contractID)
	t := muninn.Tender{
		Sources: []muninn.SourceReference{{
			Provider:   "beauamp",
			RecordID:   recordID,
			RelatedIDs: relatedIDs,
			RawFields:  rec,
		}},
		AvisType: mapAvisType(rec),
	}

	t.Sources[0].ID = firstNonEmptyString(attributionID, contractID, recordID)
	if v := str(rec["objet"]); v != "" {
		t.Objet = v
		t.Titre = v
	}
	t.CPVCodes = uniqueNonEmpty(
		append(nativeStringList(rec["cpv"]), nativeStringList(rec["cpv_supp"])...)...,
	)

	t.Buyer = muninn.Buyer{
		Nom:   firstString(rec, "nom_declare_acheteur", "nom_siren_acheteur"),
		SIREN: str(rec["siren_acheteur"]),
		Ville: str(rec["nom_commune_acheteur"]),
	}
	t.Suppliers = mapSuppliers(rec)

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

	lotID := firstString(rec, "id_lot", "numero_lot", "no_lot")
	lot := muninn.TenderLot{
		ID:            lotID,
		Objet:         firstString(rec, "objet_lot", "objet"),
		CPVCodes:      append([]string(nil), t.CPVCodes...),
		Suppliers:     append([]muninn.Buyer(nil), t.Suppliers...),
		MontantEstime: firstNumber(rec, "prix_attribution_lot", "valeur_estimee_lot", "valeur_lot", "valeur_totale"),
		Sources:       append([]muninn.SourceReference(nil), t.Sources...),
	}
	if lot.ID != "" || len(lot.Suppliers) > 0 || len(lot.CPVCodes) > 0 || lot.MontantEstime > 0 {
		t.Lots = []muninn.TenderLot{lot}
	}
	return t
}

func mapSuppliers(rec map[string]any) []muninn.Buyer {
	sirens := nativeStringList(rec["siren_fournisseur"])
	sirets := append(
		nativeStringList(rec["siret_fournisseur"]),
		nativeStringList(rec["siret_titulaire"])...,
	)
	name := firstString(rec, "nom_declare_fournisseur", "nom_siren_fournisseur")
	city := str(rec["nom_commune_fournisseur"])
	count := max(len(sirens), len(sirets))
	if count == 0 {
		if name == "" && city == "" {
			return nil
		}
		return []muninn.Buyer{{Nom: name, Ville: city}}
	}
	suppliers := make([]muninn.Buyer, 0, count)
	for index := 0; index < count; index++ {
		supplier := muninn.Buyer{Nom: name, Ville: city}
		if index < len(sirens) {
			supplier.SIREN = sirens[index]
		}
		if index < len(sirets) {
			supplier.SIRET = sirets[index]
		}
		suppliers = append(suppliers, supplier)
	}
	return suppliers
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func uniqueNonEmpty(values ...string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range values {
		if value != "" && !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
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
