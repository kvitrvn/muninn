# muninn

Librairie Go pour rechercher des marchés publics français et les normaliser
dans un modèle commun. Aujourd'hui une source est implémentée — le **BOAMP**
(Bulletin officiel des annonces des marchés publics, via l'API Opendatasoft de
la DILA) — et l'architecture est prête à en accueillir d'autres (DECP, PLACE…).

```bash
go get github.com/kvitrvn/muninn@latest
```

## Démarrage rapide

```go
package main

import (
	"context"
	"fmt"

	"github.com/kvitrvn/muninn"
	"github.com/kvitrvn/muninn/boamp"
)

func main() {
	ctx := context.Background()
	c := boamp.New()

	// Estimer : combien de marchés « GED » ? (une requête, total non plafonné)
	n, _ := c.Count(ctx, muninn.Query{
		Keywords:  []string{"gestion électronique de documents", "GED"},
		ObjetOnly: true,
	})
	fmt.Printf("%d marchés\n", n)

	// Récupérer les enregistrements (paginé automatiquement)
	tenders, err := c.Search(ctx, muninn.Query{
		Keywords:  []string{"GED"},
		ObjetOnly: true,
		Limit:     300,
	})
	if err != nil {
		// voir la note sur ErrTruncated plus bas
	}
	for _, t := range tenders {
		fmt.Printf("[%s] %s — %s\n", t.SourceID, t.Titre, t.Buyer.Nom)
	}
}
```

## API

### `muninn.Query`

| Champ | Rôle |
|---|---|
| `Keywords` | termes recherchés |
| `ObjetOnly` | `true` = cherche dans le **titre** (`objet`, précis) ; `false` = plein-texte tout l'avis (large, bruité) |
| `MatchAll` | `true` = **ET** (tous les termes) ; `false` = **OU** (au moins un, défaut) |
| `Departements` | filtre par code département acheteur (ex. `"75"`) |
| `DateFrom` / `DateTo` | bornes sur la date de parution |
| `Limit` | borne la récupération paginée de `Search` (0 = défaut du provider) |

### `boamp.Client`

- `Count(ctx, Query) (int, error)` — estimation via `total_count`, **une** requête,
  non plafonnée par la pagination. Idéal pour « combien de marchés ? ».
- `Search(ctx, Query) ([]muninn.Tender, error)` — récupération **paginée**
  (pages de 100) jusqu'au plafond de l'API (`offset+limit ≤ 10 000`) ou à
  `Query.Limit`.
- `New(opts ...Option)` avec `WithHTTPClient` et `WithBaseURL`.

`boamp.Client` implémente `muninn.Provider`.

#### Pagination et `ErrTruncated`

Quand le total dépasse ce que l'API permet de paginer, `Search` renvoie les
enregistrements récupérés **et** un `*boamp.ErrTruncated` portant le total réel :

```go
tenders, err := c.Search(ctx, q)
var truncated *boamp.ErrTruncated
if errors.As(err, &truncated) {
	fmt.Printf("%d récupérés sur %d\n", truncated.Retrieved, truncated.Total)
	// les tenders déjà récupérés restent exploitables
}
```

### Précision vs rappel

Le plein-texte (`ObjetOnly: false`) fait un **ET de tokens n'importe où** dans
l'avis : `"anonymisation"` seul remonte >12 000 avis (bruit de boilerplate),
contre ~10 réellement pertinents en `ObjetOnly: true`. **Pour un comptage fiable,
privilégier `ObjetOnly: true`.**

## Étendre : ajouter une source

Implémenter `muninn.Provider` :

```go
type Provider interface {
	Name() string
	Search(ctx context.Context, q muninn.Query) ([]muninn.Tender, error)
}
```

Mapper le format natif de la source vers `muninn.Tender`, et c'est branché.

## ⚠️ Fiabilité du mapping BOAMP

Les champs top-level (objet, dates, acheteur, département, URL) sont fiables.
En revanche, le **type de procédure** et le **type d'engagement** sont lus dans
le champ imbriqué `donnees`, dont la structure a changé avec le format eForms
(obligatoire depuis janvier 2024) et n'est pas encore pleinement validée sur des
avis récents. Utiliser `client.FetchSchema(ctx)` pour inspecter le schéma réel
avant de s'appuyer sur ces deux champs en production.

## Développement

```bash
go test ./...        # tests unitaires (mapping, pagination mock, filtres)
go vet ./...
go run ./cmd/example -limit 5 "GED" "gestion documentaire"   # démo live (réseau requis)
```

`cmd/example` est un petit CLI de démonstration : mots-clés en arguments,
flags `-limit`, `-all` (ET), `-fulltext` (plein-texte).

## Licence

MIT — voir [LICENSE](LICENSE).
