# Muninn

[![Go Reference](https://pkg.go.dev/badge/github.com/kvitrvn/muninn.svg)](https://pkg.go.dev/github.com/kvitrvn/muninn)

Muninn is a Go library for searching French public procurement data through a
single, normalized API.

It queries compatible data sources concurrently, merges strong cross-source
matches, preserves source provenance, and returns partial results when one
source is unavailable. Muninn is a library only: it does not require a
database, run a server, or impose a cache.

## Features

- Federated search across BOAMP, BEAUAMP, and DECP
- One normalized `Tender` model for notices and awarded contracts
- Exact DECP filtering across all awarded suppliers by SIRET
- Capability-aware routing for exact, approximate, and unsupported filters
- Concurrent provider calls with partial-result reporting
- Conservative cross-source consolidation with raw source provenance
- Deterministic sorting and opaque cursor pagination
- Configurable HTTP clients, timeouts, and retry policies
- No third-party runtime dependencies

## Requirements

Muninn currently targets Go 1.26.

The built-in providers call public remote APIs, so applications should always
set an appropriate context deadline and account for upstream availability and
rate limits.

## Installation

```bash
go get github.com/kvitrvn/muninn
```

## Quick start

Create an engine with the providers your application needs, then call
`Engine.Search`:

```go
package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/kvitrvn/muninn"
	"github.com/kvitrvn/muninn/beauamp"
	"github.com/kvitrvn/muninn/boamp"
	"github.com/kvitrvn/muninn/decp"
)

func main() {
	engine := muninn.NewEngine(
		boamp.New(),
		beauamp.New(),
		decp.New(),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, err := engine.Search(ctx, muninn.Query{
		Keywords:  []string{"GED"},
		ObjetOnly: true,
		Statuses:  []muninn.TenderStatus{muninn.StatusOpen},
		Sort:      muninn.Sort{Field: muninn.SortByDeadline},
		PageSize:  25,
	})
	if err != nil {
		log.Fatal(err)
	}

	for _, tender := range result.Items {
		fmt.Printf(
			"[%s] %s — deadline: %s\n",
			strings.Join(tender.ProviderNames(), "+"),
			tender.Objet,
			tender.DateLimiteReponse.Format(time.DateOnly),
		)
	}

	for _, warning := range result.Warnings {
		log.Printf(
			"provider %s (%s): %s",
			warning.Provider,
			warning.Code,
			warning.Message,
		)
	}
}
```

`Search` returns an error for an invalid query, a canceled context, an invalid
cursor, no compatible provider, or when every attempted provider fails. If at
least one provider succeeds, its results are returned even when another
provider fails; in that case `SearchResult.Partial` is `true` and
`SearchResult.Warnings` explains why.

## Choosing providers

Providers can be used together through `Engine` or directly through their
package clients.

| Package | Data source | Best suited for | Important limitations |
| --- | --- | --- | --- |
| `boamp` | Official BOAMP notices | Active notices, response deadlines, departments, and historical award notices | Supplier SIREN matching is approximate because legacy winners are identified by name; amount and supplier SIRET filtering are unsupported |
| `beauamp` | Enriched BOAMP data on data.gouv.fr | Structured buyer and supplier names/SIRENs, CPV codes, and indicative amounts | Supplier SIRET, department, and deadline filters are unsupported; supplier identity and other enriched fields remain indicative |
| `decp` | Essential public procurement data (DECP) | Published awarded contracts, exact supplier SIRENs/SIRETs, and reference amounts | Contains awards rather than active notices; department, deadline, open, and closed filters are unsupported |

The engine inspects each provider's `Capabilities` before searching:

- providers with exact support are queried normally;
- approximate support is accepted and reported as a warning;
- providers that cannot satisfy a required filter are skipped and reported;
- if no provider can execute the query, `Search` returns
  `muninn.ErrNoCapableProvider`.

Calling a provider directly is stricter. An unsupported criterion returns
`*muninn.UnsupportedFilterError` instead of being ignored. Pagination and
sorting belong to `Engine` and are also rejected by direct provider calls.

## Building queries

Query fields are combined with AND. Values within the same slice are combined
with OR, except when `MatchAll` requires every keyword.

| Field | Description |
| --- | --- |
| `Keywords` | Text terms to search for |
| `ObjetOnly` | Restrict keyword matching to the normalized title/object |
| `MatchAll` | Require every keyword instead of any keyword |
| `Departements` | Buyer department codes |
| `PublishedFrom`, `PublishedTo` | Inclusive publication date range |
| `DeadlineFrom`, `DeadlineTo` | Inclusive response deadline range |
| `CPVCodes` | CPV prefixes |
| `MontantMin`, `MontantMax` | Known contract amount range in euros |
| `BuyerSIREN` | Exact nine-digit buyer SIREN |
| `SupplierSIREN` | Nine-digit legal-entity SIREN of any awarded supplier, across its establishments |
| `SupplierSIRET` | Exact 14-digit SIRET of any awarded supplier |
| `NoticeTypes` | Competition, award, or correction notices |
| `Statuses` | Open, closed, or awarded tenders |
| `OpenAt` | Time used to derive open and closed status; zero means now |
| `Sort` | Publication date descending by default, or deadline ascending |
| `PageSize` | Results per page; 50 by default and 200 maximum |
| `Cursor` | Opaque cursor returned by a previous search |
| `CandidateLimit` | Maximum records collected per provider; zero uses a safe provider default |

Use `Query.Validate` when you want to reject invalid user input before making a
network call:

```go
query := muninn.Query{
	BuyerSIREN:    "200055703",
	SupplierSIREN: "123456789",
	MontantMin:    50_000,
	MontantMax:    250_000,
	PageSize:      50,
}

if err := query.Validate(); err != nil {
	// err is a *muninn.ValidationError
}
```

### Open tenders

`StatusOpen` is a derived lifecycle state. A tender is open when it is a
competition or correction notice, has a known response deadline, has not
passed that deadline, and has no known awarded supplier.

For reproducible searches, set `Query.OpenAt`. When it is zero, the engine uses
the current time and stores that value in the pagination cursor.

```go
result, err := muninn.NewEngine(boamp.New()).Search(ctx, muninn.Query{
	Keywords: []string{"electronic archiving"},
	Statuses: []muninn.TenderStatus{muninn.StatusOpen},
	OpenAt:   time.Date(2026, time.July, 28, 0, 0, 0, 0, time.UTC),
	Sort:     muninn.Sort{Field: muninn.SortByDeadline},
})
```

### Pagination

Pass `NextCursor` back with the same query to fetch the next page:

```go
query := muninn.Query{
	Keywords: []string{"cybersecurity"},
	PageSize: 20,
}

for {
	result, err := engine.Search(ctx, query)
	if err != nil {
		return err
	}

	consume(result.Items)

	if result.NextCursor == "" {
		break
	}
	query.Cursor = result.NextCursor
}
```

Cursors are tied to the complete query. Changing a filter or sort order while
reusing a cursor returns `*muninn.ValidationError`. A cursor may also become
stale if the upstream result set changes between pages.

## Working with results

`SearchResult` contains:

| Field | Meaning |
| --- | --- |
| `Items` | Current page of normalized tenders |
| `Total` | Consolidated matches before pagination |
| `TotalExact` | Whether the total is unaffected by warnings or skipped providers |
| `NextCursor` | Opaque cursor for the next page, or an empty string |
| `Partial` | Whether warnings affected coverage or precision |
| `Warnings` | Machine-readable provider warnings |

Warnings use stable codes:

- `muninn.WarningProviderError`
- `muninn.WarningUnsupportedFilter`
- `muninn.WarningApproximateFilter`
- `muninn.WarningTruncated`

Each `Tender` keeps all contributing source records:

```go
type SourceReference struct {
	Provider  string
	ID        string
	URL       string
	RawFields map[string]any
}
```

Use `Tender.ProviderNames` for display and `Tender.Sources` when you need the
native identifier, source URL, or raw source fields.

Awarded contractors are exposed through `Tender.Suppliers`. DECP preserves the
native titulaire order, accepts SIREN and SIRET identifiers, ignores unrelated
identifier schemes, and removes duplicates. BOAMP maps both legacy result
notices and eForms winners; when a legacy notice only contains a company name,
`SupplierSIREN` resolves official company names and reports approximate support.
Consolidation keeps distinct establishments while using their common SIREN to
relate records across sources.

> Migration note: `Tender.Supplier Buyer` was replaced by
> `Tender.Suppliers []Buyer`. Callers must iterate the list instead of reading a
> single awarded contractor.

Muninn merges records only when there is strong cross-source evidence. When
evidence is incomplete, it keeps separate tenders rather than risk a false
merge.

## HTTP and retry configuration

Each built-in provider accepts a custom `*http.Client` and `retry.Policy`.
Defaults are suitable for basic usage, but production applications will
usually want their own transport, observability, and deadlines.

```go
httpClient := &http.Client{
	Timeout: 15 * time.Second,
}
retryPolicy := retry.Policy{
	MaxAttempts: 4,
	BaseDelay:   200 * time.Millisecond,
	MaxDelay:    3 * time.Second,
}

engine := muninn.NewEngine(
	boamp.New(
		boamp.WithHTTPClient(httpClient),
		boamp.WithRetryPolicy(retryPolicy),
	),
	decp.New(
		decp.WithHTTPClient(httpClient),
		decp.WithRetryPolicy(retryPolicy),
	),
	beauamp.New(
		beauamp.WithHTTPClient(httpClient),
		beauamp.WithRetryPolicy(retryPolicy),
	),
)
```

Retries apply to HTTP 429 responses, HTTP 5xx responses, and transient network
errors. Context cancellation and deadlines stop retries immediately. The
default policy makes four attempts in total with exponential backoff, full
jitter, and a five-second cap.

Provider-specific options include:

- `decp.WithDataset` to select another DECP dataset;
- `beauamp.WithResources` to pin exact tabular resource IDs;
- `beauamp.WithResourceCacheTTL` to control resource-catalog caching;
- base URL options for tests and controlled upstream proxies.

## Error handling

Use `errors.Is` for sentinel and context errors, and `errors.As` for structured
validation errors:

```go
result, err := engine.Search(ctx, query)
if err != nil {
	var validationErr *muninn.ValidationError

	switch {
	case errors.As(err, &validationErr):
		// Invalid field or cursor.
	case errors.Is(err, muninn.ErrNoProviders):
		// The engine was created without providers.
	case errors.Is(err, muninn.ErrNoCapableProvider):
		// Every provider was incompatible with the query.
	case errors.Is(err, context.Canceled),
		errors.Is(err, context.DeadlineExceeded):
		// The request was canceled or timed out.
	default:
		// Every attempted provider failed.
	}
}
```

Provider warnings are not returned as `error` when usable results exist. Check
`result.Partial` and inspect `result.Warnings` after every successful search if
coverage matters to your application.

## Implementing a provider

Implement `muninn.Provider` to add an internal data source or another public
API:

```go
type Provider interface {
	Name() string
	Capabilities() muninn.Capabilities
	Search(context.Context, muninn.Query) (muninn.ProviderResult, error)
}
```

`ProviderResult.Total` describes matches in that provider before federation.
Set `TotalExact` to describe the source count and `Truncated` when collection
stopped before all matches were fetched.

Provider implementations should:

- validate the query with `muninn.ValidateProviderQuery` and `Query.Validate`;
- reject unsupported filters with `muninn.ValidateCapabilities`;
- honor context cancellation;
- return normalized `Tender` values with at least one `SourceReference`;
- avoid implementing engine-owned pagination and sorting.

## Known limitations

- BOAMP is authoritative, but some fields extracted from recent eForms payloads
  remain best effort. Historical supplier-SIREN searches also depend on the
  official company-search API and name matching, so the engine emits an
  approximate-filter warning.
- BEAUAMP data is indicative and its tabular API only supports a subset of the
  source data and filters.
- DECP contains awarded contracts, not the complete active-notice lifecycle.
- Search completeness depends on upstream availability, pagination limits, API
  rate limits, and schema stability.
- Muninn does not provide persistence, caching of search results, an HTTP API,
  or a background index.

## Documentation

- [Package documentation](https://pkg.go.dev/github.com/kvitrvn/muninn)
- Runnable examples are available in the root package and each provider package.

## Development

```bash
go test ./...
go test -race ./...
go vet ./...
```

## License

Muninn is distributed under the [MIT License](./LICENSE).
