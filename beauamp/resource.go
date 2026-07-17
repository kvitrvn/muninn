package beauamp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/kvitrvn/muninn"
)

// resolveResources returns the tabular resource ids to query. When resources
// were pinned with WithResources they are used as-is; otherwise the dataset
// catalog is fetched and its monthly CSV files are selected: those overlapping
// [q.DateFrom, q.DateTo] when a range is given, or the single most recent month
// by default (the "light" access mode — recent data without pulling history).
func (c *Client) resolveResources(ctx context.Context, q muninn.Query) ([]string, error) {
	if len(c.resources) > 0 {
		return c.resources, nil
	}

	months, err := c.listMonthlyResources(ctx)
	if err != nil {
		return nil, err
	}
	if len(months) == 0 {
		return nil, fmt.Errorf("beauamp: no monthly resource found in dataset %q", c.slug)
	}

	// Most recent first.
	sort.Slice(months, func(i, j int) bool {
		if months[i].year != months[j].year {
			return months[i].year > months[j].year
		}
		return months[i].month > months[j].month
	})

	// No date range: default to the latest month only.
	if q.DateFrom.IsZero() && q.DateTo.IsZero() {
		return []string{months[0].id}, nil
	}

	var ids []string
	for _, m := range months {
		if q.DateFrom.IsZero() || !m.before(q.DateFrom.Year(), int(q.DateFrom.Month())) {
			if q.DateTo.IsZero() || !m.after(q.DateTo.Year(), int(q.DateTo.Month())) {
				ids = append(ids, m.id)
			}
		}
	}
	if len(ids) == 0 {
		ids = []string{months[0].id}
	}
	return ids, nil
}

// monthlyResource is a catalog resource identified as a BEAUAMP monthly file.
type monthlyResource struct {
	id    string
	year  int
	month int
}

func (m monthlyResource) before(year, month int) bool {
	return m.year < year || (m.year == year && m.month < month)
}

func (m monthlyResource) after(year, month int) bool {
	return m.year > year || (m.year == year && m.month > month)
}

// catalogResponse is the subset of the data.gouv.fr dataset API we read.
type catalogResponse struct {
	Resources []struct {
		ID     string `json:"id"`
		Title  string `json:"title"`
		Format string `json:"format"`
	} `json:"resources"`
}

// listMonthlyResources fetches the dataset catalog and keeps the monthly CSV
// resources (titles like "beauamp_juin_2026_1.1.0.csv").
func (c *Client) listMonthlyResources(ctx context.Context) ([]monthlyResource, error) {
	reqURL := c.catalogBase + c.slug + "/"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("beauamp: build catalog request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("beauamp: catalog request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("beauamp: unexpected catalog status %d", resp.StatusCode)
	}
	var cat catalogResponse
	if err := json.NewDecoder(resp.Body).Decode(&cat); err != nil {
		return nil, fmt.Errorf("beauamp: decode catalog: %w", err)
	}

	var months []monthlyResource
	for _, r := range cat.Resources {
		if !strings.EqualFold(r.Format, "csv") {
			continue
		}
		if year, month, ok := parseMonthlyTitle(r.Title); ok {
			months = append(months, monthlyResource{id: r.ID, year: year, month: month})
		}
	}
	return months, nil
}

// frenchMonths maps the lowercased French month names used in resource titles.
var frenchMonths = map[string]int{
	"janvier": 1, "fevrier": 2, "février": 2, "mars": 3, "avril": 4,
	"mai": 5, "juin": 6, "juillet": 7, "aout": 8, "août": 8,
	"septembre": 9, "octobre": 10, "novembre": 11, "decembre": 12, "décembre": 12,
}

// parseMonthlyTitle extracts the year and month from a monthly resource title
// such as "beauamp_juin_2026_1.1.0.csv". It rejects daily ("beauamp-16-07-2026")
// and yearly ("beauamp_2025_1.1.0") titles.
func parseMonthlyTitle(title string) (year, month int, ok bool) {
	parts := strings.Split(title, "_")
	if len(parts) < 3 {
		return 0, 0, false
	}
	month, ok = frenchMonths[strings.ToLower(parts[1])]
	if !ok {
		return 0, 0, false
	}
	year, err := strconv.Atoi(parts[2])
	if err != nil {
		return 0, 0, false
	}
	return year, month, true
}
