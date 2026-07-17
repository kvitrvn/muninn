package muninn

import "time"

// ProcedureType is the award procedure type, independent of the engagement
// type (see EngagementType).
type ProcedureType int

const (
	ProcedureInconnue ProcedureType = iota
	ProcedureOuverte
	ProcedureRestreinte
	ProcedureNegocieeAvecPublicite
	ProcedureNegocieeSansPublicite
	ProcedureDialogueCompetitif
	ProcedureConcours
)

func (p ProcedureType) String() string {
	switch p {
	case ProcedureOuverte:
		return "ouverte"
	case ProcedureRestreinte:
		return "restreinte"
	case ProcedureNegocieeAvecPublicite:
		return "negociee_avec_publicite"
	case ProcedureNegocieeSansPublicite:
		return "negociee_sans_publicite"
	case ProcedureDialogueCompetitif:
		return "dialogue_competitif"
	case ProcedureConcours:
		return "concours"
	default:
		return "inconnue"
	}
}

// EngagementType is the contractual engagement type: firm contract or
// framework agreement (purchase-order based or with subsequent contracts).
// This is a separate axis from the procedure type, and the two combine.
type EngagementType int

const (
	EngagementInconnu EngagementType = iota
	EngagementFerme
	EngagementAccordCadreBC // purchase-order framework agreement
	EngagementAccordCadreMS // subsequent-contract framework agreement
)

func (e EngagementType) String() string {
	switch e {
	case EngagementFerme:
		return "marche_ferme"
	case EngagementAccordCadreBC:
		return "accord_cadre_bons_de_commande"
	case EngagementAccordCadreMS:
		return "accord_cadre_marches_subsequents"
	default:
		return "inconnu"
	}
}

// AvisType distinguishes a call-for-competition notice from an award/result
// notice.
type AvisType int

const (
	AvisInconnu AvisType = iota
	AvisAppelConcurrence
	AvisAttribution
	AvisRectificatif
)

// Buyer represents the public buyer.
type Buyer struct {
	Nom             string
	SIRET           string
	SIREN           string
	Ville           string
	CodeDepartement string
}

// Tender is the normalized representation of a public procurement notice,
// whatever its source. Each Provider maps its native format to this type.
type Tender struct {
	// Source identifies the originating provider ("boamp", "decp", "place"...).
	Source string
	// SourceID is the notice's native identifier at the provider (used for
	// traceability and as a secondary deduplication key).
	SourceID string

	Titre    string
	Objet    string
	CPVCodes []string
	Buyer    Buyer

	AvisType   AvisType
	Procedure  ProcedureType
	Engagement EngagementType

	DatePublication   time.Time
	DateLimiteReponse time.Time

	MontantEstime float64 // 0 if not disclosed

	URL string

	// RawFields keeps the origin provider's unmapped raw fields, useful while
	// the common model is still being enriched, or for debugging.
	RawFields map[string]any
}

// DedupKey returns a stable key to relate two notices that may concern the same
// contract (e.g. an initial BOAMP notice and its award result from another
// source). Useful to deduplicate a multi-provider aggregation.
func (t Tender) DedupKey() string {
	if t.Buyer.SIRET != "" && t.Titre != "" {
		return t.Buyer.SIRET + "|" + t.Titre
	}
	return t.Source + "|" + t.SourceID
}
