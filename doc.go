// Package muninn is a Go library to search French public procurement notices
// and normalize them into a common model, whatever the source.
//
// # Model
//
// The root package defines the shared types — [Tender], [Buyer], the enums
// ([ProcedureType], [EngagementType], [AvisType]) — along with the [Provider]
// contract and the [Query] request. Each data source implements [Provider] and
// maps its native format to [Tender].
//
// # Sources
//
// The only source implemented so far is BOAMP (Bulletin officiel des annonces
// des marchés publics), via the [github.com/kvitrvn/muninn/boamp] subpackage.
// Other sources (DECP, PLACE...) can be added by implementing [Provider].
//
// # Example
//
//	c := boamp.New()
//	n, _ := c.Count(ctx, muninn.Query{Keywords: []string{"GED"}, ObjetOnly: true})
//	fmt.Printf("%d tenders\n", n)
//
// # Mapping caveat
//
// The BOAMP field mapping in [github.com/kvitrvn/muninn/boamp] is partly
// heuristic (eForms format since 2024): top-level fields (objet, dates, buyer,
// department) are reliable, but the procedure and engagement type read from the
// nested "donnees" field still need confirmation on recent records. See the
// boamp subpackage doc.
package muninn
