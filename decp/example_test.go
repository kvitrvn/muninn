package decp_test

import (
	"context"
	"fmt"

	"github.com/kvitrvn/muninn"
	"github.com/kvitrvn/muninn/decp"
)

// Search returns awarded contracts, each carrying the winning contractor
// (Supplier) and the binding amount (MontantEstime).
func ExampleClient_Search() {
	c := decp.New()

	awards, err := c.Search(context.Background(), muninn.Query{
		Keywords:  []string{"gestion électronique de documents"},
		ObjetOnly: true,
		Limit:     100,
	})
	if err != nil {
		panic(err)
	}

	for _, a := range awards {
		fmt.Printf("%s — %.0f € — titulaire %s\n", a.Objet, a.MontantEstime, a.Supplier.SIRET)
	}
}
