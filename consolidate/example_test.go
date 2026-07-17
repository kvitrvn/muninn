package consolidate_test

import (
	"context"
	"fmt"

	"github.com/kvitrvn/muninn"
	"github.com/kvitrvn/muninn/beauamp"
	"github.com/kvitrvn/muninn/consolidate"
	"github.com/kvitrvn/muninn/decp"
)

// Pairing BEAUAMP (names, structured notices) with DECP (reference amounts)
// yields tenders that carry both the parties and the authoritative amount.
func ExampleConsolidator_Search() {
	c := consolidate.New(beauamp.New(), decp.New())

	tenders, err := c.Search(context.Background(), muninn.Query{
		Keywords:  []string{"gestion électronique de documents"},
		ObjetOnly: true,
	})
	if err != nil {
		panic(err)
	}

	for _, t := range tenders {
		fmt.Printf("[%s] %s — %s → %s (%.0f €)\n",
			t.Source, t.Objet, t.Buyer.Nom, t.Supplier.Nom, t.MontantEstime)
	}
}
