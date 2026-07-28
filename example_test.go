package muninn_test

import (
	"context"
	"fmt"
	"time"

	"github.com/kvitrvn/muninn"
)

type staticProvider struct {
	name    string
	tenders []muninn.Tender
}

var _ muninn.Provider = staticProvider{}

func (p staticProvider) Name() string { return p.name }

func (p staticProvider) Capabilities() muninn.Capabilities {
	return muninn.Capabilities{
		muninn.FilterTitleKeywords: muninn.Exact,
		muninn.FilterNoticeType:    muninn.Exact,
		muninn.FilterStatusOpen:    muninn.Exact,
	}
}

func (p staticProvider) Search(_ context.Context, _ muninn.Query) (muninn.ProviderResult, error) {
	return muninn.ProviderResult{
		Items:      p.tenders,
		Total:      len(p.tenders),
		TotalExact: true,
	}, nil
}

func ExampleEngine_Search() {
	p := staticProvider{name: "interne", tenders: []muninn.Tender{
		{
			Sources:           []muninn.SourceReference{{Provider: "interne", ID: "1"}},
			Objet:             "Solution GED",
			AvisType:          muninn.AvisAppelConcurrence,
			DatePublication:   time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
			DateLimiteReponse: time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC),
		},
	}}

	result, err := muninn.NewEngine(p).Search(context.Background(), muninn.Query{
		Keywords:  []string{"GED"},
		ObjetOnly: true,
		Statuses:  []muninn.TenderStatus{muninn.StatusOpen},
		OpenAt:    time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		panic(err)
	}
	for _, tender := range result.Items {
		fmt.Printf("%s — %s\n", tender.Objet, tender.StatusAt(time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC)))
	}
	// Output:
	// Solution GED — open
}
