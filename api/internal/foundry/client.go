// Package foundry talks to a Microsoft Foundry project: agents, datasets, evaluations
// (OpenAI-compatible Evals API) and chat completions for the judge model.
package foundry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Lariat-Labs/oversightLLM/api/internal/azauth"
	"github.com/Lariat-Labs/oversightLLM/api/internal/metrics"
)

type Client struct {
	ProjectEndpoint string // https://<res>.services.ai.azure.com/api/projects/<project>
	OpenAIEndpoint  string // https://<res>.openai.azure.com/openai/v1
	Auth            *azauth.CLI
	HTTP            *http.Client
}

type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string { return fmt.Sprintf("foundry: HTTP %d: %s", e.Status, e.Body) }

func (c *Client) do(ctx context.Context, scope, method, rawURL string, in, out any) (err error) {
	svc := "foundry"
	if scope == azauth.ScopeCognitive {
		svc = "openai"
	}
	start := time.Now()
	defer func() {
		metrics.UpstreamRequests.Inc("service", svc, "outcome", metrics.Outcome(err))
		metrics.UpstreamDuration.Since(start, "service", svc)
	}()
	tok, err := c.Auth.Token(ctx, scope)
	if err != nil {
		return err
	}
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		msg := string(raw)
		if len(msg) > 800 {
			msg = msg[:800]
		}
		return &APIError{resp.StatusCode, msg}
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

func (c *Client) project(path string, q url.Values) string {
	if q == nil {
		q = url.Values{}
	}
	if !strings.HasPrefix(path, "/openai/") && q.Get("api-version") == "" {
		q.Set("api-version", "v1")
	}
	u := c.ProjectEndpoint + path
	if enc := q.Encode(); enc != "" {
		u += "?" + enc
	}
	return u
}

// ---------- Agents ----------

type AgentVersion struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Version     string            `json:"version"`
	Description string            `json:"description"`
	CreatedAt   int64             `json:"created_at"`
	Status      string            `json:"status"`
	Metadata    map[string]string `json:"metadata"`
	Definition  map[string]any    `json:"definition"`
}

type Agent struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	State    string `json:"state"`
	Versions struct {
		Latest AgentVersion `json:"latest"`
	} `json:"versions"`
}

// Kind is the agent definition kind: prompt, hosted, workflow, external, …
func (a Agent) Kind() string {
	k, _ := a.Versions.Latest.Definition["kind"].(string)
	return k
}

// TraceID is the gen_ai.agent.id the agent's spans carry.
func (a Agent) TraceID() string {
	if v, ok := a.Versions.Latest.Definition["otel_agent_id"].(string); ok && v != "" {
		return v
	}
	if m := a.Versions.Latest.Metadata["trace_match"]; m != "" {
		if _, v, ok := strings.Cut(m, "="); ok {
			return strings.TrimSpace(v)
		}
	}
	return a.Name
}

func (c *Client) ListAgents(ctx context.Context) ([]Agent, error) {
	var all []Agent
	after := ""
	for page := 0; page < 20; page++ {
		q := url.Values{"limit": {"100"}}
		if after != "" {
			q.Set("after", after)
		}
		var r struct {
			Data    []Agent `json:"data"`
			HasMore bool    `json:"has_more"`
			LastID  string  `json:"last_id"`
		}
		if err := c.do(ctx, azauth.ScopeFoundry, http.MethodGet, c.project("/agents", q), nil, &r); err != nil {
			return nil, err
		}
		all = append(all, r.Data...)
		if !r.HasMore || r.LastID == "" {
			break
		}
		after = r.LastID
	}
	return all, nil
}

// RegisterExternalAgent creates a new version of an external agent: one Foundry
// lists and governs but doesn't run. Its spans are matched by otelAgentID.
func (c *Client) RegisterExternalAgent(ctx context.Context, name, otelAgentID, description string, metadata map[string]string) (*AgentVersion, error) {
	var v AgentVersion
	body := map[string]any{
		"description": description,
		"metadata":    metadata,
		"definition":  map[string]string{"kind": "external", "otel_agent_id": otelAgentID},
	}
	return &v, c.do(ctx, azauth.ScopeFoundry, http.MethodPost, c.project("/agents/"+url.PathEscape(name)+"/versions", nil), body, &v)
}

// ---------- Judge (chat completions) ----------

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Usage is the token count a chat completion reports.
type Usage struct {
	InputTokens  int `json:"prompt_tokens"`
	OutputTokens int `json:"completion_tokens"`
}

// ChatJSON sends messages to a deployment and decodes a JSON object reply into out.
func (c *Client) ChatJSON(ctx context.Context, deployment string, msgs []ChatMessage, out any) (Usage, error) {
	req := map[string]any{
		"model":                 deployment,
		"messages":              msgs,
		"response_format":       map[string]string{"type": "json_object"},
		"max_completion_tokens": 8000,
	}
	var resp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage Usage `json:"usage"`
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	if err := c.do(ctx, azauth.ScopeCognitive, http.MethodPost, c.OpenAIEndpoint+"/chat/completions", req, &resp); err != nil {
		return Usage{}, err
	}
	if len(resp.Choices) == 0 || resp.Choices[0].Message.Content == "" {
		return resp.Usage, fmt.Errorf("judge returned no content")
	}
	content := strings.TrimSpace(resp.Choices[0].Message.Content)
	content = strings.TrimPrefix(strings.TrimSuffix(strings.TrimPrefix(content, "```json"), "```"), "```")
	if err := json.Unmarshal([]byte(content), out); err != nil {
		return resp.Usage, fmt.Errorf("judge returned invalid JSON (%s): %w", resp.Choices[0].FinishReason, err)
	}
	return resp.Usage, nil
}

// ---------- Datasets ----------

type DatasetVersion struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	ID      string `json:"id"`
	DataURI string `json:"dataUri"`
}

// UploadDataset uploads JSONL content as a new uri_file dataset version.
func (c *Client) UploadDataset(ctx context.Context, name, version, fileName string, jsonl []byte, description string) (*DatasetVersion, error) {
	var pending struct {
		BlobReference struct {
			BlobURI    string `json:"blobUri"`
			Credential struct {
				SasURI string `json:"sasUri"`
			} `json:"credential"`
		} `json:"blobReference"`
	}
	path := fmt.Sprintf("/datasets/%s/versions/%s/startPendingUpload", url.PathEscape(name), url.PathEscape(version))
	if err := c.do(ctx, azauth.ScopeFoundry, http.MethodPost, c.project(path, nil),
		map[string]string{"pendingUploadType": "BlobReference"}, &pending); err != nil {
		return nil, fmt.Errorf("start dataset upload: %w", err)
	}
	sas := pending.BlobReference.Credential.SasURI
	container, query, _ := strings.Cut(sas, "?")
	blobURL := container + "/" + url.PathEscape(fileName) + "?" + query
	req, _ := http.NewRequestWithContext(ctx, http.MethodPut, blobURL, bytes.NewReader(jsonl))
	req.Header.Set("x-ms-blob-type", "BlockBlob")
	req.Header.Set("Content-Type", "application/jsonl")
	blobStart := time.Now()
	resp, err := c.HTTP.Do(req)
	metrics.UpstreamDuration.Since(blobStart, "service", "blob")
	metrics.UpstreamRequests.Inc("service", "blob", "outcome", metrics.Outcome(err))
	if err != nil {
		return nil, fmt.Errorf("upload dataset blob: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("upload dataset blob: HTTP %d", resp.StatusCode)
	}
	var dv DatasetVersion
	body := map[string]any{
		"type":        "uri_file",
		"dataUri":     pending.BlobReference.BlobURI + "/" + url.PathEscape(fileName),
		"description": description,
		"tags":        map[string]string{"source": "oversight"},
	}
	if err := c.do(ctx, azauth.ScopeFoundry, http.MethodPatch,
		c.project(fmt.Sprintf("/datasets/%s/versions/%s", url.PathEscape(name), url.PathEscape(version)), nil), body, &dv); err != nil {
		return nil, fmt.Errorf("register dataset: %w", err)
	}
	return &dv, nil
}

// NextDatasetVersion returns 1 + the highest numeric version of a dataset.
func (c *Client) NextDatasetVersion(ctx context.Context, name string) (string, error) {
	var r struct {
		Value []DatasetVersion `json:"value"`
	}
	err := c.do(ctx, azauth.ScopeFoundry, http.MethodGet, c.project("/datasets/"+url.PathEscape(name)+"/versions", nil), nil, &r)
	if err != nil {
		var ae *APIError
		if errors.As(err, &ae) && ae.Status == http.StatusNotFound {
			return "1", nil
		}
		return "", err
	}
	maxV := 0
	for _, v := range r.Value {
		var n int
		if _, err := fmt.Sscan(v.Version, &n); err == nil && n > maxV {
			maxV = n
		}
	}
	return fmt.Sprint(maxV + 1), nil
}

// ---------- Evals ----------

type Eval struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	CreatedAt int64             `json:"created_at"`
	Metadata  map[string]string `json:"metadata"`
}

type EvalRun struct {
	ID           string            `json:"id"`
	EvalID       string            `json:"eval_id"`
	Name         string            `json:"name"`
	Status       string            `json:"status"`
	CreatedAt    int64             `json:"created_at"`
	ReportURL    string            `json:"report_url"`
	ResultCounts map[string]int    `json:"result_counts"`
	Metadata     map[string]string `json:"metadata"`
	Error        any               `json:"error,omitempty"`
}

func (c *Client) ListEvals(ctx context.Context) ([]Eval, error) {
	var all []Eval
	after := ""
	for page := 0; page < 20; page++ {
		q := url.Values{"limit": {"100"}}
		if after != "" {
			q.Set("after", after)
		}
		var r struct {
			Data    []Eval `json:"data"`
			HasMore bool   `json:"has_more"`
			LastID  string `json:"last_id"`
		}
		if err := c.do(ctx, azauth.ScopeFoundry, http.MethodGet, c.project("/openai/v1/evals", q), nil, &r); err != nil {
			return nil, err
		}
		all = append(all, r.Data...)
		if !r.HasMore || r.LastID == "" {
			break
		}
		after = r.LastID
	}
	return all, nil
}

func (c *Client) CreateEval(ctx context.Context, body map[string]any) (*Eval, error) {
	var e Eval
	return &e, c.do(ctx, azauth.ScopeFoundry, http.MethodPost, c.project("/openai/v1/evals", nil), body, &e)
}

func (c *Client) CreateEvalRun(ctx context.Context, evalID string, body map[string]any) (*EvalRun, error) {
	var r EvalRun
	return &r, c.do(ctx, azauth.ScopeFoundry, http.MethodPost, c.project("/openai/v1/evals/"+evalID+"/runs", nil), body, &r)
}

func (c *Client) ListEvalRuns(ctx context.Context, evalID string) ([]EvalRun, error) {
	var r struct {
		Data []EvalRun `json:"data"`
	}
	return r.Data, c.do(ctx, azauth.ScopeFoundry, http.MethodGet, c.project("/openai/v1/evals/"+evalID+"/runs", url.Values{"limit": {"20"}}), nil, &r)
}
