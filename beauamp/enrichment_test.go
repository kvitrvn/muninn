package beauamp

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kvitrvn/muninn"
)

type enrichmentRoundTripper func(*http.Request) (*http.Response, error)

func (f enrichmentRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func enrichmentHTTPResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func enrichmentPrimary(id string, published, deadline time.Time) muninn.Tender {
	return muninn.Tender{
		Sources:           []muninn.SourceReference{{Provider: "boamp", ID: id}},
		Objet:             "Maintenance et hébergement de la solution de gestion documentaire",
		CPVCodes:          []string{"72000000-5"},
		Buyer:             muninn.Buyer{SIREN: "267500452"},
		AvisType:          muninn.AvisAppelConcurrence,
		DatePublication:   published,
		DateLimiteReponse: deadline,
	}
}

func enrichmentRow(
	recordID, attributionID, contractID, object, cpv, decision, published string,
) map[string]any {
	return map[string]any{
		"__id":                  recordID,
		"id_boamp_attribution":  attributionID,
		"id_boamp_contrat":      contractID,
		"objet":                 object,
		"cpv":                   cpv,
		"decision":              decision,
		"date_avis_attribution": published,
		"siren_acheteur":        "267500452",
	}
}

func TestEnrichTender_IdentifierRelationsAndLifecycleConflict(t *testing.T) {
	openAt := time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC)
	primary := enrichmentPrimary(
		"26-OPEN",
		time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
	)
	exactByAttribution := aggregateRows([]map[string]any{
		enrichmentRow(
			"row-a", "26-OPEN", "26-CONTRACT",
			primary.Objet, "72000000", "attribue", "2026-07-01",
		),
	})[0]
	exactByContract := aggregateRows([]map[string]any{
		enrichmentRow(
			"row-b", "26-AWARD", "26-OPEN",
			primary.Objet, "72000000", "attribue", "2026-07-02",
		),
	})[0]

	got := enrichTender(
		primary,
		[]muninn.Tender{exactByContract, exactByAttribution},
		muninn.EnrichmentOptions{HistoryMonths: 24, HistoryLimit: 5},
		openAt,
	)

	if len(got.ExactRelations) != 2 {
		t.Fatalf("exact relations = %+v", got.ExactRelations)
	}
	confidences := map[muninn.RelationConfidence]bool{}
	for _, relation := range got.ExactRelations {
		confidences[relation.Confidence] = true
	}
	if !confidences[muninn.ConfidenceExact] || !confidences[muninn.ConfidenceSourceReported] {
		t.Fatalf("confidences = %+v", confidences)
	}
	if len(got.Candidates) != 0 {
		t.Fatalf("identifier relations leaked into candidates: %+v", got.Candidates)
	}
	lifecycleConflicts := 0
	for _, conflict := range got.Conflicts {
		if conflict.Code == "lifecycle_conflict" {
			lifecycleConflicts++
		}
	}
	if lifecycleConflicts != 2 {
		t.Fatalf("lifecycle conflicts = %+v", got.Conflicts)
	}
	if primary.StatusAt(openAt) != muninn.StatusOpen {
		t.Fatal("primary BOAMP tender was mutated")
	}
}

func TestEnrichTender_ContradictoryReportedContractIDsAreExplicit(t *testing.T) {
	openAt := time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC)
	primary := enrichmentPrimary(
		"26-AWARD",
		time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
	)
	rows := []map[string]any{
		enrichmentRow("row-1", "26-AWARD", "26-CONTRACT-A", primary.Objet, "72000000", "attribue", "2026-07-01"),
		enrichmentRow("row-2", "26-AWARD", "26-CONTRACT-B", primary.Objet, "72000000", "attribue", "2026-07-01"),
	}

	got := enrichTender(
		primary,
		aggregateRows(rows),
		muninn.EnrichmentOptions{HistoryMonths: 24, HistoryLimit: 5},
		openAt,
	)
	found := false
	for _, conflict := range got.Conflicts {
		if conflict.Code == "identifier_conflict" {
			found = true
		}
	}
	if !found {
		t.Fatalf("conflicts = %+v", got.Conflicts)
	}
	if len(got.ExactRelations) != 1 || len(got.Candidates) != 0 {
		t.Fatalf("relations = %+v candidates = %+v", got.ExactRelations, got.Candidates)
	}
}

func TestEnrichTender_CompositeCandidateRequiresEveryProof(t *testing.T) {
	primary := enrichmentPrimary(
		"26-OPEN",
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC),
	)
	valid := mapRecord(enrichmentRow(
		"valid", "26-OTHER", "",
		"Maintenance et hébergement de la solution de gestion documentaire",
		"72000000", "attribue", "2026-02-01",
	))
	falseFriend := mapRecord(enrichmentRow(
		"false-friend", "26-FRIEND", "",
		"Maintenance des ascenseurs et portes automatiques",
		"72000000", "attribue", "2026-02-01",
	))
	tooOld := mapRecord(enrichmentRow(
		"too-old", "26-OLD", "",
		primary.Objet, "72000000", "attribue", "2028-01-02",
	))
	wrongCPV := mapRecord(enrichmentRow(
		"wrong-cpv", "26-CPV", "",
		primary.Objet, "45000000", "attribue", "2026-02-01",
	))

	got := enrichTender(
		primary,
		[]muninn.Tender{valid, falseFriend, tooOld, wrongCPV},
		muninn.EnrichmentOptions{HistoryMonths: 24, HistoryLimit: 5},
		time.Date(2028, 2, 1, 0, 0, 0, 0, time.UTC),
	)
	if len(got.Candidates) != 1 || got.Candidates[0].Tender.Sources[0].RecordID != "valid" {
		t.Fatalf("candidates = %+v", got.Candidates)
	}
	evidence := got.Candidates[0].Evidence
	if evidence.ObjectSimilarity < 0.85 ||
		!evidence.TemporalConsistent ||
		len(evidence.CPVRoots) != 1 ||
		evidence.BuyerSIREN != "267500452" {
		t.Fatalf("candidate evidence = %+v", evidence)
	}
}

func TestAggregateRows_PreservesLotsSuppliersAndNativeRows(t *testing.T) {
	base := enrichmentRow(
		"", "26-AWARD", "26-OPEN", "Prestations informatiques",
		"72000000", "attribue", "2026-06-03",
	)
	rows := make([]map[string]any, 0, 3)
	for _, values := range []struct {
		recordID, lotID, siret, siren, cpv string
		amount                             float64
	}{
		{"row-1", "1", "42869270100011", "428692701", "72000000", 100},
		{"row-2", "1", "55210055400013", "552100554", "72000000", 100},
		{"row-3", "2", "73282932000074", "732829320", "72500000", 200},
	} {
		row := make(map[string]any, len(base)+6)
		for key, value := range base {
			row[key] = value
		}
		row["__id"] = values.recordID
		row["id_lot"] = values.lotID
		row["siret_fournisseur"] = values.siret
		row["siren_fournisseur"] = values.siren
		row["cpv"] = values.cpv
		row["valeur_estimee_lot"] = values.amount
		rows = append(rows, row)
	}

	got := aggregateRows(rows)
	if len(got) != 1 {
		t.Fatalf("notices = %+v", got)
	}
	notice := got[0]
	if len(notice.Sources) != 3 {
		t.Fatalf("native sources = %+v", notice.Sources)
	}
	if len(notice.Lots) != 2 {
		t.Fatalf("lots = %+v", notice.Lots)
	}
	if notice.Lots[0].ID != "1" || len(notice.Lots[0].Suppliers) != 2 ||
		notice.Lots[0].MontantEstime != 100 {
		t.Fatalf("lot 1 = %+v", notice.Lots[0])
	}
	if notice.Lots[1].ID != "2" || len(notice.Lots[1].Suppliers) != 1 ||
		notice.Lots[1].MontantEstime != 200 {
		t.Fatalf("lot 2 = %+v", notice.Lots[1])
	}
}

func TestMapRecord_GroupSupplierSIRENsAndEstimatedBuyerEvidence(t *testing.T) {
	row := enrichmentRow(
		"row-group", "26-AWARD", "26-OPEN", "Maintenance incendie",
		"50700000", "attribue", "2026-06-03",
	)
	row["siren_fournisseur"] = "['712056266', '517681417']"
	row["nom_declare_fournisseur"] = "Groupement DEF-EISS"
	row["siren_acheteur_connu"] = false
	row["prix_attribution_lot"] = float64(2276000)
	row["id_lot"] = "LOT-0001"

	tender := mapRecord(row)
	if len(tender.Suppliers) != 2 ||
		tender.Suppliers[0].SIREN != "712056266" ||
		tender.Suppliers[1].SIREN != "517681417" {
		t.Fatalf("suppliers = %+v", tender.Suppliers)
	}
	if len(tender.Lots) != 1 || tender.Lots[0].MontantEstime != 2276000 {
		t.Fatalf("lots = %+v", tender.Lots)
	}
	evidence := relationEvidence(
		enrichmentPrimary("26-OPEN", time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC), time.Time{}),
		tender,
		"26-OPEN",
		[]string{"26-AWARD"},
		[]string{"26-OPEN"},
	)
	if !evidence.BuyerSIRENEstimated {
		t.Fatalf("evidence = %+v", evidence)
	}
}

func TestParseYearlyTitle(t *testing.T) {
	tests := map[string]struct {
		year int
		ok   bool
	}{
		"beauamp_2025_1.1.0.csv": {2025, true},
		"2023-beauamp1.1.0.csv":  {2023, true},
		"beauamp_juin_2026.csv":  {0, false},
		"beauamp-28-07-2026.csv": {0, false},
	}
	for title, want := range tests {
		year, ok := parseYearlyTitle(title)
		if year != want.year || ok != want.ok {
			t.Errorf("parseYearlyTitle(%q) = (%d, %v), want (%d, %v)", title, year, ok, want.year, want.ok)
		}
	}
}

func TestEnrichTender_BuyerHistoryWindowDecisionLimitAndOrder(t *testing.T) {
	openAt := time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC)
	primary := enrichmentPrimary(
		"26-OPEN",
		time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
	)
	rows := []map[string]any{
		enrichmentRow("exact", "26-OPEN", "", primary.Objet, "72000000", "attribue", "2026-07-20"),
		enrichmentRow("newest", "26-NEW", "", "Autre objet", "72000000", "attribue", "2026-06-20"),
		enrichmentRow("second", "26-SECOND", "", "Autre objet", "72000000", "attribué", "2026-05-20"),
		enrichmentRow("third", "26-THIRD", "", "Autre objet", "72000000", "attribue", "2026-04-20"),
		enrichmentRow("not-awarded", "26-NO", "", "Autre objet", "72000000", "non attribue", "2026-06-25"),
		enrichmentRow("old", "23-OLD", "", "Autre objet", "72000000", "attribue", "2023-01-01"),
		enrichmentRow("wrong-cpv", "26-CPV", "", "Autre objet", "45000000", "attribue", "2026-06-26"),
	}

	got := enrichTender(
		primary,
		aggregateRows(rows),
		muninn.EnrichmentOptions{HistoryMonths: 24, HistoryLimit: 2},
		openAt,
	)
	if len(got.BuyerHistory) != 2 {
		t.Fatalf("history = %+v", got.BuyerHistory)
	}
	if got.BuyerHistory[0].Sources[0].ID != "26-NEW" ||
		got.BuyerHistory[1].Sources[0].ID != "26-SECOND" {
		t.Fatalf("history order = %+v", got.BuyerHistory)
	}
}

func TestRowIdentity_DeduplicatesDailyAndMonthlyCopies(t *testing.T) {
	monthly := enrichmentRow(
		"monthly-id", "26-AWARD", "26-OPEN",
		"Prestations informatiques", "72000000", "attribue", "2026-06-03",
	)
	daily := enrichmentRow(
		"daily-id", "26-AWARD", "26-OPEN",
		"Prestations informatiques", "72000000", "attribue", "2026-06-03",
	)
	if rowIdentity(monthly) != rowIdentity(daily) {
		t.Fatal("overlapping native copies have different row identities")
	}
}

func TestClient_EnrichGroupsPageBuyersIntoOneResourceQuery(t *testing.T) {
	var (
		mu      sync.Mutex
		queries []url.Values
	)
	httpClient := &http.Client{Transport: enrichmentRoundTripper(func(request *http.Request) (*http.Response, error) {
		mu.Lock()
		queries = append(queries, request.URL.Query())
		mu.Unlock()
		return enrichmentHTTPResponse(http.StatusOK, `{
			"data":[{
				"__id":"row-1",
				"id_boamp_attribution":"26-1",
				"objet":"Maintenance informatique",
				"cpv":"72000000",
				"decision":"attribue",
				"date_avis_attribution":"2026-07-20",
				"siren_acheteur":"267500452"
			}],
			"meta":{"page":1,"page_size":200,"total":1}
		}`), nil
	})}
	client := New(
		WithHTTPClient(httpClient),
		WithTabularBaseURL("https://tabular.example/"),
		WithResources("resource-1"),
	)
	openAt := time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC)
	items := []muninn.Tender{
		{
			Sources:         []muninn.SourceReference{{Provider: "boamp", ID: "26-1"}},
			Objet:           "Maintenance informatique",
			CPVCodes:        []string{"72000000"},
			Buyer:           muninn.Buyer{SIREN: "267500452"},
			AvisType:        muninn.AvisAppelConcurrence,
			DatePublication: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			Sources:         []muninn.SourceReference{{Provider: "boamp", ID: "26-2"}},
			Objet:           "Travaux",
			CPVCodes:        []string{"45000000"},
			Buyer:           muninn.Buyer{SIREN: "200072130"},
			AvisType:        muninn.AvisAppelConcurrence,
			DatePublication: time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC),
		},
	}

	result, err := client.Enrich(
		context.Background(),
		items,
		muninn.EnrichmentOptions{},
		openAt,
	)
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(queries) != 1 {
		t.Fatalf("resource queries = %d, want 1", len(queries))
	}
	if got := queries[0].Get("siren_acheteur__in"); got != "200072130,267500452" {
		t.Fatalf("siren_acheteur__in = %q", got)
	}
	if got := queries[0].Get("page_size"); got != "200" {
		t.Fatalf("page_size = %q", got)
	}
	if len(result.Items) != 2 || len(result.Items[0].ExactRelations) != 1 {
		t.Fatalf("result = %+v", result)
	}
}

func TestClient_DailyResourceFallsBackToStreamingCSV(t *testing.T) {
	var calls int
	httpClient := &http.Client{Transport: enrichmentRoundTripper(func(request *http.Request) (*http.Response, error) {
		calls++
		switch request.URL.Host {
		case "tabular.example":
			return enrichmentHTTPResponse(http.StatusNotFound, `{"detail":"not indexed"}`), nil
		case "files.example":
			return enrichmentHTTPResponse(http.StatusOK, strings.Join([]string{
				"__id,siren_acheteur,id_boamp_attribution,objet,cpv,decision,date_avis_attribution",
				"row-1,267500452,26-1,Maintenance informatique,72000000,attribue,2026-07-20",
				"row-2,999999999,26-2,Autre objet,45000000,attribue,2026-07-20",
			}, "\n")), nil
		default:
			t.Fatalf("unexpected URL %s", request.URL)
			return nil, nil
		}
	})}
	client := New(
		WithHTTPClient(httpClient),
		WithTabularBaseURL("https://tabular.example/"),
	)

	rows, truncated, err := client.fetchEnrichmentResource(
		context.Background(),
		enrichmentResource{
			id:    "daily-resource",
			url:   "https://files.example/daily.csv",
			daily: true,
		},
		[]string{"267500452"},
		100,
	)
	if err != nil {
		t.Fatalf("fetchEnrichmentResource: %v", err)
	}
	if calls != 2 || truncated || len(rows) != 1 ||
		firstString(rows[0], "__id") != "row-1" {
		t.Fatalf("calls=%d truncated=%v rows=%+v", calls, truncated, rows)
	}
}
