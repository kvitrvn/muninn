package boamp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"unicode"

	"github.com/kvitrvn/muninn"
	"github.com/kvitrvn/muninn/internal/httpx"
	"github.com/kvitrvn/muninn/internal/ods"
)

const companySearchURL = "https://recherche-entreprises.api.gouv.fr/search"

func supplierIdentityClause(siren string, names []string) string {
	values := make([]string, 0, len(names)+1)
	if siren = strings.TrimSpace(siren); siren != "" {
		values = append(values, siren)
	}
	values = append(values, names...)

	seen := map[string]bool{}
	parts := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		key := strings.ToLower(value)
		if value == "" || seen[key] {
			continue
		}
		seen[key] = true
		parts = append(parts, fmt.Sprintf(`"%s"`, ods.Escape(value)))
	}
	if len(parts) == 0 {
		return ""
	}
	return "(" + strings.Join(parts, " OR ") + ")"
}

type companySearchResponse struct {
	Results []struct {
		SIREN            string `json:"siren"`
		NomComplet       string `json:"nom_complet"`
		NomRaisonSociale string `json:"nom_raison_sociale"`
		Sigle            string `json:"sigle"`
		Siege            struct {
			NomCommercial  string   `json:"nom_commercial"`
			ListeEnseignes []string `json:"liste_enseignes"`
		} `json:"siege"`
	} `json:"results"`
}

func (c *Client) resolveSupplierNames(ctx context.Context, siren string) ([]string, error) {
	params := url.Values{}
	params.Set("q", siren)
	params.Set("per_page", "10")
	requestURL := companySearchURL + "?" + params.Encode()

	resp, err := httpx.Do(ctx, c.ods.HTTP, c.ods.Retry, func(ctx context.Context) (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	})
	if err != nil {
		return nil, fmt.Errorf("boamp: resolve supplier %s: %w", siren, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf(
			"boamp: resolve supplier %s: unexpected status %d: %s",
			siren,
			resp.StatusCode,
			body,
		)
	}

	var parsed companySearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("boamp: resolve supplier %s: decode response: %w", siren, err)
	}

	names := []string{}
	seen := map[string]bool{}
	for _, result := range parsed.Results {
		if result.SIREN != siren {
			continue
		}
		candidates := []string{
			result.NomComplet,
			result.NomRaisonSociale,
			result.Sigle,
			result.Siege.NomCommercial,
		}
		candidates = append(candidates, result.Siege.ListeEnseignes...)
		for _, name := range candidates {
			name = strings.TrimSpace(name)
			key := companyNameKey(name)
			if name == "" || key == "" || seen[key] {
				continue
			}
			seen[key] = true
			names = append(names, name)
		}
	}
	return names, nil
}

func mapTopLevelSuppliers(value any) []muninn.Buyer {
	suppliers := []muninn.Buyer{}
	switch typed := value.(type) {
	case string:
		suppliers = appendSupplier(suppliers, muninn.Buyer{Nom: strings.TrimSpace(typed)})
	case []string:
		for _, name := range typed {
			suppliers = appendSupplier(suppliers, muninn.Buyer{Nom: strings.TrimSpace(name)})
		}
	case []any:
		for _, item := range typed {
			if name, ok := item.(string); ok {
				suppliers = appendSupplier(suppliers, muninn.Buyer{Nom: strings.TrimSpace(name)})
			}
		}
	}
	return suppliers
}

func mapNestedSuppliers(nested map[string]any) []muninn.Buyer {
	suppliers := mapLegacySuppliers(nested)
	return mergeSupplierLists(suppliers, mapEFormsSuppliers(nested))
}

func mapLegacySuppliers(nested map[string]any) []muninn.Buyer {
	suppliers := []muninn.Buyer{}
	for _, value := range findLocalValues(nested, "TITULAIRE") {
		for _, record := range objectRecords(value) {
			supplier := muninn.Buyer{
				Nom:   textValue(digLocal(record, "DENOMINATION")),
				Ville: textValue(digLocal(record, "VILLE")),
			}
			setSupplierIdentifier(
				&supplier,
				firstText(
					digLocal(record, "SIRET"),
					digLocal(record, "SIREN"),
					digLocal(record, "CODE_IDENT_NATIONAL"),
				),
			)
			suppliers = appendSupplier(suppliers, supplier)
		}
	}
	return suppliers
}

func mapEFormsSuppliers(nested map[string]any) []muninn.Buyer {
	winners := map[string]bool{}
	for _, value := range findLocalValues(nested, "Tenderer") {
		for _, record := range objectRecords(value) {
			if id := textValue(digLocal(record, "ID")); id != "" {
				winners[id] = true
			}
		}
	}
	if len(winners) == 0 {
		return []muninn.Buyer{}
	}

	suppliers := []muninn.Buyer{}
	for _, value := range findLocalValues(nested, "Organization") {
		for _, organization := range objectRecords(value) {
			company, _ := digLocal(organization, "Company").(map[string]any)
			if company == nil {
				continue
			}
			partyIdentification, _ := digLocal(company, "PartyIdentification").(map[string]any)
			if !winners[textValue(digLocal(partyIdentification, "ID"))] {
				continue
			}

			partyName, _ := digLocal(company, "PartyName").(map[string]any)
			postalAddress, _ := digLocal(company, "PostalAddress").(map[string]any)
			legalEntity, _ := digLocal(company, "PartyLegalEntity").(map[string]any)
			supplier := muninn.Buyer{
				Nom:   textValue(digLocal(partyName, "Name")),
				Ville: textValue(digLocal(postalAddress, "CityName")),
			}
			setSupplierIdentifier(&supplier, textValue(digLocal(legalEntity, "CompanyID")))
			suppliers = appendSupplier(suppliers, supplier)
		}
	}
	return suppliers
}

func setSupplierIdentifier(supplier *muninn.Buyer, identifier string) {
	identifier = strings.TrimSpace(identifier)
	switch {
	case isASCIIDigits(identifier, 14):
		supplier.SIRET = identifier
	case isASCIIDigits(identifier, 9):
		supplier.SIREN = identifier
	}
}

func identifySuppliers(suppliers []muninn.Buyer, siren string, names []string) []muninn.Buyer {
	aliases := map[string]bool{}
	for _, name := range names {
		if key := companyNameKey(name); key != "" {
			aliases[key] = true
		}
	}
	for index := range suppliers {
		if suppliers[index].SIREN9() == siren {
			continue
		}
		if suppliers[index].SIREN9() != "" || !aliases[companyNameKey(suppliers[index].Nom)] {
			continue
		}
		suppliers[index].SIREN = siren
	}
	return suppliers
}

func mergeSupplierLists(lists ...[]muninn.Buyer) []muninn.Buyer {
	merged := []muninn.Buyer{}
	for _, suppliers := range lists {
		for _, supplier := range suppliers {
			merged = appendSupplier(merged, supplier)
		}
	}
	return merged
}

func appendSupplier(suppliers []muninn.Buyer, supplier muninn.Buyer) []muninn.Buyer {
	if supplier.Nom == "" && supplier.SIREN9() == "" {
		return suppliers
	}
	for index := range suppliers {
		sameSIRET := supplier.SIRET != "" && suppliers[index].SIRET == supplier.SIRET
		sameSIREN := supplier.SIREN9() != "" && suppliers[index].SIREN9() == supplier.SIREN9()
		sameName := companyNameKey(supplier.Nom) != "" &&
			companyNameKey(suppliers[index].Nom) == companyNameKey(supplier.Nom)
		if !sameSIRET && !sameSIREN && !sameName {
			continue
		}
		if suppliers[index].Nom == "" {
			suppliers[index].Nom = supplier.Nom
		}
		if suppliers[index].SIRET == "" {
			suppliers[index].SIRET = supplier.SIRET
		}
		if suppliers[index].SIREN == "" {
			suppliers[index].SIREN = supplier.SIREN
		}
		if suppliers[index].Ville == "" {
			suppliers[index].Ville = supplier.Ville
		}
		return suppliers
	}
	return append(suppliers, supplier)
}

var legalFormTokens = map[string]bool{
	"eirl":    true,
	"eurl":    true,
	"sa":      true,
	"sarl":    true,
	"sas":     true,
	"sasu":    true,
	"sc":      true,
	"sci":     true,
	"scop":    true,
	"societe": true,
}

func companyNameKey(name string) string {
	var normalized strings.Builder
	for _, r := range strings.ToLower(name) {
		r = foldNameRune(r)
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			normalized.WriteRune(r)
			continue
		}
		normalized.WriteByte(' ')
	}
	fields := strings.Fields(normalized.String())
	kept := fields[:0]
	for _, field := range fields {
		if !legalFormTokens[field] {
			kept = append(kept, field)
		}
	}
	return strings.Join(kept, " ")
}

func foldNameRune(r rune) rune {
	switch r {
	case 'à', 'á', 'â', 'ä', 'ã', 'å':
		return 'a'
	case 'ç':
		return 'c'
	case 'è', 'é', 'ê', 'ë':
		return 'e'
	case 'ì', 'í', 'î', 'ï':
		return 'i'
	case 'ñ':
		return 'n'
	case 'ò', 'ó', 'ô', 'ö', 'õ':
		return 'o'
	case 'ù', 'ú', 'û', 'ü':
		return 'u'
	case 'ý', 'ÿ':
		return 'y'
	default:
		return r
	}
}

func findLocalValues(value any, wanted string) []any {
	values := []any{}
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if strings.EqualFold(localName(key), wanted) {
				values = append(values, child)
			}
			values = append(values, findLocalValues(child, wanted)...)
		}
	case []any:
		for _, child := range typed {
			values = append(values, findLocalValues(child, wanted)...)
		}
	}
	return values
}

func digLocal(record map[string]any, wanted string) any {
	for key, value := range record {
		if strings.EqualFold(localName(key), wanted) {
			return value
		}
	}
	return nil
}

func localName(key string) string {
	if index := strings.LastIndexByte(key, ':'); index >= 0 {
		return key[index+1:]
	}
	return key
}

func objectRecords(value any) []map[string]any {
	switch typed := value.(type) {
	case map[string]any:
		return []map[string]any{typed}
	case []any:
		records := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			if record, ok := item.(map[string]any); ok {
				records = append(records, record)
			}
		}
		return records
	default:
		return nil
	}
}

func firstText(values ...any) string {
	for _, value := range values {
		if text := textValue(value); text != "" {
			return text
		}
	}
	return ""
}

func textValue(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case map[string]any:
		if text, ok := typed["#text"]; ok {
			return textValue(text)
		}
	}
	return ""
}

func isASCIIDigits(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}
