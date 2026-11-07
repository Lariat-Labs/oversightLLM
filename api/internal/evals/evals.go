// Package evals stores eval cases locally (suggested → approved → uploaded) and
// publishes approved cases to Foundry as a dataset plus an evaluation run per agent.
package evals

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Lariat-Labs/oversightLLM/api/internal/foundry"
)

const (
	StatusSuggested = "suggested"
	StatusApproved  = "approved"
	StatusRejected  = "rejected"
	StatusUploaded  = "uploaded"
)

type UploadRef struct {
	EvalID         string    `json:"evalId"`
	RunID          string    `json:"runId"`
	Dataset        string    `json:"dataset"`
	DatasetVersion string    `json:"datasetVersion"`
	ReportURL      string    `json:"reportUrl"`
	At             time.Time `json:"at"`
}

type Case struct {
	ID                   string     `json:"id"`
	Agent                string     `json:"agent"`
	Query                string     `json:"query"`
	ExpectedBehavior     string     `json:"expectedBehavior"`
	ExpectedTools        []string   `json:"expectedTools"`
	Response             string     `json:"response"`
	ActualTools          []string   `json:"actualTools"`
	SourceConversationID string     `json:"sourceConversationId,omitempty"`
	Category             string     `json:"category,omitempty"`
	Rationale            string     `json:"rationale,omitempty"`
	Origin               string     `json:"origin"` // judge | manual | conversation
	Status               string     `json:"status"`
	CreatedAt            time.Time  `json:"createdAt"`
	UpdatedAt            time.Time  `json:"updatedAt"`
	Upload               *UploadRef `json:"upload,omitempty"`
}

type Store struct {
	path  string
	mu    sync.Mutex
	cases map[string]*Case
}

func NewStore(dataDir string) (*Store, error) {
	s := &Store{path: filepath.Join(dataDir, "evals.json"), cases: map[string]*Case{}}
	if b, err := os.ReadFile(s.path); err == nil {
		var list []*Case
		if err := json.Unmarshal(b, &list); err != nil {
			return nil, fmt.Errorf("read %s: %w", s.path, err)
		}
		for _, c := range list {
			s.cases[c.ID] = c
		}
	}
	return s, nil
}

func (s *Store) List(agent string) []Case {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Case
	for _, c := range s.cases {
		if agent == "" || c.Agent == agent {
			out = append(out, *c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

func (s *Store) Get(id string) (Case, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cases[id]
	if !ok {
		return Case{}, false
	}
	return *c, true
}

func (s *Store) Put(c Case) (Case, error) {
	if strings.TrimSpace(c.Agent) == "" || strings.TrimSpace(c.Query) == "" {
		return c, fmt.Errorf("agent and query are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	if c.ID == "" {
		c.ID = newID()
		c.CreatedAt = now
	} else if old, ok := s.cases[c.ID]; ok {
		c.CreatedAt = old.CreatedAt
		if c.Upload == nil {
			c.Upload = old.Upload
		}
	}
	if c.Status == "" {
		c.Status = StatusApproved
	}
	if c.Origin == "" {
		c.Origin = "manual"
	}
	c.UpdatedAt = now
	cp := c
	s.cases[c.ID] = &cp
	return c, s.save()
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.cases, id)
	return s.save()
}

func (s *Store) save() error {
	list := make([]*Case, 0, len(s.cases))
	for _, c := range s.cases {
		list = append(list, c)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].CreatedAt.Before(list[j].CreatedAt) })
	b, _ := json.MarshalIndent(list, "", "  ")
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func newID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return "case_" + hex.EncodeToString(b)
}

// ---------- Foundry publishing ----------

// Item is one JSONL row in the uploaded dataset / eval run.
type Item struct {
	Query                string `json:"query"`
	Response             string `json:"response"`
	ExpectedBehavior     string `json:"expected_behavior"`
	ExpectedTools        string `json:"expected_tools"`
	ActualTools          string `json:"actual_tools"`
	Category             string `json:"category"`
	SourceConversationID string `json:"source_conversation_id"`
	CaseID               string `json:"case_id"`
}

func ToItem(c Case) Item {
	return Item{
		Query: c.Query, Response: c.Response, ExpectedBehavior: c.ExpectedBehavior,
		ExpectedTools: strings.Join(c.ExpectedTools, ", "), ActualTools: strings.Join(c.ActualTools, ", "),
		Category: c.Category, SourceConversationID: c.SourceConversationID, CaseID: c.ID,
	}
}

// AgentTarget describes the agent the eval belongs to.
type AgentTarget struct {
	Name    string
	Version string
}

type Publisher struct {
	Foundry     *foundry.Client
	GraderModel string
}

type PublishResult struct {
	EvalID         string `json:"evalId"`
	EvalName       string `json:"evalName"`
	RunID          string `json:"runId"`
	RunStatus      string `json:"runStatus"`
	ReportURL      string `json:"reportUrl"`
	Dataset        string `json:"dataset"`
	DatasetVersion string `json:"datasetVersion"`
	Cases          int    `json:"cases"`
}

func EvalName(agent string) string    { return "oversight-" + agent }
func DatasetName(agent string) string { return "oversight-" + agent + "-evals" }

// Publish uploads cases as a dataset version, ensures the agent's eval exists, and starts a run.
func (p *Publisher) Publish(ctx context.Context, target AgentTarget, cases []Case) (*PublishResult, error) {
	if len(cases) == 0 {
		return nil, fmt.Errorf("no approved cases to upload")
	}
	items := make([]Item, len(cases))
	var buf bytes.Buffer
	for i, c := range cases {
		items[i] = ToItem(c)
		b, _ := json.Marshal(items[i])
		buf.Write(b)
		buf.WriteByte('\n')
	}

	res := &PublishResult{Dataset: DatasetName(target.Name), Cases: len(cases)}

	ver, err := p.Foundry.NextDatasetVersion(ctx, res.Dataset)
	if err != nil {
		return nil, err
	}
	dv, err := p.Foundry.UploadDataset(ctx, res.Dataset, ver, res.Dataset+"-v"+ver+".jsonl", buf.Bytes(),
		fmt.Sprintf("Eval cases for agent %s, curated in Oversight", target.Name))
	if err != nil {
		return nil, err
	}
	res.DatasetVersion = dv.Version

	eval, err := p.ensureEval(ctx, target)
	if err != nil {
		return nil, err
	}
	res.EvalID, res.EvalName = eval.ID, eval.Name

	content := make([]map[string]any, len(items))
	for i, it := range items {
		content[i] = map[string]any{"item": it}
	}
	source := map[string]any{"type": "file_content", "content": content}
	// Agents are external (Foundry can't call them), so each case is graded on its logged response.
	dataSource := map[string]any{"type": "jsonl", "source": source}
	run, err := p.Foundry.CreateEvalRun(ctx, eval.ID, map[string]any{
		"name":        fmt.Sprintf("%s dataset v%s (%d cases)", target.Name, dv.Version, len(cases)),
		"metadata":    map[string]string{"agent_name": target.Name, "agent_version": target.Version, "dataset": res.Dataset, "dataset_version": dv.Version, "source": "oversight"},
		"data_source": dataSource,
	})
	if err != nil {
		return nil, fmt.Errorf("create eval run: %w", err)
	}
	res.RunID, res.RunStatus, res.ReportURL = run.ID, run.Status, run.ReportURL
	return res, nil
}

func (p *Publisher) ensureEval(ctx context.Context, target AgentTarget) (*foundry.Eval, error) {
	evs, err := p.Foundry.ListEvals(ctx)
	if err != nil {
		return nil, fmt.Errorf("list evals: %w", err)
	}
	for _, e := range evs {
		if e.Name == EvalName(target.Name) && e.Metadata["source"] == "oversight" {
			ev := e
			return &ev, nil
		}
	}
	e, err := p.Foundry.CreateEval(ctx, EvalDefinition(target, p.GraderModel))
	if err != nil {
		return nil, fmt.Errorf("create eval: %w", err)
	}
	return e, nil
}

// EvalDefinition builds the Foundry eval: item schema plus graders.
func EvalDefinition(target AgentTarget, grader string) map[string]any {
	const response = "{{item.response}}"
	props := map[string]any{}
	for _, k := range []string{"query", "response", "expected_behavior", "expected_tools", "actual_tools", "category", "source_conversation_id", "case_id"} {
		props[k] = map[string]string{"type": "string"}
	}
	criteria := []any{
		map[string]any{
			"type": "azure_ai_evaluator", "name": "intent_resolution", "evaluator_name": "builtin.intent_resolution",
			"data_mapping":              map[string]string{"query": "{{item.query}}", "response": response},
			"initialization_parameters": map[string]string{"deployment_name": grader},
		},
		map[string]any{
			"type": "label_model", "name": "meets_expected_behavior", "model": grader,
			"input": []any{
				map[string]string{"role": "developer", "content": "You grade an AI agent's response against an expected behavior. Reply with exactly one label: pass or fail."},
				map[string]string{"role": "user", "content": "Query:\n{{item.query}}\n\nExpected behavior:\n{{item.expected_behavior}}\n\nAgent response:\n" + response},
			},
			"labels": []string{"pass", "fail"}, "passing_labels": []string{"pass"},
		},
	}
	criteria = append(criteria, map[string]any{
		"type": "label_model", "name": "used_expected_tools", "model": grader,
		"input": []any{
			map[string]string{"role": "developer", "content": "Did the agent call every expected tool? Extra tools are fine. If no tools are expected, pass. Reply with exactly one label: pass or fail."},
			map[string]string{"role": "user", "content": "Expected tools: {{item.expected_tools}}\nTools the agent called: {{item.actual_tools}}"},
		},
		"labels": []string{"pass", "fail"}, "passing_labels": []string{"pass"},
	})
	return map[string]any{
		"name":     EvalName(target.Name),
		"metadata": map[string]string{"agent_name": target.Name, "source": "oversight", "mode": "logged_responses"},
		"data_source_config": map[string]any{
			"type":                  "custom",
			"item_schema":           map[string]any{"type": "object", "properties": props, "required": []string{"query"}},
			"include_sample_schema": false,
		},
		"testing_criteria": criteria,
	}
}

// ---------- Trace evals (recent runs, linked to the agent in Foundry) ----------

// TraceEvalName is the per-agent eval that grades the agent's own App Insights traces.
func TraceEvalName(agent string) string { return "oversight-traces-" + agent }

// TraceEvalDefinition grades invoke_agent spans directly. Foundry links runs of this
// eval to the agent (its Evaluations tab) because the data source names the agent.
func TraceEvalDefinition(agent, grader string) map[string]any {
	ev := func(name, evaluator string, mapping map[string]string) map[string]any {
		return map[string]any{
			"type": "azure_ai_evaluator", "name": name, "evaluator_name": evaluator, "data_mapping": mapping,
			"initialization_parameters": map[string]string{"deployment_name": grader},
		}
	}
	base := map[string]string{"query": "{{item.query}}", "response": "{{item.response}}", "tool_definitions": "{{item.tool_definitions}}"}
	withCalls := map[string]string{"tool_calls": "{{item.tool_calls}}"}
	for k, v := range base {
		withCalls[k] = v
	}
	return map[string]any{
		"name":               TraceEvalName(agent),
		"metadata":           map[string]string{"agent_name": agent, "source": "oversight", "mode": "traces"},
		"data_source_config": map[string]string{"type": "azure_ai_source", "scenario": "traces"},
		"testing_criteria": []any{
			ev("intent_resolution", "builtin.intent_resolution", base),
			ev("task_adherence", "builtin.task_adherence", base),
			ev("tool_call_accuracy", "builtin.tool_call_accuracy", withCalls),
		},
	}
}

// TraceRunResult describes a started trace eval run.
type TraceRunResult struct {
	EvalID        string `json:"evalId"`
	EvalName      string `json:"evalName"`
	RunID         string `json:"runId"`
	RunStatus     string `json:"runStatus"`
	ReportURL     string `json:"reportUrl"`
	AgentID       string `json:"agentId"`
	LookbackHours int    `json:"lookbackHours"`
	MaxTraces     int    `json:"maxTraces"`
}

// EvaluateTraces starts a run over the agent's recent traces. agentName is the Foundry
// agent; traceID is the gen_ai.agent.id its spans carry (Foundry's otel_agent_id).
func (p *Publisher) EvaluateTraces(ctx context.Context, agentName, traceID string, lookbackHours, maxTraces int) (*TraceRunResult, error) {
	evs, err := p.Foundry.ListEvals(ctx)
	if err != nil {
		return nil, fmt.Errorf("list evals: %w", err)
	}
	var eval *foundry.Eval
	for _, e := range evs {
		if e.Name == TraceEvalName(agentName) && e.Metadata["source"] == "oversight" && e.Metadata["mode"] == "traces" {
			ev := e
			eval = &ev
			break
		}
	}
	if eval == nil {
		if eval, err = p.Foundry.CreateEval(ctx, TraceEvalDefinition(agentName, p.GraderModel)); err != nil {
			return nil, fmt.Errorf("create trace eval: %w", err)
		}
	}
	run, err := p.Foundry.CreateEvalRun(ctx, eval.ID, map[string]any{
		"name":     fmt.Sprintf("%s · last %dh · up to %d runs", agentName, lookbackHours, maxTraces),
		"metadata": map[string]string{"agent_id": traceID, "agent_name": agentName, "source": "oversight", "mode": "traces"},
		"data_source": map[string]any{
			"type": "azure_ai_traces", "agent_id": traceID, "max_traces": maxTraces, "lookback_hours": lookbackHours,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("create trace eval run: %w", err)
	}
	return &TraceRunResult{EvalID: eval.ID, EvalName: eval.Name, RunID: run.ID, RunStatus: run.Status, ReportURL: run.ReportURL,
		AgentID: traceID, LookbackHours: lookbackHours, MaxTraces: maxTraces}, nil
}
