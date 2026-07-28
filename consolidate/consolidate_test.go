package consolidate

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/kvitrvn/muninn"
)

func source(provider, id string) []muninn.SourceReference {
	return []muninn.SourceReference{{Provider: provider, ID: id}}
}

func TestMerge_EnrichesOnlyStrongCrossSourceMatch(t *testing.T) {
	published := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	beau := muninn.Tender{
		Sources:         source("beauamp", "26-1"),
		Objet:           "Solution GED",
		CPVCodes:        []string{"72000000"},
		Buyer:           muninn.Buyer{Nom: "Ville X", SIREN: "200000001"},
		Suppliers:       []muninn.Buyer{{Nom: "ENNOV", SIREN: "428692701"}},
		MontantEstime:   800000,
		DatePublication: published,
	}
	award := muninn.Tender{
		Sources:  source("decp", "d-1"),
		Objet:    "solution GED",
		CPVCodes: []string{"72000000-9"},
		Buyer:    muninn.Buyer{SIRET: "20000000100015"},
		Suppliers: []muninn.Buyer{
			{SIRET: "11111111100011"},
			{SIRET: "42869270100010"},
		},
		MontantEstime:   754853,
		AvisType:        muninn.AvisAttribution,
		DatePublication: published.Add(24 * time.Hour),
	}

	got := Merge([]muninn.Tender{beau, award})
	if len(got) != 1 {
		t.Fatalf("got %d tenders, want one strong match", len(got))
	}
	merged := got[0]
	if merged.MontantEstime != 754853 {
		t.Errorf("amount = %v, want DECP amount", merged.MontantEstime)
	}
	if merged.Buyer.Nom != "Ville X" || merged.Buyer.SIRET != "20000000100015" {
		t.Errorf("buyer = %+v", merged.Buyer)
	}
	if len(merged.Sources) != 2 {
		t.Errorf("sources = %+v", merged.Sources)
	}
	if len(merged.Suppliers) != 2 {
		t.Fatalf("suppliers = %+v, want two distinct SIRETs", merged.Suppliers)
	}
	if merged.Suppliers[0].SIRET != "11111111100011" {
		t.Errorf("first supplier = %+v", merged.Suppliers[0])
	}
	if merged.Suppliers[1].SIRET != "42869270100010" || merged.Suppliers[1].Nom != "ENNOV" {
		t.Errorf("second supplier = %+v, want enriched ENNOV SIRET", merged.Suppliers[1])
	}
	reversed := Merge([]muninn.Tender{award, beau})
	if len(reversed) != 1 || !reflect.DeepEqual(reversed[0].Suppliers, merged.Suppliers) {
		t.Errorf("reversed suppliers = %+v, want %+v", reversed, merged.Suppliers)
	}
}

func TestMerge_DoesNotCollapseSameBuyerAndCPVWithDifferentObjects(t *testing.T) {
	date := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	a := muninn.Tender{
		Sources: source("beauamp", "a"), Objet: "GED des archives",
		Buyer: muninn.Buyer{SIREN: "200000001"}, CPVCodes: []string{"72000000"}, DatePublication: date,
	}
	b := muninn.Tender{
		Sources: source("decp", "b"), Objet: "Maintenance du réseau",
		Buyer: muninn.Buyer{SIREN: "200000001"}, CPVCodes: []string{"72000000"}, DatePublication: date,
	}
	if got := Merge([]muninn.Tender{a, b}); len(got) != 2 {
		t.Fatalf("got %d records, want 2", len(got))
	}
}

func TestMerge_DoesNotGuessWithoutSupplierOrSharedNoticeID(t *testing.T) {
	date := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	a := muninn.Tender{
		Sources: source("beauamp", "a"), Objet: "Solution GED",
		Buyer: muninn.Buyer{SIREN: "200000001"}, CPVCodes: []string{"72000000"}, DatePublication: date,
	}
	b := muninn.Tender{
		Sources: source("decp", "b"), Objet: "Solution GED",
		Buyer: muninn.Buyer{SIREN: "200000001"}, CPVCodes: []string{"72000000"}, DatePublication: date,
	}
	if got := Merge([]muninn.Tender{a, b}); len(got) != 2 {
		t.Fatalf("got %d records, want 2 without strong cross-source evidence", len(got))
	}
}

type stubProvider struct {
	name string
	caps muninn.Capabilities
	res  muninn.ProviderResult
}

func (s stubProvider) Name() string                      { return s.name }
func (s stubProvider) Capabilities() muninn.Capabilities { return s.caps }
func (s stubProvider) Search(context.Context, muninn.Query) (muninn.ProviderResult, error) {
	return s.res, nil
}

func TestConsolidator_Search(t *testing.T) {
	caps := muninn.Capabilities{muninn.FilterTitleKeywords: muninn.Exact}
	a := stubProvider{name: "a", caps: caps, res: muninn.ProviderResult{Items: []muninn.Tender{
		{Sources: source("a", "1"), Objet: "GED"},
	}, TotalExact: true}}
	b := stubProvider{name: "b", caps: caps, res: muninn.ProviderResult{Items: []muninn.Tender{
		{Sources: source("b", "2"), Objet: "Archivage"},
	}, TotalExact: true}}

	got, err := New(a, b).Search(context.Background(), muninn.Query{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got.Items) != 2 || !got.TotalExact {
		t.Fatalf("result = %+v", got)
	}
}

func TestConsolidator_CapabilitiesIncludeSupplierSIRET(t *testing.T) {
	exact := stubProvider{
		name: "decp",
		caps: muninn.Capabilities{muninn.FilterSupplierSIRET: muninn.Exact},
	}
	if got := New(exact).Capabilities().Support(muninn.FilterSupplierSIRET); got != muninn.Exact {
		t.Fatalf("exact consolidator support = %v", got)
	}

	unsupported := stubProvider{name: "boamp"}
	if got := New(exact, unsupported).Capabilities().Support(muninn.FilterSupplierSIRET); got != muninn.Unsupported {
		t.Fatalf("mixed consolidator support = %v", got)
	}
}
