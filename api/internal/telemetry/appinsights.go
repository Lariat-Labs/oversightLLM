// Package telemetry sends spans to Application Insights through its ingestion
// ("track") endpoint, shaped like OpenTelemetry GenAI spans so they land in
// AppDependencies next to the agents' own spans.
package telemetry

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Lariat-Labs/oversightLLM/api/internal/azauth"
	"github.com/Lariat-Labs/oversightLLM/api/internal/metrics"
)

// Span is one finished operation.
type Span struct {
	TraceID    string // 32 hex chars; becomes OperationId
	ID         string // 16 hex chars
	ParentID   string
	Name       string
	Start      time.Time
	Duration   time.Duration
	Success    bool
	Attributes map[string]string
}

// Client batches spans and posts them to the ingestion endpoint in the background.
// A nil *Client is valid and drops everything, so callers need no checks.
type Client struct {
	ikey     string
	endpoint string
	role     string
	auth     *azauth.CLI
	http     *http.Client
	mu       sync.Mutex
	buf      []Span
	wake     chan struct{}
}

// New parses an App Insights connection string. It returns nil (tracing off) when cs is empty.
func New(cs, role string, auth *azauth.CLI) (*Client, error) {
	if strings.TrimSpace(cs) == "" {
		return nil, nil
	}
	parts := map[string]string{}
	for _, kv := range strings.Split(cs, ";") {
		if k, v, ok := strings.Cut(kv, "="); ok {
			parts[strings.ToLower(strings.TrimSpace(k))] = strings.TrimSpace(v)
		}
	}
	ikey := parts["instrumentationkey"]
	if ikey == "" {
		return nil, fmt.Errorf("APPLICATIONINSIGHTS_CONNECTION_STRING has no InstrumentationKey")
	}
	ep := strings.TrimRight(parts["ingestionendpoint"], "/")
	if ep == "" {
		ep = "https://dc.services.visualstudio.com"
	}
	return &Client{ikey: ikey, endpoint: ep + "/v2.1/track", role: role, auth: auth,
		http: &http.Client{Timeout: 20 * time.Second}, wake: make(chan struct{}, 1)}, nil
}

// Enabled reports whether spans are being sent.
func (c *Client) Enabled() bool { return c != nil }

// Record queues spans for sending.
func (c *Client) Record(spans ...Span) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.buf = append(c.buf, spans...)
	c.mu.Unlock()
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// Run flushes queued spans every few seconds until ctx ends, then flushes once more.
func (c *Client) Run(ctx context.Context) {
	if c == nil {
		return
	}
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			flush, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			c.flush(flush)
			cancel()
			return
		case <-t.C:
			c.flush(ctx)
		case <-c.wake:
			time.Sleep(500 * time.Millisecond) // let a burst of spans collect
			c.flush(ctx)
		}
	}
}

func (c *Client) flush(ctx context.Context) {
	c.mu.Lock()
	spans := c.buf
	c.buf = nil
	c.mu.Unlock()
	if len(spans) == 0 {
		return
	}
	var body bytes.Buffer
	for _, s := range spans {
		b, _ := json.Marshal(Envelope(s, c.ikey, c.role))
		body.Write(b)
		body.WriteByte('\n')
	}
	start := time.Now()
	status, msg, err := c.post(ctx, body.Bytes(), false)
	if err == nil && (status == http.StatusUnauthorized || status == http.StatusForbidden) {
		// Resource has local auth disabled: retry with an Entra token from the Azure CLI.
		status, msg, err = c.post(ctx, body.Bytes(), true)
	}
	failed := err != nil || status >= 300
	metrics.UpstreamDuration.Since(start, "service", "appinsights")
	metrics.UpstreamRequests.Inc("service", "appinsights", "outcome", map[bool]string{true: "error", false: "ok"}[failed])
	metrics.TraceExports.Add(float64(len(spans)), "outcome", map[bool]string{true: "error", false: "ok"}[failed])
	if failed {
		slog.Warn("app insights export failed", "spans", len(spans), "status", status, "err", err, "body", msg)
	}
}

func (c *Client) post(ctx context.Context, body []byte, withToken bool) (int, string, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/x-json-stream")
	if withToken {
		tok, err := c.auth.Token(ctx, "https://monitor.azure.com")
		if err != nil {
			return 0, "", err
		}
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 2000))
	return resp.StatusCode, string(b), nil
}

// Envelope builds the ingestion payload for a span as an in-process dependency,
// which App Insights stores in AppDependencies with Properties = attributes.
func Envelope(s Span, ikey, role string) map[string]any {
	tags := map[string]string{
		"ai.operation.id":   s.TraceID,
		"ai.cloud.role":     role,
		"ai.operation.name": s.Name,
	}
	if s.ParentID != "" {
		tags["ai.operation.parentId"] = s.ParentID
	}
	return map[string]any{
		"name": "Microsoft.ApplicationInsights." + strings.ReplaceAll(ikey, "-", "") + ".RemoteDependency",
		"time": s.Start.UTC().Format(time.RFC3339Nano),
		"iKey": ikey,
		"tags": tags,
		"data": map[string]any{
			"baseType": "RemoteDependencyData",
			"baseData": map[string]any{
				"ver":        2,
				"id":         s.ID,
				"name":       s.Name,
				"type":       "InProc",
				"duration":   formatDuration(s.Duration),
				"success":    s.Success,
				"resultCode": map[bool]string{true: "0", false: "1"}[s.Success],
				"properties": s.Attributes,
			},
		},
	}
}

// formatDuration renders d.hh:mm:ss.ffffff as App Insights expects.
func formatDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	days := int(d / (24 * time.Hour))
	d -= time.Duration(days) * 24 * time.Hour
	h := int(d / time.Hour)
	d -= time.Duration(h) * time.Hour
	m := int(d / time.Minute)
	d -= time.Duration(m) * time.Minute
	sec := d.Seconds()
	return fmt.Sprintf("%d.%02d:%02d:%09.6f", days, h, m, sec)
}

// NewTraceID and NewSpanID return W3C-style random ids.
func NewTraceID() string { return randHex(16) }
func NewSpanID() string  { return randHex(8) }

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
