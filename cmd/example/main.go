package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/kvitrvn/muninn"
	"github.com/kvitrvn/muninn/beauamp"
	"github.com/kvitrvn/muninn/boamp"
	"github.com/kvitrvn/muninn/consolidate"
	"github.com/kvitrvn/muninn/decp"
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
	//   go run ./cmd/example [-source S] [-limit N] [-all] [keyword ...]
	// Examples:
	//   go run ./cmd/example                                 # all sources, default GED list, OR
	//   go run ./cmd/example -source decp GED                # awarded contracts (amounts, winners)
	//   go run ./cmd/example -source beauamp "gestion documentaire"
	//   go run ./cmd/example -all "intelligence artificielle" "données personnelles"
	source := flag.String("source", "all", "data source: all (BEAUAMP+DECP consolidated), beauamp, decp, or boamp")
	limit := flag.Int("limit", 300, "max number of records to fetch (Search); Count always returns the real total")
	matchAll := flag.Bool("all", false, "require ALL keywords (AND) instead of at least one (OR, default)")
	flag.Parse()

	keywords := flag.Args()
	if len(keywords) == 0 {
		keywords = defaultKeywords
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	provider, counter := build(*source)

	// Target the title (objet) and combine with OR by default; -all switches to
	// AND. Limit bounds ONLY the paginated fetch (Search).
	q := muninn.Query{
		Keywords:  keywords,
		ObjetOnly: true,
		MatchAll:  *matchAll,
		Limit:     *limit,
	}

	mode := "OR"
	if q.MatchAll {
		mode = "AND"
	}
	fmt.Printf("Source: %s / mode: %s\n", provider.Name(), mode)

	// 1. Estimate: how many tenders match (a single request per source).
	if counter != nil {
		total, err := counter.Count(ctx, q)
		if err != nil {
			log.Fatalf("count: %v", err)
		}
		fmt.Printf("Estimate: %d tenders for %d keyword(s): %v\n", total, len(keywords), keywords)
	}

	// 2. Fetch the records. Beyond the source's pagination window / the limit,
	//    Search returns a *muninn.ErrTruncated that is treated as a warning.
	results, err := provider.Search(ctx, q)
	var truncated *muninn.ErrTruncated
	switch {
	case errors.As(err, &truncated):
		fmt.Printf("⚠ %d records retrieved out of ~%d (cap reached)\n", truncated.Retrieved, truncated.Total)
	case err != nil:
		log.Fatalf("search: %v", err)
	default:
		fmt.Printf("%d records retrieved\n", len(results))
	}

	// Preview of the first 10, with the award depth when available.
	for i, t := range results {
		if i >= 10 {
			break
		}
		fmt.Printf("  [%s] %s\n", t.Source, t.Objet)
		fmt.Printf("      %s", orDash(t.Buyer.Nom, t.Buyer.SIREN9()))
		if t.Supplier.SIREN9() != "" || t.Supplier.Nom != "" {
			fmt.Printf(" → %s", orDash(t.Supplier.Nom, t.Supplier.SIREN9()))
		}
		if t.MontantEstime > 0 {
			fmt.Printf(" (%.0f €)", t.MontantEstime)
		}
		fmt.Println()
	}
}

// counter is implemented by providers that expose a cheap Count.
type counter interface {
	Count(context.Context, muninn.Query) (int, error)
}

// build wires the requested provider, and a Count-capable handle when the source
// supports it (the consolidator does not).
func build(source string) (muninn.Provider, counter) {
	switch source {
	case "beauamp":
		c := beauamp.New()
		return c, c
	case "decp":
		c := decp.New()
		return c, c
	case "boamp":
		c := boamp.New()
		return c, c
	default: // "all"
		return consolidate.New(beauamp.New(), decp.New()), nil
	}
}

// orDash returns the name, falling back to the SIREN, or "—" when both are empty.
func orDash(name, siren string) string {
	switch {
	case name != "":
		return name
	case siren != "":
		return siren
	default:
		return "—"
	}
}
