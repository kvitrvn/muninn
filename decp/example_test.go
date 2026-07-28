package decp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/kvitrvn/muninn"
	"github.com/kvitrvn/muninn/decp"
)

func ExampleClient_Search() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"total_count": 1,
			"results": []map[string]any{{
				"id": "d-1", "objet": "Solution GED", "montant": 120000,
				"titulaire_id_2":              "49884169100039",
				"titulaire_typeidentifiant_2": "SIRET",
			}},
		})
	}))
	defer srv.Close()

	client := decp.New(decp.WithBaseURL(srv.URL), decp.WithHTTPClient(http.DefaultClient))
	result, err := client.Search(context.Background(), muninn.Query{
		Keywords:      []string{"GED"},
		ObjetOnly:     true,
		SupplierSIRET: "49884169100039",
	})
	if err != nil {
		panic(err)
	}
	fmt.Printf("%s — %.0f €\n", result.Items[0].Objet, result.Items[0].MontantEstime)
	// Output:
	// Solution GED — 120000 €
}
