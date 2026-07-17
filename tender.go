package muninn

import (
	"fmt"
	"time"
)

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

// Buyer represents an economic actor of a tender: either the public buyer or,
// when used as Tender.Supplier, the awarded contractor (titulaire).
type Buyer struct {
	Nom             string
	SIRET           string
	SIREN           string
	Ville           string
	CodeDepartement string
}

// SIREN9 returns the 9-digit SIREN identifying the legal entity, derived from
// SIREN when set, otherwise from the first 9 digits of SIRET (a SIRET is a
// SIREN plus a 5-digit establishment number). It returns "" when neither yields
// a plausible SIREN. This is the stable key used to relate an actor across
// sources (a buyer or a supplier keeps its SIREN, its SIRET may vary per site).
func (b Buyer) SIREN9() string {
	if len(b.SIREN) >= 9 {
		return b.SIREN[:9]
	}
	if len(b.SIRET) >= 9 {
		return b.SIRET[:9]
	}
	return ""
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

	// Supplier is the awarded contractor (titulaire) when the notice is an award
	// result. Its zero value means the contract is not yet awarded or the winner
	// is unknown for this source (notices from the tendering phase have none).
	Supplier Buyer

	AvisType   AvisType
	Procedure  ProcedureType
	Engagement EngagementType

	DatePublication   time.Time
	DateLimiteReponse time.Time

	// MontantEstime is the contract amount in euros, 0 when not disclosed. Its
	// authority depends on the source: DECP reports the legally binding awarded
	// amount, BEAUAMP an indicative consolidated value, BOAMP rarely any. When
	// consolidating several sources, prefer the DECP value.
	MontantEstime float64

	URL string

	// RawFields keeps the origin provider's unmapped raw fields, useful while
	// the common model is still being enriched, or for debugging.
	RawFields map[string]any
}

// DedupKey returns a stable key relating two notices that likely concern the
// same contract across sources (e.g. a BEAUAMP notice and its DECP award). It
// keys on the buyer SIREN plus the primary CPV code — both provided by BEAUAMP
// and DECP — which is robust to per-site SIRET and title-wording differences.
// It falls back to buyer SIRET + title, then to source + native ID when no
// stronger key is available.
func (t Tender) DedupKey() string {
	if siren := t.Buyer.SIREN9(); siren != "" && len(t.CPVCodes) > 0 {
		return siren + "|" + cpvRoot(t.CPVCodes[0])
	}
	if t.Buyer.SIRET != "" && t.Titre != "" {
		return t.Buyer.SIRET + "|" + t.Titre
	}
	return t.Source + "|" + t.SourceID
}

// cpvRoot normalizes a CPV code to its 8-digit root, dropping the optional
// "-N" check digit. Sources disagree on the suffix (DECP "79953000-9" vs
// BEAUAMP "79953000"), so the root is what makes them comparable.
func cpvRoot(cpv string) string {
	if i := len(cpv); i > 8 {
		return cpv[:8]
	}
	return cpv
}

// ErrTruncated signals that a paginated Search could not fetch every matching
// record — the total exceeded the source's pagination window or the requested
// Query.Limit. Retrieved is how many records were actually returned, Total the
// real number of matches. It is returned alongside the fetched subset, so a
// caller may treat it as a warning (via errors.As) and still use the records.
type ErrTruncated struct {
	Retrieved int
	Total     int
}

func (e *ErrTruncated) Error() string {
	return fmt.Sprintf("muninn: truncated results: %d retrieved out of %d",
		e.Retrieved, e.Total)
}
