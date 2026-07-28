package muninn

import (
	"testing"
	"time"
)

func TestTender_DedupKeyUsesSourceIdentity(t *testing.T) {
	a := Tender{
		Sources:  []SourceReference{{Provider: "boamp", ID: "a"}},
		Buyer:    Buyer{SIREN: "100000009"},
		CPVCodes: []string{"72000000"},
	}
	b := Tender{
		Sources:  []SourceReference{{Provider: "boamp", ID: "b"}},
		Buyer:    Buyer{SIREN: "100000009"},
		CPVCodes: []string{"72000000"},
	}
	if a.DedupKey() == b.DedupKey() {
		t.Fatalf("different source records share key %q", a.DedupKey())
	}
}

func TestTender_DedupKeyFallbackIsCPVOrderIndependent(t *testing.T) {
	a := Tender{Buyer: Buyer{SIREN: "100000009"}, Objet: "Solution GED", CPVCodes: []string{"72000000", "72500000"}}
	b := Tender{Buyer: Buyer{SIREN: "100000009"}, Objet: "Solution GED", CPVCodes: []string{"72500000", "72000000"}}
	if a.DedupKey() != b.DedupKey() {
		t.Fatalf("fallback keys differ: %q vs %q", a.DedupKey(), b.DedupKey())
	}
}

func TestTender_StatusAt(t *testing.T) {
	at := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		tender Tender
		want   TenderStatus
	}{
		{
			name: "open through deadline day",
			tender: Tender{
				AvisType:          AvisAppelConcurrence,
				DateLimiteReponse: time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC),
			},
			want: StatusOpen,
		},
		{
			name: "closed after deadline",
			tender: Tender{
				AvisType:          AvisAppelConcurrence,
				DateLimiteReponse: time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC),
			},
			want: StatusClosed,
		},
		{
			name:   "award wins over deadline",
			tender: Tender{AvisType: AvisAttribution},
			want:   StatusAwarded,
		},
		{
			name:   "missing deadline is unknown",
			tender: Tender{AvisType: AvisAppelConcurrence},
			want:   StatusUnknown,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.tender.StatusAt(at); got != test.want {
				t.Fatalf("StatusAt() = %s, want %s", got, test.want)
			}
		})
	}
}
