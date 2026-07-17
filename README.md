# muninn

Librairie Go pour rechercher des marchés publics français et les normaliser
dans un modèle commun. Trois sources complémentaires couvrent le cycle d'un
marché — de l'annonce à l'attribution — et une couche de **consolidation** les
croise pour obtenir, en un seul enregistrement, l'acheteur, le titulaire (le
gagnant) et le montant.

```bash
go get github.com/kvitrvn/muninn@latest
```

## Sources

| Package | Source | Rôle |
|---|---|---|
| `boamp` | **BOAMP** (avis officiels, API Opendatasoft DILA) | annonces live faisant foi |
| `beauamp` | **BEAUAMP** (data.gouv.fr, API tabulaire) | BOAMP consolidé + SIRENE + résultats d'attribution : objet, **acheteur**, **titulaire**, montant indicatif |
| `decp` | **DECP** (data.economie.gouv.fr, Opendatasoft) | données essentielles des marchés **attribués** (≥ 40 000 € HT) : montant et titulaire de **référence** |

Chaque source implémente `muninn.Provider` et mappe son format natif vers
`muninn.Tender`.

## Démarrage rapide

Estimer et lister les marchés « GED », consolidés (noms via BEAUAMP, montant de
référence via DECP) :

```go
package main

import (
	"context"
	"fmt"

	"github.com/kvitrvn/muninn"
	"github.com/kvitrvn/muninn/beauamp"
	"github.com/kvitrvn/muninn/consolidate"
	"github.com/kvitrvn/muninn/decp"
)

func main() {
	ctx := context.Background()
	c := consolidate.New(beauamp.New(), decp.New())

	tenders, err := c.Search(ctx, muninn.Query{
		Keywords:  []string{"gestion électronique de documents", "GED"},
		ObjetOnly: true,
	})
	if err != nil {
		// voir la note sur ErrTruncated plus bas
	}
	for _, t := range tenders {
		fmt.Printf("[%s] %s — %s → %s (%.0f €)\n",
			t.Source, t.Objet, t.Buyer.Nom, t.Supplier.Nom, t.MontantEstime)
	}
}
```

Pour une simple estimation sur une source unique :

```go
n, _ := decp.New().Count(ctx, muninn.Query{Keywords: []string{"GED"}, ObjetOnly: true})
fmt.Printf("%d marchés attribués\n", n)
```

## API

### `muninn.Query`

| Champ | Rôle |
|---|---|
| `Keywords` | termes recherchés |
| `ObjetOnly` | `true` = cherche dans le **titre** (`objet`, précis) ; `false` = plein-texte (BOAMP/DECP) |
| `MatchAll` | `true` = **ET** (tous les termes) ; `false` = **OU** (au moins un, défaut) |
| `Departements` | filtre par code département acheteur (BOAMP) |
| `DateFrom` / `DateTo` | bornes de date |
| `Limit` | borne la récupération paginée de `Search` (0 = défaut du provider) |

### `muninn.Tender`

Modèle normalisé. Les champs de profondeur : `Buyer` (acheteur), `Supplier`
(titulaire/gagnant), `MontantEstime`, `CPVCodes`. `DedupKey()` calcule la clé de
rapprochement inter-sources (SIREN acheteur + CPV).

### Providers

Tous exposent :

- `Count(ctx, Query) (int, error)` — estimation en une requête (par ressource
  pour BEAUAMP).
- `Search(ctx, Query) ([]muninn.Tender, error)` — récupération paginée.
- `New(opts ...Option)` — options `WithHTTPClient`, `WithBaseURL`, etc.

`consolidate.New(providers...)` est lui-même un `muninn.Provider` : il interroge
chaque source, déduplique (`DedupKey`) et enrichit — le montant **DECP**
(faisant foi) l'emporte sur l'indicatif BEAUAMP, les parties sont complétées
champ par champ.

#### Pagination et `ErrTruncated`

Quand le total dépasse ce qui peut être paginé (ou `Query.Limit`), `Search`
renvoie les enregistrements récupérés **et** un `*muninn.ErrTruncated` portant le
total réel :

```go
tenders, err := c.Search(ctx, q)
var truncated *muninn.ErrTruncated
if errors.As(err, &truncated) {
	fmt.Printf("%d récupérés sur %d\n", truncated.Retrieved, truncated.Total)
	// les tenders déjà récupérés restent exploitables
}
```

### Précision vs rappel

Le plein-texte (`ObjetOnly: false`) fait un **ET de tokens n'importe où** dans
l'avis et remonte beaucoup de bruit de boilerplate. **Pour un comptage fiable,
privilégier `ObjetOnly: true`** (recherche dans le titre). Sur BEAUAMP, l'API
tabulaire ne filtre que par colonne : la recherche porte toujours sur `objet`.

## Étendre : ajouter une source

Implémenter `muninn.Provider` :

```go
type Provider interface {
	Name() string
	Search(ctx context.Context, q muninn.Query) ([]muninn.Tender, error)
}
```

Mapper le format natif vers `muninn.Tender`, l'ajouter à `consolidate.New(...)`,
et c'est branché. Les sources Opendatasoft (comme BOAMP et DECP) peuvent
réutiliser la plomberie partagée `internal/ods`.

## ⚠️ Fiabilité et limites

- **BEAUAMP** est déclarée « à valeur indicative » (l'avis BOAMP fait foi) et est
  interrogée **par période** via l'API tabulaire data.gouv.fr : sans dates, la
  recherche cible le dernier mois disponible ; les gros fichiers d'historique ne
  sont pas servis par l'API. Les IDs de ressources tournent (résolution
  automatique via le catalogue, ou `beauamp.WithResources(...)`).
- **DECP** ne couvre que les marchés ≥ 40 000 € HT, et ne fournit que les SIRET
  (pas les noms) — d'où l'intérêt de la consolidation avec BEAUAMP.
- **BOAMP** : les champs top-level sont fiables ; le type de procédure/engagement
  lu dans `donnees` est heuristique (format eForms). `client.FetchSchema(ctx)`
  inspecte le schéma réel.

## Développement

```bash
go test ./...        # mapping, pagination mock, filtres, consolidation
go vet ./...
go run ./cmd/example -source all -limit 5 "GED"          # démo consolidée (réseau requis)
go run ./cmd/example -source decp "GED"                  # montants + titulaires
```

`cmd/example` est un CLI de démonstration : mots-clés en arguments, flags
`-source` (`all`/`beauamp`/`decp`/`boamp`), `-limit`, `-all` (ET).

## Licence

MIT — voir [LICENSE](LICENSE).
