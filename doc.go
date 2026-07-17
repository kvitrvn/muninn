// Package muninn is a Go library to search French public procurement notices
// and normalize them into a common model, whatever the source.
//
// # Model
//
// The root package defines the shared types — [Tender], [Buyer], the enums
// ([ProcedureType], [EngagementType], [AvisType]) — along with the [Provider]
// contract, the [Query] request and the [ErrTruncated] pagination signal. Each
// data source implements [Provider] and maps its native format to [Tender].
//
// # Sources
//
// Three sources cover complementary phases of a contract's life:
//
//   - [github.com/kvitrvn/muninn/boamp] — BOAMP, the official bulletin of
//     procurement announcements (the live, authoritative tendering notices).
//   - [github.com/kvitrvn/muninn/beauamp] — BEAUAMP, BOAMP consolidated into a
//     structured, SIRENE-matched form with award results (buyer and supplier
//     names, indicative amounts). The pragmatic backbone for consolidation.
//   - [github.com/kvitrvn/muninn/decp] — DECP, the essential data of awarded
//     contracts (≥ 40,000 € HT): the reference for who won and for how much.
//
// # Consolidation
//
// [github.com/kvitrvn/muninn/consolidate] combines several providers, keying on
// buyer SIREN and CPV (see [Tender.DedupKey]) to deduplicate a notice seen in
// several sources and to enrich it — typically taking the human-readable parties
// from BEAUAMP and the authoritative amount from DECP.
//
// # Example
//
//	c := consolidate.New(beauamp.New(), decp.New())
//	tenders, _ := c.Search(ctx, muninn.Query{Keywords: []string{"GED"}, ObjetOnly: true})
//	for _, t := range tenders {
//		fmt.Printf("%s → %s (%.0f €)\n", t.Buyer.Nom, t.Supplier.Nom, t.MontantEstime)
//	}
//
// # Caveats
//
// BEAUAMP data is declared "à valeur indicative" (the BOAMP notice remains
// authoritative) and is queried per period through the data.gouv.fr tabular API,
// which does not serve deep-history files. DECP only covers contracts of
// 40,000 € HT or more. The BOAMP procedure/engagement mapping read from the
// nested "donnees" field is heuristic (eForms format since 2024). See each
// subpackage doc.
package muninn
