package muninn

import (
	"errors"
	"testing"
	"time"
)

func TestQuery_Validate(t *testing.T) {
	now := time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name  string
		query Query
		field string
	}{
		{
			name:  "publication range",
			query: Query{PublishedFrom: now, PublishedTo: now.Add(-time.Hour)},
			field: "PublishedFrom",
		},
		{
			name:  "amount range",
			query: Query{MontantMin: 200, MontantMax: 100},
			field: "MontantMin",
		},
		{
			name:  "invalid siren",
			query: Query{BuyerSIREN: "123"},
			field: "BuyerSIREN",
		},
		{
			name:  "oversized page",
			query: Query{PageSize: MaxPageSize + 1},
			field: "PageSize",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var validation *ValidationError
			if err := test.query.Validate(); !errors.As(err, &validation) || validation.Field != test.field {
				t.Fatalf("Validate() = %v, want field %s", err, test.field)
			}
		})
	}
}

func TestValidateProviderQuery_RejectsEnginePagination(t *testing.T) {
	var validation *ValidationError
	err := ValidateProviderQuery(Query{PageSize: 10})
	if !errors.As(err, &validation) || validation.Field != "PageSize" {
		t.Fatalf("error = %v", err)
	}
}
