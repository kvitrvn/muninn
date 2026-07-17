package boamp_test

import (
	"context"
	"errors"
	"fmt"

	"github.com/kvitrvn/muninn"
	"github.com/kvitrvn/muninn/boamp"
)

// Count returns the estimated number of tenders matching the query in a single
// HTTP request (not capped by pagination).
func ExampleClient_Count() {
	c := boamp.New()

	n, err := c.Count(context.Background(), muninn.Query{
		Keywords:  []string{"gestion électronique de documents", "GED"},
		ObjetOnly: true, // search in the tender title: precise
	})
	if err != nil {
		panic(err)
	}
	fmt.Printf("%d marchés\n", n)
}

// Search fetches records by paginating. Beyond the API window (10,000) it
// returns an *ErrTruncated carrying the real total, the already-fetched records
// remaining usable.
func ExampleClient_Search() {
	c := boamp.New()

	tenders, err := c.Search(context.Background(), muninn.Query{
		Keywords:  []string{"GED"},
		ObjetOnly: true,
		Limit:     300, // bounds the paginated fetch
	})

	var truncated *boamp.ErrTruncated
	switch {
	case errors.As(err, &truncated):
		fmt.Printf("%d récupérés sur %d\n", truncated.Retrieved, truncated.Total)
	case err != nil:
		panic(err)
	}

	for _, t := range tenders {
		fmt.Printf("[%s] %s — %s\n", t.SourceID, t.Titre, t.Buyer.Nom)
	}
}
