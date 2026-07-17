package boamp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

const datasetMetaURL = "https://boamp-datadila.opendatasoft.com/api/explore/v2.1/catalog/datasets/boamp"

// FieldInfo describes a dataset field as returned by the catalog API.
type FieldInfo struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Label string `json:"label"`
}

type datasetMeta struct {
	Fields []FieldInfo `json:"fields"`
}

// FetchSchema retrieves the real list of fields of the BOAMP dataset. Useful to
// verify the field names relied on by mapRecord, buildWhere, mapProcedure and
// mapEngagement in client.go before depending on them in production.
func (c *Client) FetchSchema(ctx context.Context) ([]FieldInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, datasetMetaURL, nil)
	if err != nil {
		return nil, fmt.Errorf("boamp: build schema request: %w", err)
	}
	resp, err := c.ods.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("boamp: schema request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("boamp: unexpected schema status %d", resp.StatusCode)
	}

	var meta datasetMeta
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		return nil, fmt.Errorf("boamp: decode schema: %w", err)
	}
	return meta.Fields, nil
}
