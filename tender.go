package muninn

import (
	"sort"
	"strings"
	"time"
	"unicode"
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

func (a AvisType) String() string {
	switch a {
	case AvisAppelConcurrence:
		return "appel_concurrence"
	case AvisAttribution:
		return "attribution"
	case AvisRectificatif:
		return "rectificatif"
	default:
		return "inconnu"
	}
}

// TenderStatus is the lifecycle state derived from notice type, response
// deadline and award information at a given instant.
type TenderStatus int

const (
	StatusUnknown TenderStatus = iota
	StatusOpen
	StatusClosed
	StatusAwarded
)

func (s TenderStatus) String() string {
	switch s {
	case StatusOpen:
		return "open"
	case StatusClosed:
		return "closed"
	case StatusAwarded:
		return "awarded"
	default:
		return "unknown"
	}
}

// Buyer represents an economic actor of a tender: either the public buyer or
// one of the awarded contractors (titulaires) in Tender.Suppliers.
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

// SourceReference preserves the identity and raw payload of one source that
// contributed to a normalized Tender.
type SourceReference struct {
	Provider  string
	ID        string
	URL       string
	RawFields map[string]any
}

// Tender is the normalized representation of a public procurement notice.
type Tender struct {
	Sources []SourceReference

	Titre    string
	Objet    string
	CPVCodes []string
	Buyer    Buyer

	// Suppliers are the awarded contractors (titulaires) when the notice is an
	// award result. An empty list means the contract is not yet awarded or the
	// winners are unknown for this source.
	Suppliers []Buyer

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
}

// DedupKey identifies the record itself, not a speculative cross-source
// contract match. Cross-source consolidation uses stricter evidence in
// MergeTenders.
func (t Tender) DedupKey() string {
	if len(t.Sources) > 0 && t.Sources[0].Provider != "" && t.Sources[0].ID != "" {
		return t.Sources[0].Provider + "|" + t.Sources[0].ID
	}
	parts := []string{
		t.primaryProvider(),
		t.Buyer.SIREN9(),
		normalizeObject(firstNonEmpty(t.Objet, t.Titre)),
		cpvRootSet(t.CPVCodes),
	}
	if !t.DatePublication.IsZero() {
		parts = append(parts, t.DatePublication.Format("2006-01-02"))
	}
	return strings.Join(parts, "|")
}

// StatusAt derives the lifecycle status at at. Deadlines are compared at day
// granularity because several public datasets expose no response time.
func (t Tender) StatusAt(at time.Time) TenderStatus {
	if t.AvisType == AvisAttribution || hasKnownSupplier(t.Suppliers) {
		return StatusAwarded
	}
	if t.AvisType != AvisAppelConcurrence && t.AvisType != AvisRectificatif {
		return StatusUnknown
	}
	if t.DateLimiteReponse.IsZero() {
		return StatusUnknown
	}
	if dateOnly(t.DateLimiteReponse).Before(dateOnly(at)) {
		return StatusClosed
	}
	return StatusOpen
}

// ProviderNames returns the sorted, deduplicated providers contributing to t.
func (t Tender) ProviderNames() []string {
	seen := map[string]bool{}
	var names []string
	for _, source := range t.Sources {
		if source.Provider != "" && !seen[source.Provider] {
			seen[source.Provider] = true
			names = append(names, source.Provider)
		}
	}
	sort.Strings(names)
	return names
}

func (t Tender) primaryProvider() string {
	if len(t.Sources) == 0 {
		return ""
	}
	return t.Sources[0].Provider
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

// cpvRootSet canonicalizes a CPV code list into an order-independent,
// deduplicated key component: sources may list the same contract's CPV codes
// in different orders (or repeat one), which would otherwise make DedupKey
// depend on which code happens to come first.
func cpvRootSet(cpvs []string) string {
	seen := map[string]bool{}
	var roots []string
	for _, c := range cpvs {
		if r := cpvRoot(c); r != "" && !seen[r] {
			seen[r] = true
			roots = append(roots, r)
		}
	}
	sort.Strings(roots)
	return strings.Join(roots, ",")
}

func normalizeObject(value string) string {
	var b strings.Builder
	space := false
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			b.WriteRune(r)
			space = false
			continue
		}
		space = true
	}
	return b.String()
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func isZeroBuyer(b Buyer) bool {
	return b.Nom == "" && b.SIRET == "" && b.SIREN == "" && b.Ville == "" && b.CodeDepartement == ""
}

func hasKnownSupplier(suppliers []Buyer) bool {
	for _, supplier := range suppliers {
		if !isZeroBuyer(supplier) {
			return true
		}
	}
	return false
}

func dateOnly(value time.Time) time.Time {
	return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC)
}
