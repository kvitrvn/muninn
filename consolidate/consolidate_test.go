package consolidate

import (
	"context"
	"errors"
	"testing"

	"github.com/kvitrvn/muninn"
)

func TestMerge_EnrichesAcrossSources(t *testing.T) {
	// Same buyer SIREN + CPV root: a BEAUAMP notice (names, indicative amount)
	// and its DECP award (SIRET, reference amount) must collapse into one.
	beau := muninn.Tender{
		Source:        "beauamp",
		SourceID:      "26-1",
		Objet:         "Solution GED",
		Titre:         "Solution GED",
		CPVCodes:      []string{"72000000"},
		Buyer:         muninn.Buyer{Nom: "Ville X", SIREN: "200000001"},
		Supplier:      muninn.Buyer{Nom: "ENNOV", SIREN: "428692701"},
		MontantEstime: 800000, // indicative
	}
	award := muninn.Tender{
		Source:        "decp",
		SourceID:      "d-1",
		CPVCodes:      []string{"72000000-9"}, // same root
		Buyer:         muninn.Buyer{SIRET: "20000000100015"},
		Supplier:      muninn.Buyer{SIRET: "42869270100010"},
		MontantEstime: 754853, // authoritative
		AvisType:      muninn.AvisAttribution,
	}

	got := Merge([]muninn.Tender{beau, award})
	if len(got) != 1 {
		t.Fatalf("got %d tenders, want 1 (merged): %+v", len(got), got)
	}
	m := got[0]
	if m.MontantEstime != 754853 {
		t.Errorf("MontantEstime = %v, want DECP amount 754853", m.MontantEstime)
	}
	if m.Buyer.Nom != "Ville X" || m.Buyer.SIRET != "20000000100015" {
		t.Errorf("Buyer not merged: %+v", m.Buyer)
	}
	if m.Supplier.Nom != "ENNOV" || m.Supplier.SIRET != "42869270100010" {
		t.Errorf("Supplier not merged: %+v", m.Supplier)
	}
	if m.Source != "beauamp+decp" {
		t.Errorf("Source = %q, want beauamp+decp", m.Source)
	}
	if m.AvisType != muninn.AvisAttribution {
		t.Errorf("AvisType = %v, want AvisAttribution", m.AvisType)
	}
}

func TestMerge_DECPAmountWinsRegardlessOfOrder(t *testing.T) {
	award := muninn.Tender{Source: "decp", CPVCodes: []string{"72"}, Buyer: muninn.Buyer{SIREN: "100000009"}, MontantEstime: 500}
	beau := muninn.Tender{Source: "beauamp", CPVCodes: []string{"72"}, Buyer: muninn.Buyer{SIREN: "100000009"}, MontantEstime: 999}

	for _, order := range [][]muninn.Tender{{beau, award}, {award, beau}} {
		got := Merge(order)
		if len(got) != 1 || got[0].MontantEstime != 500 {
			t.Errorf("order %v → amount %v, want 500", sources(order), got[0].MontantEstime)
		}
	}
}

func sources(ts []muninn.Tender) []string {
	var s []string
	for _, t := range ts {
		s = append(s, t.Source)
	}
	return s
}

// stubProvider returns fixed results for testing the Consolidator wiring.
type stubProvider struct {
	name string
	res  []muninn.Tender
	err  error
}

func (s stubProvider) Name() string { return s.name }
func (s stubProvider) Search(context.Context, muninn.Query) ([]muninn.Tender, error) {
	return s.res, s.err
}

func TestConsolidator_Search(t *testing.T) {
	beau := stubProvider{name: "beauamp", res: []muninn.Tender{
		{Source: "beauamp", CPVCodes: []string{"72"}, Buyer: muninn.Buyer{SIREN: "100000009", Nom: "Ville"}, MontantEstime: 999},
	}}
	decp := stubProvider{name: "decp", res: []muninn.Tender{
		{Source: "decp", CPVCodes: []string{"72"}, Buyer: muninn.Buyer{SIREN: "100000009"}, MontantEstime: 500},
	}}

	got, err := New(beau, decp).Search(context.Background(), muninn.Query{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 1 || got[0].MontantEstime != 500 || got[0].Buyer.Nom != "Ville" {
		t.Fatalf("consolidated = %+v", got)
	}
}

func TestConsolidator_TruncationAggregated(t *testing.T) {
	p := stubProvider{name: "beauamp",
		res: []muninn.Tender{{Source: "beauamp", SourceID: "a"}},
		err: &muninn.ErrTruncated{Retrieved: 1, Total: 100},
	}
	got, err := New(p).Search(context.Background(), muninn.Query{})
	var tr *muninn.ErrTruncated
	if !errors.As(err, &tr) {
		t.Fatalf("want *muninn.ErrTruncated, got %v", err)
	}
	if tr.Total != 100 || len(got) != 1 {
		t.Errorf("got %d records, total %d", len(got), tr.Total)
	}
}
