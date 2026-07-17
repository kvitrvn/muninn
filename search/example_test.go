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
