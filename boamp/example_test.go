package boamp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/kvitrvn/muninn"
	"github.com/kvitrvn/muninn/boamp"
)

func ExampleClient_Search() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"total_count": 1,
			"results": []map[string]any{{
				"idweb": "26-1", "objet": "Solution GED", "nomacheteur": "Mairie",
			}},
		})
	}))
	defer srv.Close()

	client := boamp.New(boamp.WithBaseURL(srv.URL), boamp.WithHTTPClient(http.DefaultClient))
	result, err := client.Search(context.Background(), muninn.Query{
		Keywords:  []string{"GED"},
		ObjetOnly: true,
	})
	if err != nil {
		panic(err)
	}
	for _, tender := range result.Items {
		fmt.Printf("[%s] %s — %s\n", tender.Sources[0].ID, tender.Objet, tender.Buyer.Nom)
	}
	// Output:
	// [26-1] Solution GED — Mairie
}
