package muninn

import (
	"sort"
	"strconv"
	"strings"
	"time"
)

const maxCrossSourceDateGap = 180 * 24 * time.Hour

// MergeTenders deduplicates records and enriches cross-source matches.
//
// Records from different providers are merged only when buyer SIREN,
// normalized object and at least one CPV root agree, their publication dates
// are no more than 180 days apart, and either the winning supplier or a native
// notice ID agrees. Missing evidence prevents a merge.
func MergeTenders(tenders []Tender) []Tender {
	type group struct {
		merged  Tender
		members []Tender
	}

	var groups []group
	byRecord := map[string]int{}
	byContract := map[string][]int{}

	for _, tender := range tenders {
		tender.Sources = normalizedSources(tender.Sources)
		tender.Suppliers = normalizedSuppliers(tender.Suppliers)
		tender.Lots = normalizedLots(tender.Lots)

		target := -1
		for _, key := range recordKeys(tender) {
			if index, ok := byRecord[key]; ok {
				target = index
				break
			}
		}

		contractKey := contractCandidateKey(tender)
		if target < 0 && contractKey != "" {
			for _, index := range byContract[contractKey] {
				if compatibleWithGroup(tender, groups[index].members) {
					target = index
					break
				}
			}
		}

		if target < 0 {
			target = len(groups)
			groups = append(groups, group{merged: tender, members: []Tender{tender}})
			if contractKey != "" {
				byContract[contractKey] = append(byContract[contractKey], target)
			}
		} else {
			groups[target].merged = enrichTender(groups[target].merged, tender)
			groups[target].members = append(groups[target].members, tender)
		}

		for _, key := range recordKeys(tender) {
			byRecord[key] = target
		}
	}

	out := make([]Tender, 0, len(groups))
	for _, group := range groups {
		group.merged.Sources = normalizedSources(group.merged.Sources)
		group.merged.Suppliers = normalizedSuppliers(group.merged.Suppliers)
		group.merged.Lots = normalizedLots(group.merged.Lots)
		out = append(out, group.merged)
	}
	return out
}

func compatibleWithGroup(tender Tender, members []Tender) bool {
	for _, member := range members {
		if sameContract(tender, member) {
			return true
		}
	}
	return false
}

func sameContract(a, b Tender) bool {
	if a.primaryProvider() == b.primaryProvider() {
		return false
	}
	if contractCandidateKey(a) == "" || contractCandidateKey(a) != contractCandidateKey(b) {
		return false
	}
	if !cpvOverlap(a.CPVCodes, b.CPVCodes) {
		return false
	}
	if !hasCrossSourceEvidence(a, b) {
		return false
	}
	if a.DatePublication.IsZero() || b.DatePublication.IsZero() {
		return false
	}
	gap := a.DatePublication.Sub(b.DatePublication)
	if gap < 0 {
		gap = -gap
	}
	return gap <= maxCrossSourceDateGap
}

func recordKeys(t Tender) []string {
	var keys []string
	for _, source := range t.Sources {
		if source.Provider != "" && source.ID != "" {
			keys = append(keys, source.Provider+"|"+source.ID)
		}
	}
	return keys
}

func contractCandidateKey(t Tender) string {
	siren := t.Buyer.SIREN9()
	object := normalizeObject(firstNonEmpty(t.Objet, t.Titre))
	if siren == "" || object == "" || len(t.CPVCodes) == 0 {
		return ""
	}
	return siren + "|" + object
}

func cpvOverlap(a, b []string) bool {
	seen := map[string]bool{}
	for _, value := range a {
		if root := cpvRoot(value); root != "" {
			seen[root] = true
		}
	}
	for _, value := range b {
		if seen[cpvRoot(value)] {
			return true
		}
	}
	return false
}

func hasCrossSourceEvidence(a, b Tender) bool {
	aSuppliers := supplierSIRENs(a.Suppliers)
	bSuppliers := supplierSIRENs(b.Suppliers)
	if len(aSuppliers) > 0 && len(bSuppliers) > 0 {
		for siren := range aSuppliers {
			if bSuppliers[siren] {
				return true
			}
		}
		return false
	}
	for _, aSource := range a.Sources {
		if aSource.ID == "" {
			continue
		}
		for _, bSource := range b.Sources {
			if aSource.Provider != bSource.Provider && aSource.ID == bSource.ID {
				return true
			}
		}
	}
	return false
}

func enrichTender(a, b Tender) Tender {
	aHasDECP := hasProvider(a, "decp")
	bHasDECP := hasProvider(b, "decp")
	switch {
	case bHasDECP && b.MontantEstime > 0:
		a.MontantEstime = b.MontantEstime
	case !aHasDECP && a.MontantEstime == 0:
		a.MontantEstime = b.MontantEstime
	}

	a.Buyer = mergeBuyer(a.Buyer, b.Buyer)
	a.Suppliers = mergeSuppliers(a.Suppliers, b.Suppliers)
	if a.Titre == "" {
		a.Titre = b.Titre
	}
	if a.Objet == "" {
		a.Objet = b.Objet
	}
	if a.DatePublication.IsZero() || (!b.DatePublication.IsZero() && b.DatePublication.Before(a.DatePublication)) {
		a.DatePublication = b.DatePublication
	}
	if a.DateLimiteReponse.IsZero() {
		a.DateLimiteReponse = b.DateLimiteReponse
	}
	if a.Procedure == ProcedureInconnue {
		a.Procedure = b.Procedure
	}
	if a.Engagement == EngagementInconnu {
		a.Engagement = b.Engagement
	}
	if a.AvisType == AvisInconnu || b.AvisType == AvisAttribution {
		if b.AvisType != AvisInconnu {
			a.AvisType = b.AvisType
		}
	}
	a.CPVCodes = unionStrings(a.CPVCodes, b.CPVCodes)
	a.Sources = append(a.Sources, b.Sources...)
	a.Lots = append(a.Lots, b.Lots...)
	return a
}

func mergeBuyer(a, b Buyer) Buyer {
	if a.Nom == "" {
		a.Nom = b.Nom
	}
	if a.SIREN == "" {
		a.SIREN = b.SIREN
	}
	if a.SIRET == "" {
		a.SIRET = b.SIRET
	}
	if a.Ville == "" {
		a.Ville = b.Ville
	}
	if a.CodeDepartement == "" {
		a.CodeDepartement = b.CodeDepartement
	}
	return a
}

func mergeSuppliers(a, b []Buyer) []Buyer {
	combined := make([]Buyer, 0, len(a)+len(b))
	combined = append(combined, a...)
	combined = append(combined, b...)
	return normalizedSuppliers(combined)
}

func normalizedSuppliers(suppliers []Buyer) []Buyer {
	candidates := make([]Buyer, 0, len(suppliers))
	for _, supplier := range suppliers {
		supplier.SIRET = strings.TrimSpace(supplier.SIRET)
		supplier.SIREN = strings.TrimSpace(supplier.SIREN)
		if !isZeroBuyer(supplier) {
			candidates = append(candidates, supplier)
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return supplierSortKey(candidates[i]) < supplierSortKey(candidates[j])
	})

	merged := make([]Buyer, 0, len(candidates))
	bySIRET := map[string]int{}
	sirenOnly := map[string]Buyer{}
	anonymous := map[string]bool{}
	for _, supplier := range candidates {
		if supplier.SIRET != "" {
			if index, ok := bySIRET[supplier.SIRET]; ok {
				merged[index] = mergeBuyer(merged[index], supplier)
				continue
			}
			bySIRET[supplier.SIRET] = len(merged)
			merged = append(merged, supplier)
			continue
		}
		if siren := supplier.SIREN9(); siren != "" {
			sirenOnly[siren] = mergeBuyer(sirenOnly[siren], supplier)
			continue
		}
		key := supplierSortKey(supplier)
		if !anonymous[key] {
			anonymous[key] = true
			merged = append(merged, supplier)
		}
	}

	sirens := make([]string, 0, len(sirenOnly))
	for siren := range sirenOnly {
		sirens = append(sirens, siren)
	}
	sort.Strings(sirens)
	for _, siren := range sirens {
		details := sirenOnly[siren]
		matched := false
		for index := range merged {
			if merged[index].SIRET != "" && merged[index].SIREN9() == siren {
				merged[index] = mergeBuyer(merged[index], details)
				matched = true
			}
		}
		if !matched {
			merged = append(merged, details)
		}
	}

	sort.SliceStable(merged, func(i, j int) bool {
		return supplierSortKey(merged[i]) < supplierSortKey(merged[j])
	})
	return merged
}

func supplierSIRENs(suppliers []Buyer) map[string]bool {
	sirens := map[string]bool{}
	for _, supplier := range suppliers {
		if siren := supplier.SIREN9(); siren != "" {
			sirens[siren] = true
		}
	}
	return sirens
}

func supplierSortKey(supplier Buyer) string {
	return strings.Join([]string{
		supplier.SIREN9(),
		supplier.SIRET,
		supplier.SIREN,
		supplier.Nom,
		supplier.Ville,
		supplier.CodeDepartement,
	}, "|")
}

func unionStrings(a, b []string) []string {
	seen := map[string]bool{}
	for _, value := range a {
		seen[value] = true
	}
	for _, value := range b {
		if !seen[value] {
			seen[value] = true
			a = append(a, value)
		}
	}
	return a
}

func normalizedSources(sources []SourceReference) []SourceReference {
	byKey := map[string]SourceReference{}
	var order []string
	for _, source := range sources {
		key := source.Provider + "|" + source.ID + "|" + source.RecordID
		if _, exists := byKey[key]; !exists {
			order = append(order, key)
			byKey[key] = source
			continue
		}
		current := byKey[key]
		if current.URL == "" {
			current.URL = source.URL
		}
		if current.RawFields == nil {
			current.RawFields = source.RawFields
		}
		current.RelatedIDs = unionStrings(current.RelatedIDs, source.RelatedIDs)
		byKey[key] = current
	}
	sort.Strings(order)
	out := make([]SourceReference, 0, len(order))
	for _, key := range order {
		out = append(out, byKey[key])
	}
	return out
}

func normalizedLots(lots []TenderLot) []TenderLot {
	if len(lots) == 0 {
		return nil
	}
	byID := map[string]TenderLot{}
	var order []string
	for index, lot := range lots {
		id := strings.TrimSpace(lot.ID)
		if id == "" {
			id = "\x00" + strings.Join([]string{
				lot.Objet,
				cpvRootSet(lot.CPVCodes),
				strconv.Itoa(index),
			}, "|")
		}
		current, exists := byID[id]
		if !exists {
			lot.Suppliers = normalizedSuppliers(lot.Suppliers)
			lot.Sources = normalizedSources(lot.Sources)
			lot.CPVCodes = unionStrings(nil, lot.CPVCodes)
			byID[id] = lot
			order = append(order, id)
			continue
		}
		if current.Objet == "" {
			current.Objet = lot.Objet
		}
		if current.MontantEstime == 0 {
			current.MontantEstime = lot.MontantEstime
		}
		current.CPVCodes = unionStrings(current.CPVCodes, lot.CPVCodes)
		current.Suppliers = normalizedSuppliers(append(current.Suppliers, lot.Suppliers...))
		current.Sources = normalizedSources(append(current.Sources, lot.Sources...))
		byID[id] = current
	}
	sort.Strings(order)
	out := make([]TenderLot, 0, len(order))
	for _, id := range order {
		out = append(out, byID[id])
	}
	return out
}

func hasProvider(t Tender, provider string) bool {
	for _, source := range t.Sources {
		if strings.EqualFold(source.Provider, provider) {
			return true
		}
	}
	return false
}
