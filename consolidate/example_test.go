package consolidate_test

import (
	"fmt"
	"time"

	"github.com/kvitrvn/muninn"
	"github.com/kvitrvn/muninn/consolidate"
)

func ExampleMerge() {
	date := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	items := []muninn.Tender{
		{
			Sources: []muninn.SourceReference{{Provider: "beauamp", ID: "b-1"}},
			Objet:   "Solution GED", CPVCodes: []string{"72000000"},
			Buyer:           muninn.Buyer{SIREN: "200000001", Nom: "Ville"},
			Supplier:        muninn.Buyer{SIREN: "428692701", Nom: "Titulaire"},
			DatePublication: date,
		},
		{
			Sources: []muninn.SourceReference{{Provider: "decp", ID: "d-1"}},
			Objet:   "Solution GED", CPVCodes: []string{"72000000-9"},
			Buyer:           muninn.Buyer{SIRET: "20000000100015"},
			Supplier:        muninn.Buyer{SIRET: "42869270100010"},
			DatePublication: date.Add(24 * time.Hour),
			MontantEstime:   120000,
		},
	}

	merged := consolidate.Merge(items)
	fmt.Printf("%d résultat, %d sources, %.0f €\n",
		len(merged), len(merged[0].Sources), merged[0].MontantEstime)
	// Output:
	// 1 résultat, 2 sources, 120000 €
}
