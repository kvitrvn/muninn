package search_test

import (
	"fmt"

	"github.com/kvitrvn/muninn"
	"github.com/kvitrvn/muninn/search"
)

// Filter refines an already-fetched list of Tenders client-side, by keywords
// (OR) and/or CPV prefixes. Useful to tighten a provider result or combine
// criteria a source cannot filter natively.
func ExampleFilter() {
	tenders := []muninn.Tender{
		{Titre: "Solution GED open source", CPVCodes: []string{"48000000"}},
		{Titre: "Fournitures de bureau", CPVCodes: []string{"30190000"}},
	}

	kept := search.Filter{
		Keywords: []string{"open source", "logiciel libre"},
		CPVCodes: []string{"48"}, // software
	}.Apply(tenders)

	fmt.Println(len(kept))
	// Output: 1
}

func ExampleAdvancedFilter_supplierSIRET() {
	tenders := []muninn.Tender{
		{
			Objet: "Marché groupé",
			Suppliers: []muninn.Buyer{
				{SIRET: "11111111100011"},
				{SIRET: "49884169100039"},
			},
		},
		{
			Objet:     "Autre marché",
			Suppliers: []muninn.Buyer{{SIRET: "22222222200022"}},
		},
	}

	kept := search.AdvancedFilter{
		SupplierSIRET: "49884169100039",
	}.Apply(tenders)

	fmt.Println(kept[0].Objet)
	// Output: Marché groupé
}

func ExampleAdvancedFilter_supplierSIREN() {
	tenders := []muninn.Tender{
		{
			Objet:     "Ancien établissement",
			Suppliers: []muninn.Buyer{{SIRET: "12345678900010"}},
		},
		{
			Objet:     "Nouvel établissement",
			Suppliers: []muninn.Buyer{{SIRET: "12345678900028"}},
		},
		{
			Objet:     "Autre entreprise",
			Suppliers: []muninn.Buyer{{SIREN: "987654321"}},
		},
	}

	kept := search.AdvancedFilter{
		SupplierSIREN: "123456789",
	}.Apply(tenders)

	fmt.Println(len(kept))
	// Output: 2
}
