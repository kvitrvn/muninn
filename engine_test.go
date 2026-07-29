package muninn

import (
	"context"
	"errors"
	"testing"
	"time"
)

type engineStubProvider struct {
	name string
	caps Capabilities
	res  ProviderResult
	err  error
	seen chan Query
}

type engineStubEnricher struct {
	engineStubProvider
	enrichment EnrichmentResult
	enrichErr  error
	enrichSeen chan []Tender
}

func (p engineStubEnricher) Enrich(
	_ context.Context,
	items []Tender,
	_ EnrichmentOptions,
	_ time.Time,
) (EnrichmentResult, error) {
	if p.enrichSeen != nil {
		p.enrichSeen <- items
	}
	return p.enrichment, p.enrichErr
}

func (p engineStubProvider) Name() string               { return p.name }
func (p engineStubProvider) Capabilities() Capabilities { return p.caps }
func (p engineStubProvider) Search(_ context.Context, q Query) (ProviderResult, error) {
	if p.seen != nil {
		p.seen <- q
	}
	return p.res, p.err
}

func testTender(provider, id string, published, deadline time.Time) Tender {
	return Tender{
		Sources:           []SourceReference{{Provider: provider, ID: id}},
		Objet:             "Solution GED",
		AvisType:          AvisAppelConcurrence,
		DatePublication:   published,
		DateLimiteReponse: deadline,
	}
}

func TestEngine_SearchFiltersOpenAndSortsByDeadline(t *testing.T) {
	at := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	provider := engineStubProvider{
		name: "boamp",
		caps: Capabilities{
			FilterTitleKeywords: Exact,
			FilterStatusOpen:    Exact,
		},
		res: ProviderResult{
			Items: []Tender{
				testTender("boamp", "late", at.Add(-3*time.Hour), at.Add(72*time.Hour)),
				testTender("boamp", "closed", at.Add(-time.Hour), at.Add(-24*time.Hour)),
				testTender("boamp", "soon", at.Add(-2*time.Hour), at.Add(24*time.Hour)),
			},
			Total:      3,
			TotalExact: true,
		},
	}

	result, err := NewEngine(provider).Search(context.Background(), Query{
		Keywords:  []string{"GED"},
		ObjetOnly: true,
		Statuses:  []TenderStatus{StatusOpen},
		OpenAt:    at,
		Sort:      Sort{Field: SortByDeadline},
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(result.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(result.Items))
	}
	if got := result.Items[0].Sources[0].ID; got != "soon" {
		t.Fatalf("first item = %q, want soon", got)
	}
	if result.Partial || !result.TotalExact || result.Total != 2 {
		t.Fatalf("metadata = %+v", result)
	}
}

func TestEngine_OpenEnrichmentIsSecondaryAndDoesNotChangePrimaryGuarantees(t *testing.T) {
	at := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	primary := testTender("boamp", "26-1", at.Add(-time.Hour), at.Add(24*time.Hour))
	enrichSeen := make(chan []Tender, 1)
	enricherSearchSeen := make(chan Query, 1)
	enricher := engineStubEnricher{
		engineStubProvider: engineStubProvider{
			name: "beauamp",
			seen: enricherSearchSeen,
		},
		enrichment: EnrichmentResult{
			Items: []TenderEnrichment{{
				TenderKey: primary.DedupKey(),
				ExactRelations: []RelatedTender{{
					Confidence: ConfidenceExact,
				}},
			}},
		},
		enrichSeen: enrichSeen,
	}
	engine := NewEngine(
		engineStubProvider{
			name: "boamp",
			caps: Capabilities{FilterStatusOpen: Exact},
			res:  ProviderResult{Items: []Tender{primary}, TotalExact: true},
		},
		enricher,
	)

	result, err := engine.Search(context.Background(), Query{
		Statuses:   []TenderStatus{StatusOpen},
		OpenAt:     at,
		Enrichment: &EnrichmentOptions{},
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if result.Partial || !result.TotalExact || len(result.Warnings) != 0 {
		t.Fatalf("primary metadata changed by enrichment: %+v", result)
	}
	if len(result.Items) != 1 || result.Items[0].DedupKey() != primary.DedupKey() {
		t.Fatalf("primary items = %+v", result.Items)
	}
	if result.Enrichment == nil ||
		len(result.Enrichment.Items) != 1 ||
		len(result.Enrichment.Items[0].ExactRelations) != 1 {
		t.Fatalf("enrichment = %+v", result.Enrichment)
	}
	select {
	case got := <-enrichSeen:
		if len(got) != 1 || got[0].DedupKey() != primary.DedupKey() {
			t.Fatalf("enriched page = %+v", got)
		}
	default:
		t.Fatal("enricher was not called")
	}
	select {
	case <-enricherSearchSeen:
		t.Fatal("enricher was queried as a primary provider")
	default:
	}
}

func TestEngine_EnrichmentFailureStaysOutOfPrimaryPartialState(t *testing.T) {
	failure := errors.New("BEAUAMP unavailable")
	at := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	primary := testTender("boamp", "26-1", at.Add(-time.Hour), at.Add(24*time.Hour))
	engine := NewEngine(
		engineStubProvider{
			name: "boamp",
			caps: Capabilities{FilterStatusOpen: Exact},
			res:  ProviderResult{Items: []Tender{primary}, TotalExact: true},
		},
		engineStubEnricher{
			engineStubProvider: engineStubProvider{name: "beauamp"},
			enrichErr:          failure,
		},
	)

	result, err := engine.Search(context.Background(), Query{
		Statuses:   []TenderStatus{StatusOpen},
		OpenAt:     at,
		Enrichment: &EnrichmentOptions{},
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if result.Partial || !result.TotalExact || len(result.Warnings) != 0 {
		t.Fatalf("primary metadata changed by enrichment failure: %+v", result)
	}
	if result.Enrichment == nil || !result.Enrichment.Partial ||
		len(result.Enrichment.Warnings) != 1 ||
		!errors.Is(result.Enrichment.Warnings[0].Err, failure) {
		t.Fatalf("enrichment = %+v", result.Enrichment)
	}
}

func TestEngine_CursorFingerprintIncludesNormalizedEnrichmentOptions(t *testing.T) {
	at := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	engine := NewEngine(
		engineStubProvider{
			name: "boamp",
			caps: Capabilities{FilterStatusOpen: Exact},
			res: ProviderResult{Items: []Tender{
				testTender("boamp", "1", at.Add(-time.Hour), at.Add(24*time.Hour)),
				testTender("boamp", "2", at.Add(-2*time.Hour), at.Add(48*time.Hour)),
			}},
		},
		engineStubEnricher{engineStubProvider: engineStubProvider{name: "beauamp"}},
	)
	first, err := engine.Search(context.Background(), Query{
		Statuses:   []TenderStatus{StatusOpen},
		OpenAt:     at,
		PageSize:   1,
		Enrichment: &EnrichmentOptions{},
	})
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if first.NextCursor == "" {
		t.Fatal("first page has no cursor")
	}

	_, err = engine.Search(context.Background(), Query{
		Statuses: []TenderStatus{StatusOpen},
		OpenAt:   at,
		PageSize: 1,
		Cursor:   first.NextCursor,
		Enrichment: &EnrichmentOptions{
			HistoryMonths: 12,
		},
	})
	var validation *ValidationError
	if !errors.As(err, &validation) || validation.Field != "Cursor" {
		t.Fatalf("cursor mismatch error = %v", err)
	}
}

func TestEngine_SearchCombinesSupportedStatusSubsets(t *testing.T) {
	at := time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC)
	openSeen := make(chan Query, 1)
	awardSeen := make(chan Query, 1)
	openProvider := engineStubProvider{
		name: "notices",
		caps: Capabilities{FilterStatusOpen: Exact},
		res: ProviderResult{Items: []Tender{
			testTender("notices", "open", at, at.Add(24*time.Hour)),
		}, TotalExact: true},
		seen: openSeen,
	}
	awardProvider := engineStubProvider{
		name: "awards",
		caps: Capabilities{FilterStatusAwarded: Exact},
		res: ProviderResult{Items: []Tender{{
			Sources:  []SourceReference{{Provider: "awards", ID: "award"}},
			Objet:    "Marché attribué",
			AvisType: AvisAttribution,
		}}, TotalExact: true},
		seen: awardSeen,
	}

	result, err := NewEngine(openProvider, awardProvider).Search(context.Background(), Query{
		Statuses: []TenderStatus{StatusOpen, StatusAwarded},
		OpenAt:   at,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(result.Items) != 2 {
		t.Fatalf("items = %+v", result.Items)
	}
	if got := (<-openSeen).Statuses; len(got) != 1 || got[0] != StatusOpen {
		t.Errorf("open provider statuses = %v", got)
	}
	if got := (<-awardSeen).Statuses; len(got) != 1 || got[0] != StatusAwarded {
		t.Errorf("award provider statuses = %v", got)
	}
	if !result.Partial || len(result.Warnings) != 2 {
		t.Fatalf("warnings = %+v", result.Warnings)
	}
}

func TestEngine_SearchReturnsPartialResultsOnProviderFailure(t *testing.T) {
	failure := errors.New("upstream unavailable")
	good := engineStubProvider{
		name: "good",
		res: ProviderResult{Items: []Tender{{
			Sources: []SourceReference{{Provider: "good", ID: "1"}},
		}}, TotalExact: true},
	}
	bad := engineStubProvider{name: "bad", err: failure}

	result, err := NewEngine(good, bad).Search(context.Background(), Query{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(result.Items) != 1 || !result.Partial || result.TotalExact {
		t.Fatalf("result = %+v", result)
	}
	if len(result.Warnings) != 1 || !errors.Is(result.Warnings[0].Err, failure) {
		t.Fatalf("warnings = %+v", result.Warnings)
	}
}

func TestEngine_SearchReturnsErrorWhenAllProvidersFail(t *testing.T) {
	failure := errors.New("boom")
	result, err := NewEngine(
		engineStubProvider{name: "a", err: failure},
		engineStubProvider{name: "b", err: failure},
	).Search(context.Background(), Query{})
	if err == nil || !errors.Is(err, failure) {
		t.Fatalf("error = %v", err)
	}
	if !result.Partial || len(result.Warnings) != 2 {
		t.Fatalf("result = %+v", result)
	}
}

func TestEngine_SearchPaginatesWithOpaqueCursor(t *testing.T) {
	base := time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC)
	provider := engineStubProvider{name: "p", res: ProviderResult{
		Items: []Tender{
			testTender("p", "1", base.Add(3*time.Hour), time.Time{}),
			testTender("p", "2", base.Add(2*time.Hour), time.Time{}),
			testTender("p", "3", base.Add(time.Hour), time.Time{}),
		},
		TotalExact: true,
	}}
	engine := NewEngine(provider)

	first, err := engine.Search(context.Background(), Query{PageSize: 2})
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if len(first.Items) != 2 || first.NextCursor == "" {
		t.Fatalf("first page = %+v", first)
	}

	second, err := engine.Search(context.Background(), Query{PageSize: 2, Cursor: first.NextCursor})
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	if len(second.Items) != 1 || second.Items[0].Sources[0].ID != "3" || second.NextCursor != "" {
		t.Fatalf("second page = %+v", second)
	}

	_, err = engine.Search(context.Background(), Query{
		Keywords: []string{"different"},
		PageSize: 2,
		Cursor:   first.NextCursor,
	})
	var validation *ValidationError
	if !errors.As(err, &validation) || validation.Field != "Cursor" {
		t.Fatalf("cursor mismatch error = %v", err)
	}
}

func TestEngine_SearchSkipsUnsupportedProvider(t *testing.T) {
	unsupported := engineStubProvider{name: "boamp"}
	supported := engineStubProvider{
		name: "decp",
		caps: Capabilities{FilterAmount: Exact},
		res: ProviderResult{Items: []Tender{{
			Sources:       []SourceReference{{Provider: "decp", ID: "1"}},
			MontantEstime: 500,
		}}, TotalExact: true},
	}
	result, err := NewEngine(unsupported, supported).Search(context.Background(), Query{MontantMin: 100})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(result.Items) != 1 || !result.Partial {
		t.Fatalf("result = %+v", result)
	}
	if len(result.Warnings) != 1 || result.Warnings[0].Code != WarningUnsupportedFilter {
		t.Fatalf("warnings = %+v", result.Warnings)
	}
}

func TestEngine_SearchReportsApproximationAndTruncation(t *testing.T) {
	provider := engineStubProvider{
		name: "approx",
		caps: Capabilities{FilterAmount: Approximate},
		res: ProviderResult{
			Items: []Tender{{
				Sources:       []SourceReference{{Provider: "approx", ID: "1"}},
				MontantEstime: 500,
			}},
			Total:      100,
			TotalExact: false,
			Truncated:  true,
		},
	}
	result, err := NewEngine(provider).Search(context.Background(), Query{MontantMin: 100})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if !result.Partial || result.TotalExact || len(result.Warnings) != 2 {
		t.Fatalf("result = %+v", result)
	}
	if result.Warnings[0].Code != WarningApproximateFilter || result.Warnings[1].Code != WarningTruncated {
		t.Fatalf("warnings = %+v", result.Warnings)
	}
}

func TestEngine_SearchFailsWhenNoProviderSupportsQuery(t *testing.T) {
	result, err := NewEngine(engineStubProvider{name: "unsupported"}).
		Search(context.Background(), Query{MontantMin: 100})
	if !errors.Is(err, ErrNoCapableProvider) {
		t.Fatalf("error = %v", err)
	}
	if !result.Partial || len(result.Warnings) != 1 {
		t.Fatalf("result = %+v", result)
	}
}

func TestEngine_SearchRoutesSupplierSIRETToCapableProvider(t *testing.T) {
	const wanted = "49884169100039"
	unsupportedSeen := make(chan Query, 1)
	supportedSeen := make(chan Query, 1)
	unsupported := engineStubProvider{
		name: "boamp",
		seen: unsupportedSeen,
	}
	supported := engineStubProvider{
		name: "decp",
		caps: Capabilities{FilterSupplierSIRET: Exact},
		res: ProviderResult{
			Items: []Tender{{
				Sources: []SourceReference{{Provider: "decp", ID: "1"}},
				Suppliers: []Buyer{
					{SIRET: "11111111100011"},
					{SIRET: wanted},
				},
			}},
			TotalExact: true,
		},
		seen: supportedSeen,
	}

	result, err := NewEngine(unsupported, supported).Search(
		context.Background(),
		Query{SupplierSIRET: wanted},
	)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(result.Items) != 1 || !result.Partial {
		t.Fatalf("result = %+v", result)
	}
	if len(result.Warnings) != 1 ||
		result.Warnings[0].Provider != "boamp" ||
		result.Warnings[0].Code != WarningUnsupportedFilter {
		t.Fatalf("warnings = %+v", result.Warnings)
	}
	select {
	case query := <-supportedSeen:
		if query.SupplierSIRET != wanted {
			t.Errorf("DECP query SupplierSIRET = %q", query.SupplierSIRET)
		}
	default:
		t.Fatal("capable provider was not queried")
	}
	select {
	case <-unsupportedSeen:
		t.Fatal("unsupported provider was queried")
	default:
	}
}

func TestEngine_SearchRoutesSupplierSIRENToExactAndApproximateProviders(t *testing.T) {
	const wanted = "123456789"
	boampSeen := make(chan Query, 1)
	decpSeen := make(chan Query, 1)
	unsupportedSeen := make(chan Query, 1)

	boamp := engineStubProvider{
		name: "boamp",
		caps: Capabilities{FilterSupplierSIREN: Approximate},
		res: ProviderResult{Items: []Tender{{
			Sources:   []SourceReference{{Provider: "boamp", ID: "legacy"}},
			Suppliers: []Buyer{{Nom: "ACME Numérique", SIREN: wanted}},
		}}},
		seen: boampSeen,
	}
	decp := engineStubProvider{
		name: "decp",
		caps: Capabilities{FilterSupplierSIREN: Exact},
		res: ProviderResult{Items: []Tender{{
			Sources:   []SourceReference{{Provider: "decp", ID: "current"}},
			Suppliers: []Buyer{{SIRET: wanted + "00043"}},
		}}},
		seen: decpSeen,
	}
	unsupported := engineStubProvider{name: "other", seen: unsupportedSeen}

	result, err := NewEngine(boamp, decp, unsupported).Search(
		context.Background(),
		Query{SupplierSIREN: wanted},
	)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(result.Items) != 2 || !result.Partial {
		t.Fatalf("result = %+v", result)
	}
	if len(result.Warnings) != 2 {
		t.Fatalf("warnings = %+v, want approximate and unsupported warnings", result.Warnings)
	}
	for name, seen := range map[string]<-chan Query{"boamp": boampSeen, "decp": decpSeen} {
		select {
		case query := <-seen:
			if query.SupplierSIREN != wanted {
				t.Errorf("%s query SupplierSIREN = %q", name, query.SupplierSIREN)
			}
		default:
			t.Errorf("%s was not queried", name)
		}
	}
	select {
	case <-unsupportedSeen:
		t.Fatal("unsupported provider was queried")
	default:
	}
}

func TestEngine_CursorFingerprintIncludesSupplierSIRET(t *testing.T) {
	const firstSIRET = "49884169100039"
	provider := engineStubProvider{
		name: "decp",
		caps: Capabilities{FilterSupplierSIRET: Exact},
		res: ProviderResult{
			Items: []Tender{
				{
					Sources:   []SourceReference{{Provider: "decp", ID: "1"}},
					Suppliers: []Buyer{{SIRET: firstSIRET}},
				},
				{
					Sources:   []SourceReference{{Provider: "decp", ID: "2"}},
					Suppliers: []Buyer{{SIRET: firstSIRET}},
				},
			},
			TotalExact: true,
		},
	}
	engine := NewEngine(provider)

	first, err := engine.Search(context.Background(), Query{
		SupplierSIRET: firstSIRET,
		PageSize:      1,
	})
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if first.NextCursor == "" {
		t.Fatal("first page has no cursor")
	}

	_, err = engine.Search(context.Background(), Query{
		SupplierSIRET: "55210055400013",
		PageSize:      1,
		Cursor:        first.NextCursor,
	})
	var validation *ValidationError
	if !errors.As(err, &validation) || validation.Field != "Cursor" {
		t.Fatalf("cursor mismatch error = %v", err)
	}
}

func TestEngine_CursorFingerprintIncludesSupplierSIREN(t *testing.T) {
	const firstSIREN = "123456789"
	provider := engineStubProvider{
		name: "decp",
		caps: Capabilities{FilterSupplierSIREN: Exact},
		res: ProviderResult{
			Items: []Tender{
				{
					Sources:   []SourceReference{{Provider: "decp", ID: "1"}},
					Suppliers: []Buyer{{SIREN: firstSIREN}},
				},
				{
					Sources:   []SourceReference{{Provider: "decp", ID: "2"}},
					Suppliers: []Buyer{{SIREN: firstSIREN}},
				},
			},
			TotalExact: true,
		},
	}
	engine := NewEngine(provider)

	first, err := engine.Search(context.Background(), Query{
		SupplierSIREN: firstSIREN,
		PageSize:      1,
	})
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if first.NextCursor == "" {
		t.Fatal("first page has no cursor")
	}

	_, err = engine.Search(context.Background(), Query{
		SupplierSIREN: "428692701",
		PageSize:      1,
		Cursor:        first.NextCursor,
	})
	var validation *ValidationError
	if !errors.As(err, &validation) || validation.Field != "Cursor" {
		t.Fatalf("cursor mismatch error = %v", err)
	}
}
