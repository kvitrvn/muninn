package boamp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kvitrvn/muninn"
	"github.com/kvitrvn/muninn/retry"
)

// This fixture reproduces the shape of a BOAMP record: top-level fields
// (idweb/objet/nomacheteur/dateparution/nature_categorise_libelle) and the
// nested "donnees" field (XML-to-JSON) with OBJET.PROCEDURE.TYPE_PROCEDURE.OUVERT
// and OBJET.CPV.PRINCIPAL. It validates the mapping logic, not the exact shape
// of "donnees" in every case (multi-lot notably).
const fixtureRecord = `{
	"idweb": "23-123456",
	"objet": "Acquisition d'une solution de gestion électronique de documents (GED)",
	"nomacheteur": "Ministère de la Transition Numérique",
	"code_departement": "75",
	"dateparution": "2026-07-01",
	"datelimitereponse": "2026-08-15",
	"nature_categorise_libelle": "Avis de marché",
	"type_marche": "Marché",
	"url_avis": "https://www.boamp.fr/avis/23-123456",
	"donnees": "{\"OBJET\":{\"PROCEDURE\":{\"TYPE_PROCEDURE\":{\"OUVERT\":\"\"}},\"CPV\":{\"PRINCIPAL\":\"72000000\"}}}"
}`

func TestMapRecord_LegacyContractNotice(t *testing.T) {
	var rec map[string]any
	if err := json.Unmarshal([]byte(fixtureRecord), &rec); err != nil {
		t.Fatalf("fixture invalide: %v", err)
	}

	got := mapRecord(rec)

	if got.Sources[0].Provider != "boamp" || got.Sources[0].ID != "23-123456" {
		t.Errorf("source = %+v", got.Sources[0])
	}
	if got.Buyer.Nom != "Ministère de la Transition Numérique" {
		t.Errorf("Buyer.Nom = %q", got.Buyer.Nom)
	}
	if got.AvisType != muninn.AvisAppelConcurrence {
		t.Errorf("AvisType = %v, attendu AvisAppelConcurrence", got.AvisType)
	}
	if got.Procedure != muninn.ProcedureOuverte {
		t.Errorf("Procedure = %v, attendu ProcedureOuverte", got.Procedure)
	}
	if len(got.CPVCodes) != 1 || got.CPVCodes[0] != "72000000" {
		t.Errorf("CPVCodes = %v, attendu [72000000]", got.CPVCodes)
	}
	if got.DatePublication.Format("2006-01-02") != "2026-07-01" {
		t.Errorf("DatePublication = %v", got.DatePublication)
	}
	if got.DateLimiteReponse.Format("2006-01-02") != "2026-08-15" {
		t.Errorf("DateLimiteReponse = %v", got.DateLimiteReponse)
	}
}

func TestMapRecord_EFormsCPV_26_58589(t *testing.T) {
	rec := map[string]any{
		"idweb": "26-58589",
		"donnees": `{
			"EFORMS": {
				"ContractNotice": {
					"cac:ProcurementProject": {
						"cac:MainCommodityClassification": {
							"cbc:ItemClassificationCode": {
								"@listName": "cpv",
								"#text": "64120000"
							}
						}
					},
					"cac:ProcurementProjectLot": {
						"cac:ProcurementProject": {
							"cac:MainCommodityClassification": {
								"cbc:ItemClassificationCode": {
									"@listName": "cpv",
									"#text": "64120000"
								}
							},
							"cac:AdditionalCommodityClassification": {
								"cbc:ItemClassificationCode": {
									"@listName": "cpv",
									"#text": "48613000"
								}
							}
						}
					}
				}
			}
		}`,
	}

	got := mapRecord(rec)
	want := []string{"64120000", "48613000"}
	if !slices.Equal(got.CPVCodes, want) {
		t.Fatalf("CPVCodes = %v, want %v", got.CPVCodes, want)
	}
}

func TestMapRecord_FNSimpleCPV_26_74350(t *testing.T) {
	rec := map[string]any{
		"idweb": "26-74350",
		"donnees": `{
			"FNSimple": {
				"initial": {
					"identifiantProcedure": "12345678",
					"natureMarche": {
						"codeCPV": {
							"objetPrincipal": {
								"classPrincipale": "38112100"
							}
						}
					},
					"lots": {
						"lot": [
							{
								"codeCPV": {
									"objetPrincipal": {
										"classPrincipale": "38112100"
									}
								}
							},
							{
								"codeCPV": {
									"objetPrincipal": {
										"classPrincipale": "38000000"
									}
								}
							}
						]
					}
				}
			}
		}`,
	}

	got := mapRecord(rec)
	want := []string{"38112100", "38000000"}
	if !slices.Equal(got.CPVCodes, want) {
		t.Fatalf("CPVCodes = %v, want %v", got.CPVCodes, want)
	}
}

func TestMapRecord_FNSimpleBuyerSIRET(t *testing.T) {
	rec := map[string]any{
		"idweb":       "26-75690",
		"nomacheteur": "Commune De Megeve",
		"donnees": `{
			"FNSimple": {
				"organisme": {
					"nomOfficiel": "Commune De Megeve",
					"typeIdentificationNational": {"siret": ""},
					"codeIdentificationNational": "21740173600012",
					"ville": "MEGEVE"
				}
			}
		}`,
	}

	got := mapRecord(rec)
	if got.Buyer.SIRET != "21740173600012" || got.Buyer.SIREN9() != "217401736" {
		t.Fatalf("Buyer = %+v, want FNSimple buyer SIRET", got.Buyer)
	}
}

func TestMapRecord_EFormsBuyerSIRETUsesContractingPartyReference(t *testing.T) {
	rec := map[string]any{
		"idweb":       "26-75225",
		"nomacheteur": "PFC Sud-Ouest",
		"donnees": `{
			"EFORMS": {
				"ContractNotice": {
					"ext:UBLExtensions": {
						"ext:UBLExtension": {
							"ext:ExtensionContent": {
								"efext:EformsExtension": {
									"efac:Organizations": {
										"efac:Organization": [
											{
												"efac:Company": {
													"cac:PartyIdentification": {"cbc:ID": "ORG-PUBLISHER"},
													"cac:PartyName": {"cbc:Name": "Publication Services"},
													"cac:PartyLegalEntity": {"cbc:CompanyID": "11111111100011"}
												}
											},
											{
												"efac:Company": {
													"cac:PartyIdentification": {
														"cbc:ID": {"@schemeName": "organization", "#text": "ORG-BUYER"}
													},
													"cac:PartyName": {"cbc:Name": "PFC Sud-Ouest"},
													"cac:PartyLegalEntity": {
														"cbc:CompanyID": {"@schemeName": "national", "#text": "20003236500013"}
													}
												}
											},
											{
												"efac:Company": {
													"cac:PartyIdentification": {"cbc:ID": "ORG-COURT"},
													"cac:PartyName": {"cbc:Name": "Tribunal administratif"},
													"cac:PartyLegalEntity": {"cbc:CompanyID": "17640002600017"}
												}
											}
										]
									}
								}
							}
						}
					},
					"cac:ContractingParty": {
						"cac:Party": {
							"cac:PartyIdentification": {
								"cbc:ID": {"@schemeName": "organization", "#text": "ORG-BUYER"}
							}
						}
					}
				}
			}
		}`,
	}

	got := mapRecord(rec)
	if got.Buyer.SIRET != "20003236500013" || got.Buyer.SIREN9() != "200032365" {
		t.Fatalf("Buyer = %+v, want referenced eForms buyer SIRET", got.Buyer)
	}
}

func TestMapRecord_EFormsBuyerDoesNotUseUnreferencedOrganization(t *testing.T) {
	rec := map[string]any{
		"idweb":       "26-75018",
		"nomacheteur": "Département de la Loire",
		"donnees": `{
			"EFORMS": {
				"ContractNotice": {
					"ext:UBLExtensions": {
						"ext:UBLExtension": {
							"ext:ExtensionContent": {
								"efext:EformsExtension": {
									"efac:Organizations": {
										"efac:Organization": [
											{
												"efac:Company": {
													"cac:PartyIdentification": {"cbc:ID": "ORG-PUBLISHER"},
													"cac:PartyLegalEntity": {"cbc:CompanyID": "11111111100011"}
												}
											},
											{
												"efac:Company": {
													"cac:PartyIdentification": {"cbc:ID": "ORG-BUYER"},
													"cac:PartyLegalEntity": {
														"cbc:CompanyID": {"@schemeName": "eu", "#text": "1"}
													}
												}
											}
										]
									}
								}
							}
						}
					},
					"cac:ContractingParty": {
						"cac:Party": {
							"cac:PartyIdentification": {"cbc:ID": "ORG-BUYER"}
						}
					}
				}
			}
		}`,
	}

	got := mapRecord(rec)
	if got.Buyer.SIREN != "" || got.Buyer.SIRET != "" {
		t.Fatalf("Buyer = %+v, want no identifier from unrelated eForms organizations", got.Buyer)
	}
}

func TestExtractCPV_EFormsAwardMultiLot(t *testing.T) {
	nested := decodeNestedFixture(t, `{
		"EFORMS": {
			"ContractAwardNotice": {
				"cac:ProcurementProject": {
					"cac:MainCommodityClassification": [
						{
							"cbc:ItemClassificationCode": {
								"@listName": "other-classification",
								"#text": "not-a-cpv"
							}
						},
						{
							"cbc:ItemClassificationCode": {
								"@listName": "cpv",
								"#text": "45214200"
							}
						}
					]
				},
				"cac:ProcurementProjectLot": [
					{
						"cac:ProcurementProject": {
							"cac:MainCommodityClassification": {
								"cbc:ItemClassificationCode": {
									"@listName": "cpv",
									"#text": "45214200"
								}
							},
							"cac:AdditionalCommodityClassification": {
								"cbc:ItemClassificationCode": {
									"@listName": "cpv",
									"#text": "45112500"
								}
							}
						}
					},
					{
						"cac:ProcurementProject": {
							"cac:AdditionalCommodityClassification": [
								{"cbc:ItemClassificationCode": "44220000"},
								{
									"cbc:ItemClassificationCode": {
										"@listName": "cpv",
										"#text": "45112500"
									}
								}
							]
						}
					}
				]
			}
		}
	}`)

	got := extractCPV(nested)
	want := []string{"45214200", "45112500", "44220000"}
	if !slices.Equal(got, want) {
		t.Fatalf("extractCPV() = %v, want %v", got, want)
	}
}

func TestExtractCPV_LegacyLotShapes(t *testing.T) {
	nested := decodeNestedFixture(t, `{
		"OBJET": {
			"CPV": {"PRINCIPAL": "71000000"},
			"LOT": {"CPV": {"PRINCIPAL": "71100000"}},
			"LOTS": {
				"LOT": [
					{"CPV": {"PRINCIPAL": "71200000"}},
					{
						"CPV": [
							{"PRINCIPAL": "71300000"},
							{"PRINCIPAL": "71000000"}
						]
					}
				]
			}
		}
	}`)

	got := extractCPV(nested)
	want := []string{"71000000", "71100000", "71200000", "71300000"}
	if !slices.Equal(got, want) {
		t.Fatalf("extractCPV() = %v, want %v", got, want)
	}
}

func decodeNestedFixture(t *testing.T, raw string) map[string]any {
	t.Helper()
	var nested map[string]any
	if err := json.Unmarshal([]byte(raw), &nested); err != nil {
		t.Fatalf("decode nested fixture: %v", err)
	}
	return nested
}

func TestCapabilities_SupplierSIRETIsUnsupported(t *testing.T) {
	if got := New().Capabilities().Support(muninn.FilterSupplierSIRET); got != muninn.Unsupported {
		t.Fatalf("supplier SIRET support = %v, want Unsupported", got)
	}
}

func TestCapabilities_SupplierSIRENIsApproximate(t *testing.T) {
	if got := New().Capabilities().Support(muninn.FilterSupplierSIREN); got != muninn.Approximate {
		t.Fatalf("supplier SIREN support = %v, want Approximate", got)
	}
}

func TestMapRecord_LegacyAwardSupplier(t *testing.T) {
	rec := map[string]any{
		"idweb":     "16-10001",
		"titulaire": []any{"SCOP ACME NUMERIQUE"},
		"donnees": `{
			"ATTRIBUTION": {
				"DECISION": {
					"TITULAIRE": {
						"DENOMINATION": "SCOP ACME NUMERIQUE",
						"VILLE": "Dijon"
					}
				}
			}
		}`,
	}

	got := mapRecord(rec)
	if len(got.Suppliers) != 1 ||
		got.Suppliers[0].Nom != "SCOP ACME NUMERIQUE" ||
		got.Suppliers[0].Ville != "Dijon" {
		t.Fatalf("Suppliers = %+v, want legacy ACME winner", got.Suppliers)
	}
}

func TestMapRecord_EFormsAwardSupplier(t *testing.T) {
	rec := map[string]any{
		"idweb": "24-30003",
		"donnees": `{
			"EFORMS": {
				"ContractAwardNotice": {
					"efac:NoticeResult": {
						"efac:TenderingParty": {
							"efac:Tenderer": {"cbc:ID": "ORG-0004"}
						}
					},
					"efac:Organizations": {
						"efac:Organization": [
							{
								"efac:Company": {
									"cac:PartyIdentification": {"cbc:ID": "ORG-0001"},
									"cac:PartyName": {"cbc:Name": "CNOUS"},
									"cac:PartyLegalEntity": {"cbc:CompanyID": "18004401800026"}
								}
							},
							{
								"efac:Company": {
									"cac:PartyIdentification": {"cbc:ID": "ORG-0004"},
									"cac:PartyName": {"cbc:Name": "ACME Numérique"},
									"cac:PostalAddress": {"cbc:CityName": "Dijon"},
									"cac:PartyLegalEntity": {"cbc:CompanyID": "12345678900010"}
								}
							}
						]
					}
				}
			}
		}`,
	}

	got := mapRecord(rec)
	if len(got.Suppliers) != 1 ||
		got.Suppliers[0].Nom != "ACME Numérique" ||
		got.Suppliers[0].SIRET != "12345678900010" ||
		got.Suppliers[0].Ville != "Dijon" {
		t.Fatalf("Suppliers = %+v, want eForms ACME winner only", got.Suppliers)
	}
}

func TestMapRecord_FrameworkAgreement(t *testing.T) {
	rec := map[string]any{
		"idweb":       "23-999999",
		"objet":       "Accord-cadre logiciel libre souveraineté numérique",
		"nomacheteur": "Région Test",
		"donnees":     `{"OBJET":{"PROCEDURE":{"TYPE_PROCEDURE":{"RESTREINT":""}},"ACCORD_CADRE_OUI":""}}`,
	}

	got := mapRecord(rec)

	if got.Procedure != muninn.ProcedureRestreinte {
		t.Errorf("Procedure = %v, attendu ProcedureRestreinte", got.Procedure)
	}
	if got.Engagement != muninn.EngagementAccordCadreBC {
		t.Errorf("Engagement = %v, attendu EngagementAccordCadreBC", got.Engagement)
	}
}

// The API returns code_departement as an array (e.g. ["35"]); verify the
// mapping handles it and does not silently fail.
func TestMapRecord_CodeDepartementArray(t *testing.T) {
	rec := map[string]any{
		"idweb":            "24-000001",
		"objet":            "GED",
		"code_departement": []any{"35", "44"},
	}
	got := mapRecord(rec)
	if got.Buyer.CodeDepartement != "35" {
		t.Errorf("CodeDepartement = %q, attendu %q", got.Buyer.CodeDepartement, "35")
	}
}

func TestBuildWhere(t *testing.T) {
	tests := []struct {
		name string
		q    muninn.Query
		want string
	}{
		{
			name: "vide",
			q:    muninn.Query{},
			want: "",
		},
		{
			name: "mots-clés en OR (plein-texte, défaut)",
			q:    muninn.Query{Keywords: []string{"GED", "gestion documentaire"}},
			want: `("GED" OR "gestion documentaire")`,
		},
		{
			name: "objet + ET",
			q:    muninn.Query{Keywords: []string{"IA", "données personnelles"}, ObjetOnly: true, MatchAll: true},
			want: `(objet like "IA" AND objet like "données personnelles")`,
		},
		{
			name: "objet + OU",
			q:    muninn.Query{Keywords: []string{"GED", "SAE"}, ObjetOnly: true},
			want: `(objet like "GED" OR objet like "SAE")`,
		},
		{
			name: "mots-clés + département en AND",
			q:    muninn.Query{Keywords: []string{"GED"}, Departements: []string{"75"}},
			want: `("GED") AND (code_departement="75")`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := buildWhere(tt.q); got != tt.want {
				t.Errorf("buildWhere() = %q, attendu %q", got, tt.want)
			}
		})
	}
}

func TestBuildWhereWithSupplierNamesCombinesCriteria(t *testing.T) {
	got := buildWhereWithSupplierNames(
		muninn.Query{
			Keywords:      []string{"maintenance"},
			ObjetOnly:     true,
			SupplierSIREN: "123456789",
			PublishedFrom: time.Date(2016, time.January, 1, 0, 0, 0, 0, time.UTC),
			PublishedTo:   time.Date(2024, time.December, 31, 0, 0, 0, 0, time.UTC),
		},
		[]string{"ACME NUMERIQUE"},
	)
	want := `(objet like "maintenance") AND ("123456789" OR "ACME NUMERIQUE") AND ` +
		`dateparution >= "2016-01-01" AND dateparution <= "2024-12-31"`
	if got != want {
		t.Errorf("buildWhereWithSupplierNames() = %q\nwant: %q", got, want)
	}
}

// Pagination, Count and truncation are exercised generically against the shared
// Opendatasoft plumbing in internal/ods; here we only cover the BOAMP-specific
// record mapping and where-clause building.

// TestSearch_AdvancedFiltersPostFetch verifies that the approximate BOAMP
// candidate query is followed by exact client-side CPV / amount / SIREN
// filtering.
func TestSearch_AdvancedFiltersPostFetch(t *testing.T) {
	rows := []map[string]any{
		{
			"idweb":       "a",
			"objet":       "GED un",
			"nomacheteur": "Acheteur A",
			"donnees": `{
				"FNSimple": {
					"organisme": {
						"typeIdentificationNational": {"siret": ""},
						"codeIdentificationNational": "11111111100011"
					},
					"initial": {
						"natureMarche": {
							"codeCPV": {"objetPrincipal": {"classPrincipale": "72000000"}}
						}
					}
				}
			}`,
		},
		{
			"idweb":       "b",
			"objet":       "Fournitures",
			"nomacheteur": "Acheteur B",
			"donnees":     `{"OBJET":{"CPV":{"PRINCIPAL":"30190000"}},"ORGANISME":{"ACHETEUR":{"IDENTIFICATION":{"SIREN":"222222222"}}}}`,
		},
		{
			"idweb":       "c",
			"objet":       "GED deux",
			"nomacheteur": "Acheteur A",
			"donnees":     `{"OBJET":{"CPV":{"PRINCIPAL":"72500000"}},"ORGANISME":{"ACHETEUR":{"IDENTIFICATION":{"SIREN":"111111111"}}}}`,
		},
		{
			"idweb":       "d",
			"objet":       "GED eForms",
			"nomacheteur": "Acheteur A",
			"donnees": `{
				"EFORMS": {
					"ContractNotice": {
						"ext:UBLExtensions": {
							"ext:UBLExtension": {
								"ext:ExtensionContent": {
									"efext:EformsExtension": {
										"efac:Organizations": {
											"efac:Organization": {
												"efac:Company": {
													"cac:PartyIdentification": {"cbc:ID": "ORG-0001"},
													"cac:PartyLegalEntity": {"cbc:CompanyID": "11111111100011"}
												}
											}
										}
									}
								}
							}
						},
						"cac:ContractingParty": {
							"cac:Party": {
								"cac:PartyIdentification": {"cbc:ID": "ORG-0001"}
							}
						},
						"cac:ProcurementProject": {
							"cac:MainCommodityClassification": {
								"cbc:ItemClassificationCode": {
									"@listName": "cpv",
									"#text": "72200000"
								}
							}
						}
					}
				}
			}`,
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"total_count": len(rows),
			"results":     rows,
		})
	}))
	defer srv.Close()

	c := New(WithBaseURL(srv.URL), WithHTTPClient(http.DefaultClient))
	got, err := c.Search(context.Background(), muninn.Query{
		CPVCodes:   []string{"72"},
		BuyerSIREN: "111111111",
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	// FNSimple "a", legacy "c" and eForms "d" match both filters.
	if len(got.Items) != 3 {
		t.Fatalf("got %d tenders, want 3: %+v", len(got.Items), got.Items)
	}
	ids := map[string]bool{}
	for _, x := range got.Items {
		ids[x.Sources[0].ID] = true
	}
	if !ids["a"] || !ids["c"] || !ids["d"] {
		t.Errorf("ids = %v, want {a, c, d}", ids)
	}
}

func TestSearch_SupplierSIRENFindsLegacyAndEFormsAwards(t *testing.T) {
	const siren = "123456789"
	rows := []map[string]any{
		{
			"idweb":                     "16-10001",
			"objet":                     "Maintenance applicative historique",
			"titulaire":                 []any{"SCOP ACME NUMERIQUE"},
			"nature_categorise_libelle": "Résultat de marché/",
		},
		{
			"idweb":                     "20-20002",
			"objet":                     "Tierce maintenance applicative",
			"titulaire":                 []any{"acme numérique"},
			"nature_categorise_libelle": "Résultat de marché/",
		},
		{
			"idweb":                     "24-30003",
			"objet":                     "Maintenance et évolutions applicatives",
			"nature_categorise_libelle": "Résultat de marché/",
			"donnees": `{
				"EFORMS": {
					"ContractAwardNotice": {
						"efac:NoticeResult": {
							"efac:TenderingParty": {
								"efac:Tenderer": {"cbc:ID": "ORG-0004"}
							}
						},
						"efac:Organizations": {
							"efac:Organization": {
								"efac:Company": {
									"cac:PartyIdentification": {"cbc:ID": "ORG-0004"},
									"cac:PartyName": {"cbc:Name": "ACME Numérique"},
									"cac:PartyLegalEntity": {"cbc:CompanyID": "12345678900010"}
								}
							}
						}
					}
				}
			}`,
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wantWhere := `("123456789" OR "ACME NUMERIQUE")`
		if got := r.URL.Query().Get("where"); got != wantWhere {
			t.Errorf("where = %q, want %q", got, wantWhere)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"total_count": len(rows),
			"results":     rows,
		})
	}))
	defer srv.Close()

	client := New(
		WithBaseURL(srv.URL),
		WithHTTPClient(http.DefaultClient),
		WithSupplierNameResolver(func(context.Context, string) ([]string, error) {
			return []string{"ACME NUMERIQUE"}, nil
		}),
	)
	got, err := client.Search(context.Background(), muninn.Query{SupplierSIREN: siren})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got.Items) != 3 {
		t.Fatalf("items = %+v, want the three ACME awards", got.Items)
	}
	for _, tender := range got.Items {
		if len(tender.Suppliers) != 1 || tender.Suppliers[0].SIREN9() != siren {
			t.Errorf("%s suppliers = %+v", tender.Sources[0].ID, tender.Suppliers)
		}
	}
}

// TestSearch_TruncatedRetrievedMatchesFilteredCount verifies that, when the
// ODS layer reports a truncation AND the post-fetch advanced filter further
// shrinks the set, ProviderResult metadata tracks the returned subset.
func TestSearch_TruncatedRetrievedMatchesFilteredCount(t *testing.T) {
	rows := []map[string]any{
		{
			"idweb":       "a",
			"objet":       "GED un",
			"nomacheteur": "Acheteur A",
			"donnees":     `{"OBJET":{"CPV":{"PRINCIPAL":"72000000"}},"ORGANISME":{"ACHETEUR":{"IDENTIFICATION":{"SIREN":"111111111"}}}}`,
		},
		{
			"idweb":       "b",
			"objet":       "Fournitures",
			"nomacheteur": "Acheteur B",
			"donnees":     `{"OBJET":{"CPV":{"PRINCIPAL":"30190000"}},"ORGANISME":{"ACHETEUR":{"IDENTIFICATION":{"SIREN":"222222222"}}}}`,
		},
		{
			"idweb":       "c",
			"objet":       "GED deux",
			"nomacheteur": "Acheteur A",
			"donnees":     `{"OBJET":{"CPV":{"PRINCIPAL":"72500000"}},"ORGANISME":{"ACHETEUR":{"IDENTIFICATION":{"SIREN":"111111111"}}}}`,
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// total_count is reported far beyond what this page serves, forcing
		// internal/ods to mark the result truncated.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"total_count": 10,
			"results":     rows,
		})
	}))
	defer srv.Close()

	c := New(WithBaseURL(srv.URL), WithHTTPClient(http.DefaultClient))
	got, err := c.Search(context.Background(), muninn.Query{
		CPVCodes:   []string{"72"},
		BuyerSIREN: "111111111",
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	// Only "a" and "c" survive the CPV/SIREN post-fetch filter.
	if len(got.Items) != 2 {
		t.Fatalf("got %d tenders, want 2: %+v", len(got.Items), got.Items)
	}
	if got.Total != 10 || got.TotalExact || !got.Truncated {
		t.Errorf("metadata = %+v", got)
	}
}

func TestSearch_RejectsUnsupportedAmountFilter(t *testing.T) {
	rows := []map[string]any{
		{
			"idweb":       "a",
			"objet":       "GED",
			"nomacheteur": "Acheteur A",
			"donnees":     `{"OBJET":{"CPV":{"PRINCIPAL":"72000000"}}}`,
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"total_count": len(rows),
			"results":     rows,
		})
	}))
	defer srv.Close()

	c := New(WithBaseURL(srv.URL), WithHTTPClient(http.DefaultClient))
	_, err := c.Search(context.Background(), muninn.Query{MontantMin: 1})
	var unsupported *muninn.UnsupportedFilterError
	if !errors.As(err, &unsupported) || unsupported.Filter != muninn.FilterAmount {
		t.Fatalf("error = %v, want unsupported amount filter", err)
	}
}

// TestSearch_RetriesTransientServerError verifies WithRetryPolicy (exported
// via the public retry package, since boamp is backed by internal/ods.Client)
// actually retries a transient 500 instead of failing Search outright.
func TestSearch_RetriesTransientServerError(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&calls, 1) <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"total_count": 1,
			"results":     []map[string]any{{"idweb": "rec-0", "objet": "GED"}},
		})
	}))
	defer srv.Close()

	c := New(
		WithBaseURL(srv.URL),
		WithHTTPClient(http.DefaultClient),
		WithRetryPolicy(retry.Policy{MaxAttempts: 4, BaseDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond}),
	)
	got, err := c.Search(context.Background(), muninn.Query{Keywords: []string{"GED"}})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got.Items) != 1 || got.Items[0].Sources[0].ID != "rec-0" {
		t.Fatalf("got %+v, want [rec-0]", got)
	}
	if calls != 3 {
		t.Errorf("calls = %d, want 3 (2 failures + 1 success)", calls)
	}
}

func TestBuildWhere_CPVPrefilteredOtherAdvancedFiltersRemainClientSide(t *testing.T) {
	// BOAMP can narrow CPVs through full-text search on the nested donnees blob.
	// Amount and buyer SIREN remain exact client-side filters.
	got := buildWhere(muninn.Query{
		Keywords:   []string{"GED"},
		CPVCodes:   []string{"72"},
		MontantMin: 100000,
		BuyerSIREN: "111111111",
	})
	want := `("GED") AND search(donnees, "72")`
	if got != want {
		t.Errorf("buildWhere() = %q, want %q", got, want)
	}
}

func TestCPVSearchClause_MultiplePrefixes(t *testing.T) {
	got := cpvSearchClause([]string{" 38112100 ", "", "38295000"})
	want := `(search(donnees, "38112100") OR search(donnees, "38295000"))`
	if got != want {
		t.Errorf("cpvSearchClause() = %q, want %q", got, want)
	}
}
