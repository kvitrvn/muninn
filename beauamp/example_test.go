package beauamp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/kvitrvn/muninn"
	"github.com/kvitrvn/muninn/beauamp"
)

func ExampleClient_Search() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{
				"id_boamp_attribution": "26-1",
				"objet":                "Solution GED",
				"nom_declare_acheteur": "AP-HP",
			}},
			"meta": map[string]any{"page": 1, "page_size": 100, "total": 1},
		})
	}))
	defer srv.Close()

	client := beauamp.New(
		beauamp.WithTabularBaseURL(srv.URL+"/"),
		beauamp.WithResources("res-2026-06"),
	)
	result, err := client.Search(context.Background(), muninn.Query{
		Keywords:  []string{"GED"},
		ObjetOnly: true,
	})
	if err != nil {
		panic(err)
	}
	for _, tender := range result.Items {
		fmt.Printf("%s — %s\n", tender.Objet, tender.Buyer.Nom)
	}
	// Output:
	// Solution GED — AP-HP
}
