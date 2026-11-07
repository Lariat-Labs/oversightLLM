// Package server exposes the oversight HTTP API and SSE stream.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Lariat-Labs/oversightLLM/api/internal/activity"
	"github.com/Lariat-Labs/oversightLLM/api/internal/azauth"
	"github.com/Lariat-Labs/oversightLLM/api/internal/config"
	"github.com/Lariat-Labs/oversightLLM/api/internal/evals"
	"github.com/Lariat-Labs/oversightLLM/api/internal/foundry"
	"github.com/Lariat-Labs/oversightLLM/api/internal/insights"
	"github.com/Lariat-Labs/oversightLLM/api/internal/judge"
	"github.com/Lariat-Labs/oversightLLM/api/internal/metrics"
	"github.com/Lariat-Labs/oversightLLM/api/internal/stream"
)

type Server struct {
	Version   string // release version ("dev" for local builds)
	Commit    string
	Cfg       *config.Config
	Auth      *azauth.CLI
	Foundry   *foundry.Client
	Insights  *insights.Client
	Judge     *judge.Judge
	Evals     *evals.Store
	Publisher *evals.Publisher
	Hub       *stream.Hub
	Live      *Live

	agentsMu    sync.Mutex
	agentsCache []foundry.Agent
	agentsAt    time.Time

	patterns []string // registered routes, checked against openapi.json in tests

	insightMu sync.Mutex
	insight   *judge.Insight

	auto autoJudge
}

func (s *Server) Routes() http.Handler {
	mux := &routeMux{ServeMux: http.NewServeMux(), s: s}
	mux.HandleFunc("GET /metrics", s.metricsHandler)
	mux.HandleFunc("GET /api/openapi.json", s.openapiSpec)
	mux.HandleFunc("GET /api/docs", s.apiDocs)
	mux.HandleFunc("GET /api/health", s.health)
	mux.HandleFunc("GET /api/events", s.events)
	mux.HandleFunc("GET /api/settings", s.getSettings)
	mux.HandleFunc("PUT /api/settings", s.putSettings)
	mux.HandleFunc("GET /api/alerts", s.listAlerts)
	mux.HandleFunc("GET /api/judge", s.judgeInfo)
	mux.HandleFunc("POST /api/judge/register", s.registerJudge)
	mux.HandleFunc("GET /api/overview", s.overview)
	mux.HandleFunc("GET /api/overview/insight", s.getInsight)
	mux.HandleFunc("POST /api/overview/insight", s.postInsight)
	mux.HandleFunc("GET /api/agents", s.listAgents)
	mux.HandleFunc("GET /api/agents/{id}", s.getAgent)
	mux.HandleFunc("GET /api/tasks", s.listTasks)
	mux.HandleFunc("GET /api/conversations/{id}", s.getConversation)
	mux.HandleFunc("POST /api/conversations/{id}/intent", s.conversationIntent)
	mux.HandleFunc("GET /api/intents", s.listIntents)
	mux.HandleFunc("GET /api/evals", s.listEvals)
	mux.HandleFunc("POST /api/evals", s.createEval)
	mux.HandleFunc("PUT /api/evals/{id}", s.updateEval)
	mux.HandleFunc("DELETE /api/evals/{id}", s.deleteEval)
	mux.HandleFunc("POST /api/evals/from-conversation/{id}", s.evalFromConversation)
	mux.HandleFunc("POST /api/evals/suggest", s.suggestEvals)
	mux.HandleFunc("POST /api/evals/upload", s.uploadEvals)
	mux.HandleFunc("POST /api/evals/traces", s.evaluateTraces)
	mux.HandleFunc("GET /api/evals/foundry", s.foundryEvals)
	if s.Cfg.WebDir != "" {
		mux.ServeMux.Handle("/", spa(s.Cfg.WebDir))
	}
	return s.cors(logRequests(mux.ServeMux))
}

// routeMux records each pattern it registers.
type routeMux struct {
	*http.ServeMux
	s *Server
}

func (m *routeMux) HandleFunc(pattern string, h func(http.ResponseWriter, *http.Request)) {
	m.s.patterns = append(m.s.patterns, pattern)
	m.ServeMux.HandleFunc(pattern, h)
}

// ---------- helpers ----------

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

func upstreamErr(w http.ResponseWriter, err error) {
	var ae *foundry.APIError
	if errors.As(err, &ae) && ae.Status == http.StatusNotFound {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	writeErr(w, http.StatusBadGateway, err)
}

func window(r *http.Request, def time.Duration) time.Duration {
	if v := r.URL.Query().Get("window"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return def
}

func (s *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && (slices.Contains(s.Cfg.CORSOrigins, "*") || slices.Contains(s.Cfg.CORSOrigins, origin)) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, code: http.StatusOK}
		next.ServeHTTP(rec, r)
		route := r.Pattern // set by ServeMux; empty for unmatched paths
		if route == "" {
			route = "unmatched"
		}
		metrics.HTTPRequests.Inc("route", route, "code", fmt.Sprint(rec.code))
		if r.URL.Path != "/api/events" && r.URL.Path != "/metrics" {
			metrics.HTTPDuration.Since(start, "route", route)
		}
		if r.URL.Path != "/api/events" && strings.HasPrefix(r.URL.Path, "/api/") {
			slog.Info("request", "method", r.Method, "path", r.URL.Path, "code", rec.code, "ms", time.Since(start).Milliseconds())
		}
	})
}

// statusRecorder captures the response code and keeps SSE flushing working.
type statusRecorder struct {
	http.ResponseWriter
	code int
}

func (r *statusRecorder) WriteHeader(code int) { r.code = code; r.ResponseWriter.WriteHeader(code) }

func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// spa serves the built frontend, falling back to index.html.
func spa(dir string) http.Handler {
	fs := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := filepath.Join(dir, filepath.Clean("/"+r.URL.Path))
		if st, err := os.Stat(p); err != nil || st.IsDir() {
			http.ServeFile(w, r, filepath.Join(dir, "index.html"))
			return
		}
		fs.ServeHTTP(w, r)
	})
}

// ---------- health / events ----------

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	out := map[string]any{
		"version":         s.Version,
		"commit":          s.Commit,
		"project":         s.Cfg.ProjectEndpoint,
		"projectName":     s.Cfg.ProjectName(),
		"workspaceId":     s.Cfg.WorkspaceID,
		"judgeDeployment": s.Cfg.JudgeDeployment,
		"evalDeployment":  s.Cfg.EvalDeployment,
		"openaiEndpoint":  s.Cfg.OpenAIEndpoint,
		"live":            s.Live.StatusLocked(),
		"subscribers":     s.Hub.Subscribers(),
	}
	ok := true
	if acct, err := s.Auth.Account(ctx); err != nil {
		out["azure"] = map[string]any{"ok": false, "error": err.Error()}
		ok = false
	} else {
		out["azure"] = map[string]any{"ok": true, "account": acct}
	}
	if _, err := s.agents(ctx, false); err != nil {
		out["foundry"] = map[string]any{"ok": false, "error": err.Error()}
		ok = false
	} else {
		out["foundry"] = map[string]any{"ok": true}
	}
	out["ok"] = ok
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	f := stream.Filter{Agent: r.URL.Query().Get("agent"), ConversationID: r.URL.Query().Get("conversation")}
	initial := []stream.Event{{Type: "status", Data: s.Live.StatusLocked()}}
	if f.ConversationID == "" {
		initial = append(initial, stream.Event{Type: "overview", Data: s.Live.Overview(s.Cfg.Lookback)})
	}
	s.Hub.Serve(w, r, f, initial...)
}

// ---------- overview ----------

func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	ov := s.Live.Overview(window(r, s.Cfg.Lookback))
	if a := r.URL.Query().Get("agent"); a != "" {
		ov = filterOverview(ov, a)
	}
	writeJSON(w, http.StatusOK, ov)
}

func filterOverview(ov activity.Overview, agent string) activity.Overview {
	tasks := []activity.Task{}
	for _, t := range ov.Tasks {
		if t.Agent == agent {
			tasks = append(tasks, t)
		}
	}
	feed := []activity.FeedItem{}
	for _, f := range ov.Feed {
		if f.Agent == agent {
			feed = append(feed, f)
		}
	}
	agents := []activity.AgentStats{}
	for _, a := range ov.Agents {
		if a.ID == agent {
			agents = append(agents, a)
		}
	}
	tools := []activity.ToolStats{}
	for _, t := range ov.Tools {
		if slices.Contains(t.Agents, agent) {
			tools = append(tools, t)
		}
	}
	ov.Tasks, ov.Feed, ov.Agents, ov.Tools = tasks, feed, agents, tools
	return ov
}

func (s *Server) getInsight(w http.ResponseWriter, r *http.Request) {
	s.insightMu.Lock()
	defer s.insightMu.Unlock()
	if s.insight == nil {
		writeJSON(w, http.StatusOK, nil)
		return
	}
	writeJSON(w, http.StatusOK, s.insight)
}

func (s *Server) postInsight(w http.ResponseWriter, r *http.Request) {
	ov := s.Live.Overview(window(r, s.Cfg.Lookback))
	in, err := s.Judge.SummarizeFleet(r.Context(), ov)
	if err != nil {
		upstreamErr(w, err)
		return
	}
	s.insightMu.Lock()
	s.insight = &in
	s.insightMu.Unlock()
	s.Hub.Publish(stream.Event{Type: "insight", Data: in})
	writeJSON(w, http.StatusOK, in)
}

// ---------- agents ----------

func (s *Server) agents(ctx context.Context, force bool) ([]foundry.Agent, error) {
	s.agentsMu.Lock()
	defer s.agentsMu.Unlock()
	if !force && s.agentsCache != nil && time.Since(s.agentsAt) < time.Minute {
		return s.agentsCache, nil
	}
	list, err := s.Foundry.ListAgents(ctx)
	if err != nil {
		return nil, err
	}
	s.agentsCache, s.agentsAt = list, time.Now()
	return list, nil
}

type AgentView struct {
	ID          string               `json:"id"`
	Name        string               `json:"name"`
	TraceID     string               `json:"traceId"`
	Kind        string               `json:"kind"`
	Version     string               `json:"version"`
	Description string               `json:"description"`
	State       string               `json:"state"`
	Metadata    map[string]string    `json:"metadata,omitempty"`
	CreatedAt   time.Time            `json:"createdAt"`
	InFoundry   bool                 `json:"inFoundry"`
	Stats       *activity.AgentStats `json:"stats,omitempty"`
	EvalCases   int                  `json:"evalCases"`
}

func (s *Server) agentViews(ctx context.Context, win time.Duration) ([]AgentView, error) {
	list, err := s.agents(ctx, false)
	if err != nil {
		return nil, err
	}
	ov := s.Live.Overview(win)
	stats := map[string]activity.AgentStats{}
	for _, a := range ov.Agents {
		stats[a.ID] = a
	}
	cases := map[string]int{}
	for _, c := range s.Evals.List("") {
		cases[c.Agent]++
	}
	seen := map[string]bool{}
	var out []AgentView
	for _, a := range list {
		v := AgentView{
			ID: a.Name, Name: a.Name, TraceID: a.TraceID(), Kind: a.Kind(), Version: a.Versions.Latest.Version,
			Description: a.Versions.Latest.Description, State: a.State, Metadata: a.Versions.Latest.Metadata,
			CreatedAt: time.Unix(a.Versions.Latest.CreatedAt, 0).UTC(), InFoundry: true, EvalCases: cases[a.Name],
		}
		if st, ok := stats[v.TraceID]; ok {
			v.Stats = &st
		}
		seen[v.TraceID] = true
		out = append(out, v)
	}
	// Agents that emit telemetry but are not registered in Foundry are worth seeing too.
	for id, st := range stats {
		if !seen[id] {
			st := st
			out = append(out, AgentView{ID: id, Name: id, TraceID: id, Kind: "unregistered", Stats: &st, EvalCases: cases[id]})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (s *Server) listAgents(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("refresh") == "1" {
		_, _ = s.agents(r.Context(), true)
	}
	out, err := s.agentViews(r.Context(), window(r, s.Cfg.Lookback))
	if err != nil {
		upstreamErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) findAgent(ctx context.Context, id string) (*AgentView, error) {
	views, err := s.agentViews(ctx, s.Cfg.Lookback)
	if err != nil {
		return nil, err
	}
	for _, v := range views {
		if v.ID == id || v.TraceID == id {
			return &v, nil
		}
	}
	return nil, &foundry.APIError{Status: http.StatusNotFound, Body: "agent " + id + " not found"}
}

func (s *Server) getAgent(w http.ResponseWriter, r *http.Request) {
	v, err := s.findAgent(r.Context(), r.PathValue("id"))
	if err != nil {
		upstreamErr(w, err)
		return
	}
	ov := filterOverview(s.Live.Overview(window(r, s.Cfg.Lookback)), v.TraceID)
	writeJSON(w, http.StatusOK, map[string]any{"agent": v, "overview": ov})
}

// ---------- tasks / conversations ----------

func (s *Server) listTasks(w http.ResponseWriter, r *http.Request) {
	tasks := activity.BuildTasks(s.Live.Spans(window(r, s.Cfg.Lookback)), time.Now().UTC())
	agent, status := r.URL.Query().Get("agent"), r.URL.Query().Get("status")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 200
	}
	intents := s.Judge.Cached()
	type taskView struct {
		activity.Task
		Intent *judge.Intent `json:"intent,omitempty"`
	}
	out := []taskView{}
	for _, t := range tasks {
		if (agent != "" && t.Agent != agent) || (status != "" && t.Status != status) {
			continue
		}
		tv := taskView{Task: t}
		if in, ok := intents[t.ConversationID]; ok {
			tv.Intent = &in
		}
		out = append(out, tv)
		if len(out) >= limit {
			break
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) conversation(ctx context.Context, id string) (activity.Conversation, error) {
	rows, err := s.Insights.Query(ctx, activity.ConversationQuery(id), 30*24*time.Hour)
	if err != nil {
		return activity.Conversation{}, err
	}
	if len(rows) == 0 {
		return activity.Conversation{}, &foundry.APIError{Status: http.StatusNotFound, Body: "no telemetry for conversation " + id}
	}
	spans := make([]activity.Span, 0, len(rows))
	for _, r := range rows {
		spans = append(spans, activity.SpanFromDetailRow(r))
	}
	return activity.BuildConversation(id, spans, time.Now().UTC()), nil
}

func (s *Server) getConversation(w http.ResponseWriter, r *http.Request) {
	c, err := s.conversation(r.Context(), r.PathValue("id"))
	if err != nil {
		upstreamErr(w, err)
		return
	}
	var intent *judge.Intent
	if in, ok := s.Judge.Cached()[c.ID]; ok {
		intent = &in
	}
	writeJSON(w, http.StatusOK, map[string]any{"conversation": c, "intent": intent})
}

func (s *Server) conversationIntent(w http.ResponseWriter, r *http.Request) {
	c, err := s.conversation(r.Context(), r.PathValue("id"))
	if err != nil {
		upstreamErr(w, err)
		return
	}
	in, err := s.Judge.AnalyzeConversation(r.Context(), c, r.URL.Query().Get("force") == "1")
	if err != nil {
		upstreamErr(w, err)
		return
	}
	s.Hub.Publish(stream.Event{Type: "intent", Agent: in.Agent, ConversationID: in.ConversationID, Data: in})
	writeJSON(w, http.StatusOK, in)
}

func (s *Server) listIntents(w http.ResponseWriter, r *http.Request) {
	out := []judge.Intent{}
	for _, in := range s.Judge.Cached() {
		if a := r.URL.Query().Get("agent"); a == "" || in.Agent == a {
			out = append(out, in)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].JudgedAt.After(out[j].JudgedAt) })
	writeJSON(w, http.StatusOK, out)
}

// ---------- evals ----------

func (s *Server) listEvals(w http.ResponseWriter, r *http.Request) {
	out := s.Evals.List(r.URL.Query().Get("agent"))
	if out == nil {
		out = []evals.Case{}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createEval(w http.ResponseWriter, r *http.Request) {
	var c evals.Case
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	c.ID = ""
	saved, err := s.Evals.Put(c)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.Hub.Publish(stream.Event{Type: "evals", Agent: saved.Agent, Data: saved})
	writeJSON(w, http.StatusCreated, saved)
}

func (s *Server) updateEval(w http.ResponseWriter, r *http.Request) {
	old, ok := s.Evals.Get(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, errors.New("case not found"))
		return
	}
	c := old
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	c.ID = old.ID
	saved, err := s.Evals.Put(c)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.Hub.Publish(stream.Event{Type: "evals", Agent: saved.Agent, Data: saved})
	writeJSON(w, http.StatusOK, saved)
}

func (s *Server) deleteEval(w http.ResponseWriter, r *http.Request) {
	if err := s.Evals.Delete(r.PathValue("id")); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func toolsUsed(c activity.Conversation) []string {
	var out []string
	for _, st := range c.Steps {
		if st.Kind == "tool" {
			name := strings.TrimPrefix(st.Title, "Tool: ")
			if !slices.Contains(out, name) {
				out = append(out, name)
			}
		}
	}
	return out
}

// evalFromConversation turns a real conversation into a draft regression case.
func (s *Server) evalFromConversation(w http.ResponseWriter, r *http.Request) {
	c, err := s.conversation(r.Context(), r.PathValue("id"))
	if err != nil {
		upstreamErr(w, err)
		return
	}
	var body struct {
		ExpectedBehavior string `json:"expectedBehavior"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	tools := toolsUsed(c)
	saved, err := s.Evals.Put(evals.Case{
		Agent: c.Agent, Query: c.Input, Response: c.Output, ActualTools: tools, ExpectedTools: tools,
		ExpectedBehavior:     nonEmpty(body.ExpectedBehavior, "Behaves like the reference run: same tools, grounded answer to the user's request."),
		SourceConversationID: c.ID, Category: "regression", Origin: "conversation", Status: evals.StatusSuggested,
	})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.Hub.Publish(stream.Event{Type: "evals", Agent: saved.Agent, Data: saved})
	writeJSON(w, http.StatusCreated, saved)
}

func (s *Server) suggestEvals(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Agent           string   `json:"agent"`
		Count           int      `json:"count"`
		ConversationIDs []string `json:"conversationIds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Agent == "" {
		writeErr(w, http.StatusBadRequest, errors.New(`body must be {"agent": "...", "count": 5}`))
		return
	}
	if body.Count <= 0 || body.Count > 20 {
		body.Count = 5
	}
	ctx := r.Context()
	agent, err := s.findAgent(ctx, body.Agent)
	if err != nil {
		upstreamErr(w, err)
		return
	}
	ids := body.ConversationIDs
	if len(ids) == 0 {
		for _, t := range activity.BuildTasks(s.Live.Spans(s.Cfg.Lookback), time.Now().UTC()) {
			if t.Agent == agent.TraceID && !slices.Contains(ids, t.ConversationID) {
				ids = append(ids, t.ConversationID)
			}
			if len(ids) >= 6 {
				break
			}
		}
	}
	var convs []activity.Conversation
	byID := map[string]activity.Conversation{}
	for _, id := range ids {
		c, err := s.conversation(ctx, id)
		if err != nil {
			slog.Warn("skip conversation for suggestions", "id", id, "err", err)
			continue
		}
		convs = append(convs, c)
		byID[id] = c
	}
	info := agent.Name + ": " + agent.Description
	if agent.Metadata["tools"] != "" {
		info += "\nTools: " + agent.Metadata["tools"]
	}
	if len(convs) > 0 && convs[0].SystemPrompt != "" {
		info += "\nSystem instructions:\n" + convs[0].SystemPrompt
	}
	suggested, err := s.Judge.SuggestEvals(ctx, agent.Name, info, convs, body.Count)
	if err != nil {
		upstreamErr(w, err)
		return
	}
	out := []evals.Case{}
	for _, sc := range suggested {
		c := evals.Case{
			Agent: agent.Name, Query: sc.Query, ExpectedBehavior: sc.ExpectedBehavior, ExpectedTools: sc.ExpectedTools,
			Category: sc.Category, Rationale: sc.Rationale, SourceConversationID: sc.SourceConversationID,
			Origin: "judge", Status: evals.StatusSuggested,
		}
		// Only trust source ids the judge was actually shown; otherwise match on the input text.
		src, ok := byID[sc.SourceConversationID]
		if !ok {
			c.SourceConversationID = ""
			for _, cv := range convs {
				if cv.Input != "" && strings.TrimSpace(cv.Input) == strings.TrimSpace(sc.Query) {
					src, ok, c.SourceConversationID = cv, true, cv.ID
					break
				}
			}
		}
		// A replayed real input carries the logged response so it can be graded as-is.
		if ok && strings.TrimSpace(src.Input) == strings.TrimSpace(sc.Query) {
			c.Response, c.ActualTools = src.Output, toolsUsed(src)
		}
		saved, err := s.Evals.Put(c)
		if err != nil {
			continue
		}
		out = append(out, saved)
	}
	s.Hub.Publish(stream.Event{Type: "evals", Agent: agent.Name, Data: map[string]int{"suggested": len(out)}})
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) uploadEvals(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Agent   string   `json:"agent"`
		CaseIDs []string `json:"caseIds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Agent == "" {
		writeErr(w, http.StatusBadRequest, errors.New(`body must be {"agent": "...", "caseIds": [optional]}`))
		return
	}
	agent, err := s.findAgent(r.Context(), body.Agent)
	if err != nil {
		upstreamErr(w, err)
		return
	}
	if !agent.InFoundry {
		writeErr(w, http.StatusBadRequest, errors.New("agent is not registered in Foundry; register it before uploading evals"))
		return
	}
	var cases []evals.Case
	for _, c := range s.Evals.List(agent.Name) {
		if len(body.CaseIDs) > 0 {
			if slices.Contains(body.CaseIDs, c.ID) && c.Status != evals.StatusRejected {
				cases = append(cases, c)
			}
		} else if c.Status == evals.StatusApproved || c.Status == evals.StatusUploaded {
			cases = append(cases, c)
		}
	}
	res, err := s.Publisher.Publish(r.Context(), evals.AgentTarget{Name: agent.Name, Version: agent.Version}, cases)
	metrics.EvalUploads.Inc("outcome", metrics.Outcome(err))
	if err == nil {
		metrics.EvalCasesUploaded.Add(float64(len(cases)))
	}
	if err != nil {
		upstreamErr(w, err)
		return
	}
	ref := &evals.UploadRef{EvalID: res.EvalID, RunID: res.RunID, Dataset: res.Dataset, DatasetVersion: res.DatasetVersion, ReportURL: res.ReportURL, At: time.Now().UTC()}
	for _, c := range cases {
		c.Status, c.Upload = evals.StatusUploaded, ref
		_, _ = s.Evals.Put(c)
	}
	s.Hub.Publish(stream.Event{Type: "evals.uploaded", Agent: agent.Name, Data: res})
	writeJSON(w, http.StatusOK, res)
}

// evaluateTraces grades an agent's recent runs straight from its App Insights traces.
// Unlike curated-case uploads, these runs appear under the agent's Evaluations tab in Foundry.
func (s *Server) evaluateTraces(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Agent         string `json:"agent"`
		LookbackHours int    `json:"lookbackHours"`
		MaxTraces     int    `json:"maxTraces"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Agent == "" {
		writeErr(w, http.StatusBadRequest, errors.New(`body must be {"agent": "...", "lookbackHours": 24, "maxTraces": 50}`))
		return
	}
	if body.LookbackHours <= 0 {
		body.LookbackHours = int(s.Cfg.Lookback.Hours())
	}
	if body.LookbackHours > 720 {
		body.LookbackHours = 720
	}
	if body.MaxTraces <= 0 || body.MaxTraces > 500 {
		body.MaxTraces = 50
	}
	agent, err := s.findAgent(r.Context(), body.Agent)
	if err != nil {
		upstreamErr(w, err)
		return
	}
	if !agent.InFoundry {
		writeErr(w, http.StatusBadRequest, errors.New("agent is not registered in Foundry; register it before evaluating its traces"))
		return
	}
	res, err := s.Publisher.EvaluateTraces(r.Context(), agent.Name, agent.TraceID, body.LookbackHours, body.MaxTraces)
	metrics.EvalTraceRuns.Inc("outcome", metrics.Outcome(err))
	if err != nil {
		upstreamErr(w, err)
		return
	}
	s.Hub.Publish(stream.Event{Type: "evals.uploaded", Agent: agent.Name, Data: res})
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) foundryEvals(w http.ResponseWriter, r *http.Request) {
	agent := r.URL.Query().Get("agent")
	evs, err := s.Foundry.ListEvals(r.Context())
	if err != nil {
		upstreamErr(w, err)
		return
	}
	type evalView struct {
		foundry.Eval
		Runs []foundry.EvalRun `json:"runs"`
	}
	out := []evalView{}
	for _, e := range evs {
		if agent != "" && e.Metadata["agent_name"] != agent {
			continue
		}
		runs, err := s.Foundry.ListEvalRuns(r.Context(), e.ID)
		if err != nil {
			slog.Warn("list eval runs", "eval", e.ID, "err", err)
		}
		out = append(out, evalView{Eval: e, Runs: runs})
	}
	writeJSON(w, http.StatusOK, out)
}

func nonEmpty(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

// judgeInfo describes the judge: where it runs, whether it's registered in Foundry, and its usage.
func (s *Server) judgeInfo(w http.ResponseWriter, r *http.Request) {
	registered := false
	if list, err := s.agents(r.Context(), false); err == nil {
		for _, a := range list {
			if a.Name == judge.AgentID {
				registered = true
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"agentId":       judge.AgentID,
		"deployment":    s.Cfg.JudgeDeployment,
		"graderModel":   s.Cfg.EvalDeployment,
		"endpoint":      s.Cfg.OpenAIEndpoint,
		"foundryAgent":  registered,
		"traced":        s.Judge.Tracer.Enabled(),
		"autoJudge":     s.currentSettings().AutoJudge,
		"cachedIntents": len(s.Judge.Cached()),
		"stats":         s.Judge.Stats(),
	})
}

// registerJudge creates (or adds a version of) the judge as a Foundry external agent.
func (s *Server) registerJudge(w http.ResponseWriter, r *http.Request) {
	desc, meta := s.Judge.Registration()
	v, err := s.Foundry.RegisterExternalAgent(r.Context(), judge.AgentID, judge.AgentID, desc, meta)
	if err != nil {
		upstreamErr(w, err)
		return
	}
	_, _ = s.agents(r.Context(), true)
	writeJSON(w, http.StatusOK, v)
}
