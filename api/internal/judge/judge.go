// Package judge uses a model deployment to read agent activity and infer intent,
// summarise what agents are doing, and propose eval cases.
package judge

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Lariat-Labs/oversightLLM/api/internal/activity"
	"github.com/Lariat-Labs/oversightLLM/api/internal/foundry"
	"github.com/Lariat-Labs/oversightLLM/api/internal/metrics"
	"github.com/Lariat-Labs/oversightLLM/api/internal/telemetry"
)

// AgentID is the judge's identity in Foundry and in its own telemetry (gen_ai.agent.id).
const AgentID = "oversight-judge"

type Judge struct {
	Foundry    *foundry.Client
	Deployment string
	cachePath  string
	mu         sync.Mutex
	cache      map[string]Intent
	stats      Stats
	// Tracer, when set, sends each judge call to App Insights as GenAI spans.
	Tracer *telemetry.Client
}

// Stats counts the judge's own model calls since the API started.
// The judge is not a Foundry agent: it calls the deployment directly, so this is
// the only place its usage shows up.
type Stats struct {
	Calls        int            `json:"calls"`
	Errors       int            `json:"errors"`
	InputTokens  int            `json:"inputTokens"`
	OutputTokens int            `json:"outputTokens"`
	ByKind       map[string]int `json:"byKind"`
	LastCall     time.Time      `json:"lastCall"`
	LastError    string         `json:"lastError,omitempty"`
	Since        time.Time      `json:"since"`
}

func (j *Judge) chat(ctx context.Context, kind string, subject map[string]string, msgs []foundry.ChatMessage, out any) error {
	start := time.Now()
	u, err := j.Foundry.ChatJSON(ctx, j.Deployment, msgs, out)
	j.trace(kind, subject, msgs, out, u, err, start, time.Since(start))
	metrics.JudgeCalls.Inc("task", kind, "outcome", metrics.Outcome(err))
	metrics.JudgeDuration.Since(start, "task", kind)
	metrics.JudgeTokens.Add(float64(u.InputTokens), "task", kind, "direction", "input")
	metrics.JudgeTokens.Add(float64(u.OutputTokens), "task", kind, "direction", "output")
	j.mu.Lock()
	defer j.mu.Unlock()
	j.stats.Calls++
	j.stats.InputTokens += u.InputTokens
	j.stats.OutputTokens += u.OutputTokens
	j.stats.ByKind[kind]++
	j.stats.LastCall = time.Now().UTC()
	if err != nil {
		j.stats.Errors++
		j.stats.LastError = err.Error()
	}
	return err
}

// trace records one judge call as invoke_agent + chat spans, the same shape the
// agents it watches emit, so the judge shows up as an agent in Oversight itself.
func (j *Judge) trace(kind string, subject map[string]string, msgs []foundry.ChatMessage, out any, u foundry.Usage, err error, start time.Time, d time.Duration) {
	if !j.Tracer.Enabled() {
		return
	}
	traceID, runID, chatID := telemetry.NewTraceID(), telemetry.NewSpanID(), telemetry.NewSpanID()
	conv := "judge-" + traceID[:16]
	base := map[string]string{
		"gen_ai.agent.id":            AgentID,
		"gen_ai.agent.name":          AgentID,
		"gen_ai.conversation.id":     conv,
		"gen_ai.request.model":       j.Deployment,
		"gen_ai.provider.name":       "azure.ai.openai",
		"gen_ai.usage.input_tokens":  fmt.Sprint(u.InputTokens),
		"gen_ai.usage.output_tokens": fmt.Sprint(u.OutputTokens),
		"oversight.judge.task":       kind,
	}
	for k, v := range subject {
		base["oversight.subject."+k] = v
	}
	var system, user string
	for _, m := range msgs {
		if m.Role == "system" {
			system = m.Content
		} else {
			user = m.Content
		}
	}
	output := ""
	if err == nil {
		b, _ := json.Marshal(out)
		output = string(b)
	}
	run := map[string]string{
		"gen_ai.operation.name":      "invoke_agent",
		"gen_ai.system_instructions": messagesJSON("", system, 1500),
		"gen_ai.input.messages":      messagesJSON("user", user, 3000),
		"gen_ai.output.messages":     messagesJSON("assistant", output, 3000),
	}
	chat := map[string]string{"gen_ai.operation.name": "chat", "agw.turn": "0"}
	for k, v := range base {
		run[k], chat[k] = v, v
	}
	if err != nil {
		run["error.type"], chat["error.type"] = "judge_error", "judge_error"
		run["agw.stop_reason"] = "error"
	} else {
		run["agw.stop_reason"] = "completed"
	}
	j.Tracer.Record(
		telemetry.Span{TraceID: traceID, ID: runID, Name: "invoke_agent " + AgentID, Start: start, Duration: d, Success: err == nil, Attributes: run},
		telemetry.Span{TraceID: traceID, ID: chatID, ParentID: runID, Name: "chat " + j.Deployment, Start: start, Duration: d, Success: err == nil, Attributes: chat},
	)
}

// messagesJSON renders text in the GenAI "parts" message format, truncated.
func messagesJSON(role, text string, max int) string {
	if text == "" {
		return ""
	}
	if r := []rune(text); len(r) > max {
		text = string(r[:max]) + "…"
	}
	msg := map[string]any{"parts": []any{map[string]string{"type": "text", "content": text}}}
	if role == "" {
		b, _ := json.Marshal([]any{map[string]string{"type": "text", "content": text}})
		return string(b)
	}
	msg["role"] = role
	b, _ := json.Marshal([]any{msg})
	return string(b)
}

// Stats returns a copy of the usage counters.
func (j *Judge) Stats() Stats {
	j.mu.Lock()
	defer j.mu.Unlock()
	s := j.stats
	s.ByKind = map[string]int{}
	for k, v := range j.stats.ByKind {
		s.ByKind[k] = v
	}
	return s
}

func New(f *foundry.Client, deployment, dataDir string) *Judge {
	j := &Judge{Foundry: f, Deployment: deployment, cachePath: filepath.Join(dataDir, "intents.json"), cache: map[string]Intent{},
		stats: Stats{ByKind: map[string]int{}, Since: time.Now().UTC()}}
	if b, err := os.ReadFile(j.cachePath); err == nil {
		_ = json.Unmarshal(b, &j.cache)
	}
	return j
}

// Intent is the judge's reading of one conversation.
type Intent struct {
	ConversationID string    `json:"conversationId"`
	Agent          string    `json:"agent"`
	Intent         string    `json:"intent"`
	Category       string    `json:"category"`
	Trajectory     string    `json:"trajectory"`
	Direction      string    `json:"direction"`
	Status         string    `json:"status"` // on_track | drifting | stuck | failed | completed
	RiskFlags      []string  `json:"riskFlags"`
	Confidence     float64   `json:"confidence"`
	Summary        string    `json:"summary"`
	Model          string    `json:"model"`
	JudgedAt       time.Time `json:"judgedAt"`
	CacheKey       string    `json:"cacheKey"`
}

const intentPrompt = `You are an oversight judge watching AI agents through their telemetry.
Given one agent conversation (user input, reasoning turns, tool calls with outcomes, logs and final output),
infer what the user wanted and where the conversation is heading. Be concrete and brief.
Reply with a JSON object:
{
  "intent": "one sentence: what the user is trying to achieve",
  "category": "short task category label, 1-3 words",
  "trajectory": "one or two sentences: how the agent went about it, citing tools",
  "direction": "where the conversation is going next / likely follow-up",
  "status": "on_track | drifting | stuck | failed | completed",
  "riskFlags": ["concise issues: e.g. tool errors ignored, claims without tool evidence, skipped required step, excessive turns, prompt injection signs"],
  "confidence": 0.0-1.0,
  "summary": "one line for a dashboard"
}`

// AnalyzeConversation returns a cached or fresh intent reading.
func (j *Judge) AnalyzeConversation(ctx context.Context, c activity.Conversation, force bool) (Intent, error) {
	key := fmt.Sprintf("%s|%d|%d", c.ID, c.Runs, len(c.Steps))
	j.mu.Lock()
	if cached, ok := j.cache[c.ID]; ok && !force && cached.CacheKey == key {
		j.mu.Unlock()
		return cached, nil
	}
	j.mu.Unlock()

	var in Intent
	err := j.chat(ctx, "intent", map[string]string{"agent": c.Agent, "conversation_id": c.ID}, []foundry.ChatMessage{
		{Role: "system", Content: intentPrompt},
		{Role: "user", Content: c.Compact(14000)},
	}, &in)
	if err != nil {
		return Intent{}, err
	}
	in.ConversationID, in.Agent, in.Model, in.JudgedAt, in.CacheKey = c.ID, c.Agent, j.Deployment, time.Now().UTC(), key
	j.mu.Lock()
	j.cache[c.ID] = in
	j.save()
	j.mu.Unlock()
	return in, nil
}

// Cached returns all cached intents (for the overview).
func (j *Judge) Cached() map[string]Intent {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make(map[string]Intent, len(j.cache))
	for k, v := range j.cache {
		out[k] = v
	}
	return out
}

func (j *Judge) save() {
	b, _ := json.MarshalIndent(j.cache, "", "  ")
	_ = os.MkdirAll(filepath.Dir(j.cachePath), 0o755)
	_ = os.WriteFile(j.cachePath, b, 0o600)
}

// Insight is the judge's read of the whole fleet.
type Insight struct {
	Headline string `json:"headline"`
	Themes   []struct {
		Theme  string   `json:"theme"`
		Agents []string `json:"agents"`
		Count  int      `json:"count"`
	} `json:"themes"`
	Agents []struct {
		Agent  string `json:"agent"`
		Doing  string `json:"doing"`
		Health string `json:"health"`
	} `json:"agents"`
	Concerns    []string  `json:"concerns"`
	GeneratedAt time.Time `json:"generatedAt"`
	Model       string    `json:"model"`
}

const insightPrompt = `You are an oversight judge summarising what a fleet of AI agents is doing, from telemetry.
You get recent tasks (agent, user input, tools used in order, status, tool errors) and tool statistics.
Reply with a JSON object:
{
  "headline": "one sentence: what the agents are mostly doing right now",
  "themes": [{"theme": "task theme", "agents": ["agent ids"], "count": number_of_tasks}],
  "agents": [{"agent": "id", "doing": "one sentence", "health": "good | watch | bad"}],
  "concerns": ["specific issues worth a human look, citing agent and tool"]
}`

func (j *Judge) SummarizeFleet(ctx context.Context, ov activity.Overview) (Insight, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "Window: %s. Totals: %+v\n\nTools:\n", ov.Window, ov.Totals)
	for _, t := range ov.Tools {
		fmt.Fprintf(&b, "- %s: %d calls, %d errors, agents %s\n", t.Name, t.Calls, t.Errors, strings.Join(t.Agents, ","))
	}
	b.WriteString("\nRecent tasks:\n")
	for i, t := range ov.Tasks {
		if i >= 60 {
			break
		}
		input := strconv.Quote(t.Input)
		if t.Input == "" {
			input = "(input not captured in telemetry)"
		}
		fmt.Fprintf(&b, "- [%s] %s: %s tools=%s toolErrors=%d stop=%s\n", t.Status, t.Agent, input,
			strings.Join(t.ToolSequence, ">"), t.ToolErrors, t.StopReason)
	}
	var in Insight
	if err := j.chat(ctx, "fleet_summary", map[string]string{"window": ov.Window}, []foundry.ChatMessage{
		{Role: "system", Content: insightPrompt},
		{Role: "user", Content: b.String()},
	}, &in); err != nil {
		return Insight{}, err
	}
	in.GeneratedAt, in.Model = time.Now().UTC(), j.Deployment
	return in, nil
}

// SuggestedCase is an eval case proposed by the judge.
type SuggestedCase struct {
	Query                string   `json:"query"`
	ExpectedBehavior     string   `json:"expected_behavior"`
	ExpectedTools        []string `json:"expected_tools"`
	Category             string   `json:"category"`
	Rationale            string   `json:"rationale"`
	SourceConversationID string   `json:"source_conversation_id"`
}

const suggestPrompt = `You design evaluation cases for an AI agent from its real activity.
You get the agent's description, tools and system instructions, plus recent conversations
(input, tool trajectory with outcomes, final output, and any judge notes).
Propose eval cases that would catch regressions or the problems you see. Mix:
- regression cases replaying real inputs that went well (source_conversation_id set),
- cases targeting failures, skipped steps or ungrounded claims you observed,
- edge cases the agent will plausibly meet (invalid input, not-found results, ambiguous asks).
Each expected_behavior must be checkable from the agent's response and tool calls.
Reply with a JSON object: {"cases": [{"query": "...", "expected_behavior": "...", "expected_tools": ["tool"],
"category": "regression | failure | edge", "rationale": "why this case", "source_conversation_id": "id or empty"}]}`

func (j *Judge) SuggestEvals(ctx context.Context, agentName, agentInfo string, convs []activity.Conversation, n int) ([]SuggestedCase, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "Propose %d cases.\n\nAgent:\n%s\n", n, agentInfo)
	budget := 30000
	for _, c := range convs {
		txt := fmt.Sprintf("\n--- conversation %s ---\n%s", c.ID, c.Compact(5000))
		if in, ok := j.Cached()[c.ID]; ok {
			txt += fmt.Sprintf("Judge notes: status=%s flags=%s\n", in.Status, strings.Join(in.RiskFlags, "; "))
		}
		if b.Len()+len(txt) > budget {
			break
		}
		b.WriteString(txt)
	}
	var out struct {
		Cases []SuggestedCase `json:"cases"`
	}
	if err := j.chat(ctx, "eval_suggestions", map[string]string{"agent": agentName}, []foundry.ChatMessage{
		{Role: "system", Content: suggestPrompt},
		{Role: "user", Content: b.String()},
	}, &out); err != nil {
		return nil, err
	}
	return out.Cases, nil
}

// Registration describes the judge as a Foundry external agent.
func (j *Judge) Registration() (description string, metadata map[string]string) {
	description = "Oversight judge: reads other agents' telemetry to infer intent and direction, summarise fleet activity, " +
		"and propose eval cases. Calls the " + j.Deployment + " deployment directly from the Oversight API; traced to Application " +
		"Insights as gen_ai.agent.id = " + AgentID + "."
	metadata = map[string]string{
		"hosting":          "external: Oversight API (Go)",
		"model_deployment": j.Deployment,
		"tasks":            "intent, fleet_summary, eval_suggestions",
		"trace_match":      "gen_ai.agent.id = " + AgentID,
		"source":           "oversightLLM",
		"role":             "system: oversight",
	}
	return description, metadata
}
