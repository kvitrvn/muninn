package search

import (
	"testing"

	"github.com/kvitrvn/muninn"
)

func TestAdvancedFilter_Apply(t *testing.T) {
	tenders := []muninn.Tender{
		{
			Sources:       []muninn.SourceReference{{Provider: "test", ID: "1"}},
			CPVCodes:      []string{"72000000"},
			MontantEstime: 50000,
			Buyer:         muninn.Buyer{SIREN: "111111111"},
			Suppliers:     []muninn.Buyer{{SIRET: "11111111100011"}},
		},
		{
			Sources:       []muninn.SourceReference{{Provider: "test", ID: "2"}},
			CPVCodes:      []string{"30190000"},
			MontantEstime: 500000,
			Buyer:         muninn.Buyer{SIREN: "222222222"},
			Suppliers: []muninn.Buyer{
				{SIRET: "11111111100011"},
				{SIRET: "49884169100039"},
			},
		},
		{
			Sources:       []muninn.SourceReference{{Provider: "test", ID: "3"}},
			CPVCodes:      []string{"72500000"},
			MontantEstime: 5000000,
			Buyer:         muninn.Buyer{SIREN: "111111111"},
			Suppliers:     []muninn.Buyer{{SIRET: "49884169100039"}},
		},
		{
			Sources:       []muninn.SourceReference{{Provider: "test", ID: "4"}},
			CPVCodes:      []string{"72000000"},
			MontantEstime: 50000,
			Buyer:         muninn.Buyer{SIRET: "99999999999999"}, // SIREN9 == "999999999"
		},
	}
	tests := []struct {
		name string
		f    AdvancedFilter
		want []string
	}{
		{"empty filter is no-op", AdvancedFilter{}, []string{"1", "2", "3", "4"}},
		{"CPV prefix match", AdvancedFilter{CPVCodes: []string{"72"}}, []string{"1", "3", "4"}},
		{"amount min", AdvancedFilter{MontantMin: 100000}, []string{"2", "3"}},
		{"amount range", AdvancedFilter{MontantMin: 100000, MontantMax: 1000000}, []string{"2"}},
		{"buyer SIREN exact", AdvancedFilter{BuyerSIREN: "111111111"}, []string{"1", "3"}},
		{"buyer SIREN derived from SIRET", AdvancedFilter{BuyerSIREN: "999999999"}, []string{"4"}},
		{"supplier SIREN derived from SIRET", AdvancedFilter{SupplierSIREN: "498841691"}, []string{"2", "3"}},
		{"supplier SIRET exact on any member", AdvancedFilter{SupplierSIRET: " 49884169100039 "}, []string{"2", "3"}},
		{"combined", AdvancedFilter{CPVCodes: []string{"72"}, MontantMin: 100000, BuyerSIREN: "111111111"}, []string{"3"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.f.Apply(tenders)
			ids := make([]string, 0, len(got))
			for _, x := range got {
				ids = append(ids, x.Sources[0].ID)
			}
			if !sameSet(ids, tt.want) {
				t.Errorf("ids = %v, want %v", ids, tt.want)
			}
		})
	}
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := map[string]bool{}
	for _, s := range a {
		m[s] = true
	}
	for _, s := range b {
		if !m[s] {
			return false
		}
	}
	return true
}
