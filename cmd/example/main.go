package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/kvitrvn/muninn"
	"github.com/kvitrvn/muninn/boamp"
)

// defaultKeywords is the broad list of "document management" synonyms (GED and
// strongly related topics), used when no keyword is passed as an argument. Each
// term is searched with OR, maximizing recall for the estimate.
var defaultKeywords = []string{
	"gestion électronique de documents",
	"gestion électronique des documents",
	"GED",
	"gestion documentaire",
	"dématérialisation",
	"archivage électronique",
}

func main() {
	// Usage:
	//   go run ./cmd/example [-limit N] [-all] [-fulltext] [keyword ...]
	// Examples:
	//   go run ./cmd/example                              # default GED list, title, OR
	//   go run ./cmd/example GED "gestion documentaire"   # OR in the title
	//   go run ./cmd/example -all "intelligence artificielle" "données personnelles"
	//                                                     # intersection (AND) in the title
	//   go run ./cmd/example -fulltext RGPD               # full-text over all fields
	limit := flag.Int("limit", 300, "max number of records to fetch (Search); Count always returns the real total")
	matchAll := flag.Bool("all", false, "require ALL keywords (AND) instead of at least one (OR, default)")
	fulltext := flag.Bool("fulltext", false, "search full-text over the whole notice instead of the title only (objet, default)")
	flag.Parse()

	keywords := flag.Args()
	if len(keywords) == 0 {
		keywords = defaultKeywords
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	client := boamp.New()

	// By default we target the title (objet) and combine with OR. -fulltext and
	// -all flip each axis respectively.
	// Limit bounds ONLY the paginated fetch (Search); Count ignores Limit and
	// always returns the real total.
	q := muninn.Query{
		Keywords:  keywords,
		ObjetOnly: !*fulltext,
		MatchAll:  *matchAll,
		Limit:     *limit,
	}

	mode := "OR"
	if q.MatchAll {
		mode = "AND"
	}
	field := "title (objet)"
	if !q.ObjetOnly {
		field = "full-text (all fields)"
	}
	fmt.Printf("Search: %s / %s\n", mode, field)

	// 1. Estimate: how many tenders in total (open or closed)?
	//    A single request, not capped by the pagination window.
	total, err := client.Count(ctx, q)
	if err != nil {
		log.Fatalf("BOAMP count: %v", err)
	}
	fmt.Printf("Estimate: %d public tenders for %d keyword(s): %v\n",
		total, len(keywords), keywords)

	// 2. Paginated fetch of the records. Search retrieves everything up to the
	//    API cap (10,000); beyond that it returns an *ErrTruncated that can be
	//    treated as a warning, the fetched data remaining valid.
	results, err := client.Search(ctx, q)
	var truncated *boamp.ErrTruncated
	switch {
	case errors.As(err, &truncated):
		fmt.Printf("⚠ %d records retrieved out of %d (API cap reached)\n",
			truncated.Retrieved, truncated.Total)
	case err != nil:
		log.Fatalf("BOAMP search: %v", err)
	default:
		fmt.Printf("%d records retrieved (complete set)\n", len(results))
	}

	// Preview of the first 10.
	for i, t := range results {
		if i >= 10 {
			break
		}
		fmt.Printf("  [%s] %s — %s\n", t.SourceID, t.Titre, t.Buyer.Nom)
	}
}
