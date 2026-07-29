package muninn

import (
	"fmt"
	"time"
)

const (
	DefaultEnrichmentHistoryMonths  = 24
	MaxEnrichmentHistoryMonths      = 60
	DefaultEnrichmentHistoryLimit   = 5
	MaxEnrichmentHistoryLimit       = 50
	DefaultEnrichmentCandidateLimit = 10000
)

// EnrichmentOptions controls the optional secondary attribution context.
// Zero values use the documented defaults.
type EnrichmentOptions struct {
	HistoryMonths  int
	HistoryLimit   int
	CandidateLimit int
}

func (o EnrichmentOptions) normalized() EnrichmentOptions {
	if o.HistoryMonths == 0 {
		o.HistoryMonths = DefaultEnrichmentHistoryMonths
	}
	if o.HistoryLimit == 0 {
		o.HistoryLimit = DefaultEnrichmentHistoryLimit
	}
	if o.CandidateLimit == 0 {
		o.CandidateLimit = DefaultEnrichmentCandidateLimit
	}
	return o
}

// Validate checks enrichment bounds without performing I/O.
func (o EnrichmentOptions) Validate() error {
	switch {
	case o.HistoryMonths < 0:
		return &ValidationError{Field: "Enrichment.HistoryMonths", Problem: "must be non-negative"}
	case o.HistoryMonths > MaxEnrichmentHistoryMonths:
		return &ValidationError{
			Field:   "Enrichment.HistoryMonths",
			Problem: fmt.Sprintf("must not exceed %d", MaxEnrichmentHistoryMonths),
		}
	case o.HistoryLimit < 0:
		return &ValidationError{Field: "Enrichment.HistoryLimit", Problem: "must be non-negative"}
	case o.HistoryLimit > MaxEnrichmentHistoryLimit:
		return &ValidationError{
			Field:   "Enrichment.HistoryLimit",
			Problem: fmt.Sprintf("must not exceed %d", MaxEnrichmentHistoryLimit),
		}
	case o.CandidateLimit < 0:
		return &ValidationError{Field: "Enrichment.CandidateLimit", Problem: "must be non-negative"}
	default:
		return nil
	}
}

// EnrichmentResult is kept separate from the primary search guarantees.
type EnrichmentResult struct {
	Items    []TenderEnrichment
	Coverage EnrichmentCoverage
	Partial  bool
	Warnings []Warning
}

// EnrichmentCoverage describes both the requested history window and the
// interval for which the secondary source actually supplied resources.
type EnrichmentCoverage struct {
	RequestedFrom time.Time
	RequestedTo   time.Time
	AvailableFrom time.Time
	AvailableTo   time.Time
	FreshAt       time.Time
}

// TenderEnrichment contains attribution context for one primary Tender. Its
// TenderKey is the primary tender's DedupKey and Items preserve page order.
type TenderEnrichment struct {
	TenderKey      string
	ExactRelations []RelatedTender
	Candidates     []RelatedTender
	BuyerHistory   []Tender
	Conflicts      []EnrichmentConflict
}

// RelationType states why a BEAUAMP notice is related to the primary BOAMP
// notice. Composite candidates are explicitly not direct relations.
type RelationType string

const (
	RelationSameAwardNotice    RelationType = "same_award_notice"
	RelationReportedContract   RelationType = "reported_contract"
	RelationCompositeCandidate RelationType = "composite_candidate"
)

// RelationConfidence is intentionally categorical: only a shared native
// identifier can produce Exact or SourceReported confidence.
type RelationConfidence string

const (
	ConfidenceExact          RelationConfidence = "exact"
	ConfidenceSourceReported RelationConfidence = "source_reported"
	ConfidenceCandidate      RelationConfidence = "candidate"
)

// RelationEvidence is the auditable set of facts used for one relationship.
type RelationEvidence struct {
	BOAMPID              string
	BEAUAMPAttributionID string
	BEAUAMPContractID    string

	BuyerSIREN          string
	BuyerSIRENEstimated bool
	CPVRoots            []string
	ObjectSimilarity    float64
	PublicationGapDays  int
	TemporalConsistent  bool
}

// RelatedTender wraps a normalized BEAUAMP notice without merging it into the
// authoritative primary tender.
type RelatedTender struct {
	Tender     Tender
	Relation   RelationType
	Confidence RelationConfidence
	Evidence   RelationEvidence
}

// EnrichmentConflict reports contradictory lifecycle or identifier context.
type EnrichmentConflict struct {
	Code    string
	Message string

	BOAMPStatus   TenderStatus
	BEAUAMPStatus TenderStatus
	RelatedID     string
}
