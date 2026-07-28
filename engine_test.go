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
