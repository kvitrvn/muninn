package boamp

import (
	"strings"

	"github.com/kvitrvn/muninn"
)

// enrichBuyerIdentifier maps the national identifier of the contracting
// authority from each BOAMP nested format. Recent eForms notices reference the
// buyer through an ORG-* identifier, so an arbitrary organization carrying a
// SIRET must never be used as a fallback.
func enrichBuyerIdentifier(buyer *muninn.Buyer, nested map[string]any) {
	if buyer.SIREN9() != "" {
		return
	}

	if id := digDict(nested, "ORGANISME", "ACHETEUR", "IDENTIFICATION"); id != nil {
		setLegalEntityIdentifier(
			buyer,
			firstNationalIdentifier(digLocal(id, "SIRET"), digLocal(id, "SIREN")),
		)
	}
	if buyer.SIREN9() != "" {
		return
	}

	setLegalEntityIdentifier(buyer, fnSimpleBuyerIdentifier(nested))
	if buyer.SIREN9() != "" {
		return
	}

	setLegalEntityIdentifier(buyer, eFormsBuyerIdentifier(nested, buyer.Nom))
}

func fnSimpleBuyerIdentifier(nested map[string]any) string {
	for _, document := range objectRecords(digLocal(nested, "FNSimple")) {
		for _, organization := range objectRecords(digLocal(document, "organisme")) {
			if identifier := textValue(digLocal(organization, "codeIdentificationNational")); validNationalIdentifier(identifier) {
				return identifier
			}
		}
	}
	return ""
}

func eFormsBuyerIdentifier(nested map[string]any, buyerName string) string {
	eForms := digLocal(nested, "EFORMS")
	if eForms == nil {
		return ""
	}

	buyerOrganizationIDs := map[string]bool{}
	for _, value := range findLocalValues(eForms, "ContractingParty") {
		for _, contractingParty := range objectRecords(value) {
			for _, party := range objectRecords(digLocal(contractingParty, "Party")) {
				for _, identification := range objectRecords(digLocal(party, "PartyIdentification")) {
					if id := textValue(digLocal(identification, "ID")); id != "" {
						buyerOrganizationIDs[id] = true
					}
				}
			}
		}
	}
	if len(buyerOrganizationIDs) == 0 {
		return ""
	}

	type candidate struct {
		name       string
		identifier string
	}
	candidates := []candidate{}
	for _, value := range findLocalValues(eForms, "Organization") {
		for _, organization := range objectRecords(value) {
			company, _ := digLocal(organization, "Company").(map[string]any)
			if company == nil || !buyerOrganizationIDs[companyOrganizationID(company)] {
				continue
			}

			identifier := companyNationalIdentifier(company)
			if identifier == "" {
				continue
			}
			partyName, _ := digLocal(company, "PartyName").(map[string]any)
			candidates = append(candidates, candidate{
				name:       textValue(digLocal(partyName, "Name")),
				identifier: identifier,
			})
		}
	}

	wantedName := companyNameKey(buyerName)
	if wantedName != "" {
		for _, candidate := range candidates {
			if companyNameKey(candidate.name) == wantedName {
				return candidate.identifier
			}
		}
	}
	if len(candidates) > 0 {
		return candidates[0].identifier
	}
	return ""
}

func companyOrganizationID(company map[string]any) string {
	for _, identification := range objectRecords(digLocal(company, "PartyIdentification")) {
		if id := textValue(digLocal(identification, "ID")); id != "" {
			return id
		}
	}
	return ""
}

func companyNationalIdentifier(company map[string]any) string {
	for _, legalEntity := range objectRecords(digLocal(company, "PartyLegalEntity")) {
		identifier := textValue(digLocal(legalEntity, "CompanyID"))
		if validNationalIdentifier(identifier) {
			return identifier
		}
	}
	return ""
}

func setLegalEntityIdentifier(entity *muninn.Buyer, identifier string) {
	identifier = strings.TrimSpace(identifier)
	switch {
	case isASCIIDigits(identifier, 14):
		entity.SIRET = identifier
	case isASCIIDigits(identifier, 9):
		entity.SIREN = identifier
	}
}

func validNationalIdentifier(identifier string) bool {
	identifier = strings.TrimSpace(identifier)
	return isASCIIDigits(identifier, 9) || isASCIIDigits(identifier, 14)
}

func firstNationalIdentifier(values ...any) string {
	for _, value := range values {
		identifier := textValue(value)
		if validNationalIdentifier(identifier) {
			return identifier
		}
	}
	return ""
}
