// Package muninn provides a federated Go search engine for French public
// procurement data.
//
// Providers declare their capabilities and return normalized ProviderResult
// values. Engine queries compatible providers concurrently, preserves partial
// results, consolidates strong cross-source matches, applies common lifecycle
// filters, and returns a deterministic SearchResult page.
//
// The built-in providers live in the boamp, beauamp and decp subpackages.
// Applications compose only the sources they need:
//
//	engine := muninn.NewEngine(boamp.New(), beauamp.New(), decp.New())
//	result, err := engine.Search(ctx, muninn.Query{
//		Keywords:  []string{"GED"},
//		ObjetOnly: true,
//		PageSize:  25,
//	})
//
// Tender.Sources preserves the native notice ID, row ID, related IDs, URL and
// raw payload of every source contributing to a consolidated record. Optional
// BEAUAMP enrichment is secondary and auditable: it never changes the BOAMP
// result used to decide that a tender is open. Muninn deliberately favors
// missed merges over false merges when cross-source evidence is incomplete.
package muninn
