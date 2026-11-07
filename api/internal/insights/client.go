// Package insights queries Application Insights data in its Log Analytics workspace.
package insights

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/Lariat-Labs/oversightLLM/api/internal/azauth"
	"github.com/Lariat-Labs/oversightLLM/api/internal/metrics"
)

type Client struct {
	WorkspaceID string
	Auth        *azauth.CLI
	HTTP        *http.Client
}

type Row map[string]any

// Query runs KQL and returns the primary table as rows keyed by column name.
func (c *Client) Query(ctx context.Context, kql string, timespan time.Duration) (rows []Row, err error) {
	start := time.Now()
	defer func() {
		metrics.UpstreamRequests.Inc("service", "loganalytics", "outcome", metrics.Outcome(err))
		metrics.UpstreamDuration.Since(start, "service", "loganalytics")
	}()
	tok, err := c.Auth.Token(ctx, azauth.ScopeLogAnalytics)
	if err != nil {
		return nil, err
	}
	body, _ := json.Marshal(map[string]string{"query": kql, "timespan": isoDuration(timespan)})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.loganalytics.io/v1/workspaces/"+c.WorkspaceID+"/query", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("log analytics query: %s: %s", resp.Status, truncate(string(raw), 500))
	}
	return ParseResponse(raw)
}

// ParseResponse converts the Log Analytics JSON response into rows.
func ParseResponse(raw []byte) ([]Row, error) {
	var r struct {
		Tables []struct {
			Columns []struct {
				Name string `json:"name"`
			} `json:"columns"`
			Rows [][]any `json:"rows"`
		} `json:"tables"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, err
	}
	if len(r.Tables) == 0 {
		return nil, nil
	}
	t := r.Tables[0]
	rows := make([]Row, 0, len(t.Rows))
	for _, vals := range t.Rows {
		row := Row{}
		for i, col := range t.Columns {
			if i < len(vals) {
				row[col.Name] = vals[i]
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func isoDuration(d time.Duration) string {
	if d <= 0 {
		d = 24 * time.Hour
	}
	return fmt.Sprintf("PT%dS", int(d.Seconds()))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
