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

func TestQuery_ValidateSupplierSIRET(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{name: "empty", value: ""},
		{name: "whitespace", value: "   "},
		{name: "exact 14 digits", value: "49884169100039"},
		{name: "trimmed exact 14 digits", value: " 49884169100039 "},
		{name: "too short", value: "4988416910003", wantErr: true},
		{name: "too long", value: "498841691000390", wantErr: true},
		{name: "non digit", value: "4988416910003A", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := (Query{SupplierSIRET: test.value}).Validate()
			if !test.wantErr {
				if err != nil {
					t.Fatalf("Validate() = %v", err)
				}
				return
			}
			var validation *ValidationError
			if !errors.As(err, &validation) || validation.Field != "SupplierSIRET" {
				t.Fatalf("Validate() = %v, want SupplierSIRET validation error", err)
			}
		})
	}
}

func TestQuery_ValidateSupplierSIREN(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{name: "empty", value: ""},
		{name: "whitespace", value: "   "},
		{name: "exact 9 digits", value: "123456789"},
		{name: "trimmed exact 9 digits", value: " 123456789 "},
		{name: "too short", value: "12345678", wantErr: true},
		{name: "too long", value: "1234567890", wantErr: true},
		{name: "non digit", value: "12345678A", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := (Query{SupplierSIREN: test.value}).Validate()
			if !test.wantErr {
				if err != nil {
					t.Fatalf("Validate() = %v", err)
				}
				return
			}
			var validation *ValidationError
			if !errors.As(err, &validation) || validation.Field != "SupplierSIREN" {
				t.Fatalf("Validate() = %v, want SupplierSIREN validation error", err)
			}
		})
	}
}

func TestQuery_ValidateEnrichmentOptions(t *testing.T) {
	tests := []struct {
		name    string
		options EnrichmentOptions
		field   string
	}{
		{
			name:    "negative history months",
			options: EnrichmentOptions{HistoryMonths: -1},
			field:   "Enrichment.HistoryMonths",
		},
		{
			name:    "history months above maximum",
			options: EnrichmentOptions{HistoryMonths: MaxEnrichmentHistoryMonths + 1},
			field:   "Enrichment.HistoryMonths",
		},
		{
			name:    "history limit above maximum",
			options: EnrichmentOptions{HistoryLimit: MaxEnrichmentHistoryLimit + 1},
			field:   "Enrichment.HistoryLimit",
		},
		{
			name:    "negative candidate limit",
			options: EnrichmentOptions{CandidateLimit: -1},
			field:   "Enrichment.CandidateLimit",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := (Query{Enrichment: &test.options}).Validate()
			var validation *ValidationError
			if !errors.As(err, &validation) || validation.Field != test.field {
				t.Fatalf("Validate() = %v, want field %s", err, test.field)
			}
		})
	}

	if err := (Query{Enrichment: &EnrichmentOptions{}}).Validate(); err != nil {
		t.Fatalf("zero options should use defaults: %v", err)
	}
}

func TestValidateProviderQuery_RejectsEngineEnrichment(t *testing.T) {
	var validation *ValidationError
	err := ValidateProviderQuery(Query{Enrichment: &EnrichmentOptions{}})
	if !errors.As(err, &validation) || validation.Field != "Enrichment" {
		t.Fatalf("error = %v", err)
	}
}
