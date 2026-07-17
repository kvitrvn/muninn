package beauamp_test

import (
	"context"
	"fmt"

	"github.com/kvitrvn/muninn"
	"github.com/kvitrvn/muninn/beauamp"
)

// By default the client resolves the most recent monthly resource from the
// dataset catalog; WithResources pins specific months instead.
func ExampleClient_Search() {
	c := beauamp.New()

	notices, err := c.Search(context.Background(), muninn.Query{
		Keywords: []string{"GED", "gestion électronique de documents"},
	})
	if err != nil {
		panic(err)
	}

	for _, n := range notices {
		fmt.Printf("%s — %s → %s (%.0f €)\n",
			n.Objet, n.Buyer.Nom, n.Supplier.Nom, n.MontantEstime)
	}
}
