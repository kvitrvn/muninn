package muninn

import (
	"testing"
	"time"
)

func TestFilterTenders_SupplierSIRETMatchesAnySupplier(t *testing.T) {
	const wanted = "49884169100039"
	tenders := []Tender{
		{
			Sources:   []SourceReference{{Provider: "test", ID: "first"}},
			Suppliers: []Buyer{{SIRET: wanted}, {SIRET: "11111111100011"}},
		},
		{
			Sources:   []SourceReference{{Provider: "test", ID: "second"}},
			Suppliers: []Buyer{{SIRET: "11111111100011"}, {SIRET: wanted}},
		},
		{
			Sources: []SourceReference{{Provider: "test", ID: "third"}},
			Suppliers: []Buyer{
				{SIRET: "11111111100011"},
				{SIRET: "22222222200022"},
				{SIRET: wanted},
			},
		},
		{
			Sources:   []SourceReference{{Provider: "test", ID: "different"}},
			Suppliers: []Buyer{{SIRET: "11111111100011"}},
		},
		{
			Sources: []SourceReference{{Provider: "test", ID: "missing"}},
		},
	}

	got := FilterTenders(tenders, Query{SupplierSIRET: " " + wanted + " "}, time.Time{})
	if len(got) != 3 {
		t.Fatalf("FilterTenders() returned %d tenders, want 3", len(got))
	}
	for index, wantID := range []string{"first", "second", "third"} {
		if got[index].Sources[0].ID != wantID {
			t.Errorf("result %d ID = %q, want %q", index, got[index].Sources[0].ID, wantID)
		}
	}
}
