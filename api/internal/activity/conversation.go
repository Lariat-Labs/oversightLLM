package activity

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Noise spans from web frameworks that add nothing to an agent's story.
func isNoise(s Span) bool {
	return strings.HasPrefix(s.Name, "fastapi.") || strings.HasPrefix(s.Name, "/AzMonSDK")
}

// BuildConversation reconstructs a conversation's chain: user input, each reasoning
// turn, the tools it called (with their own sub-calls), log lines, and the answer.
func BuildConversation(id string, spans []Span, now time.Time) Conversation {
	c := Conversation{ID: id, Steps: []Step{}}
	spans = Normalize(spans)
	byID := map[string]Span{}
	for _, s := range spans {
		if s.ID != "" {
			byID[s.ID] = s
		}
	}
	// Agent spans anchor depth; everything else is measured from its nearest agent ancestor.
	agentIDs := map[string]bool{}
	var agentSpans []Span
	for _, s := range spans {
		if s.Kind == "agent" {
			agentIDs[s.ID] = true
			agentSpans = append(agentSpans, s)
		}
	}
	depthOf := func(s Span) (int, bool) {
		d, cur := 0, s
		for i := 0; i < 50; i++ {
			if agentIDs[cur.ParentID] {
				return d, true
			}
			p, ok := byID[cur.ParentID]
			if !ok {
				return d, false
			}
			d++
			cur = p
		}
		return d, false
	}

	sort.SliceStable(agentSpans, func(i, j int) bool { return agentSpans[i].Time.Before(agentSpans[j].Time) })
	for _, a := range agentSpans {
		if c.Started.IsZero() {
			c.Started = a.Time
		}
		c.Input = nz(c.Input, a.Input)
		c.Agent = nz(c.Agent, a.Agent)
		c.User = nz(c.User, a.User)
		c.Model = nz(c.Model, a.Model)
		c.SystemPrompt = nz(c.SystemPrompt, a.SystemPrompt)
		if len(c.ToolDefs) == 0 {
			c.ToolDefs = a.ToolDefs
		}
		c.InputTokens += a.InputTokens
		c.OutputTokens += a.OutputTokens
		c.DurationMs += a.DurationMs
		if a.Output != "" {
			c.Output = a.Output
		}
		c.StopReason = nz(a.StopReason, c.StopReason)
		c.Runs++
		end := a.Time.Add(time.Duration(a.DurationMs) * time.Millisecond)
		if end.After(c.Ended) {
			c.Ended = end
		}
	}

	var steps []Step
	for _, s := range spans {
		if isNoise(s) {
			continue
		}
		if s.Operation != "" && (c.Started.IsZero() || s.Time.Before(c.Started)) {
			c.Started = s.Time
		}
		if c.Agent == "" && s.Agent != "" {
			c.Agent = s.Agent
		}
		if s.Kind == "agent" {
			steps = append(steps, Step{
				ID: s.ID + ":in", Time: s.Time, Kind: "user", Title: "User asked",
				Detail: s.Input, OK: true, Turn: -1,
			})
			kind, title := "answer", "Agent answered"
			if s.Failed() {
				title = "Run failed"
			}
			steps = append(steps, Step{
				ID: s.ID, Time: s.Time.Add(time.Duration(s.DurationMs) * time.Millisecond), Kind: kind,
				Title: title, Detail: s.Output, DurationMs: s.DurationMs, OK: !s.Failed(), Turn: -1,
				Tokens:     s.InputTokens + s.OutputTokens,
				Attributes: pick(s.Attributes, "agw.stop_reason", "agw.tool_calls", "gen_ai.request.model", "agw.user.name", "agw.client_app"),
			})
			continue
		}
		depth, under := depthOf(s)
		if !under && s.Kind != "log" {
			// Spans above the agent (gateway, UI, HTTP ingress) are context, shown at depth 0.
			depth = 0
		}
		st := Step{ID: s.ID, ParentID: s.ParentID, Time: s.Time, DurationMs: s.DurationMs, OK: !s.Failed(), Turn: s.Turn, Depth: depth}
		switch s.Kind {
		case "llm":
			st.Kind = "llm"
			st.Tokens = s.InputTokens + s.OutputTokens
			st.Title = fmt.Sprintf("Reasoning (turn %d)", max(s.Turn, 0))
			if len(s.ToolsRequested) > 0 {
				st.Detail = "Decided to call: " + strings.Join(s.ToolsRequested, ", ")
			} else {
				st.Detail = "Composed the answer"
			}
			if s.Output != "" {
				st.Detail += "\n\n" + s.Output
			}
		case "tool":
			c.ToolCalls++
			if s.Failed() {
				c.ToolErrors++
			}
			st.Kind = "tool"
			st.Title = "Tool: " + s.Tool
			st.Detail = "Outcome: " + nz(s.Error, nz(s.Outcome, map[bool]string{true: "error", false: "ok"}[s.Failed()]))
			st.Attributes = pick(s.Attributes, "gen_ai.tool.call.id", "secsdk.operation", "agw.tool.result_chars", "gen_ai.tool.type")
		case "log":
			st.Kind = "log"
			st.Title = "Log"
			st.Detail = s.Message
			st.OK = s.Attributes["severity"] == "" || toInt(s.Attributes["severity"]) < 3
			st.Attributes = s.Attributes
		case "http", "request":
			st.Kind = "http"
			st.Title = s.Name
			st.Attributes = pick(s.Attributes, "http.response.status_code", "server.address", "url.path", "http.request.resend_count")
		case "client":
			st.Kind = "http"
			st.Title = "Client: " + s.Name
			st.Tokens = s.InputTokens + s.OutputTokens
		default:
			st.Kind = "subcall"
			st.Title = s.Name
			st.Attributes = pick(s.Attributes, "secsdk.outcome", "secsdk.result.count", "error.type", "secsdk.provider")
		}
		steps = append(steps, st)
	}
	sort.SliceStable(steps, func(i, j int) bool { return steps[i].Time.Before(steps[j].Time) })
	if steps != nil {
		c.Steps = steps
	}

	switch {
	case c.Runs == 0 && now.Sub(lastTime(spans)) < RunningWindow:
		c.Status = "running"
	case c.Runs == 0:
		c.Status = "incomplete"
	default:
		c.Status = "completed"
		for _, a := range agentSpans {
			if a.Failed() {
				c.Status = "failed"
			}
		}
	}
	if c.Ended.IsZero() {
		c.Ended = lastTime(spans)
	}
	return c
}

func lastTime(spans []Span) time.Time {
	var t time.Time
	for _, s := range spans {
		if s.Time.After(t) {
			t = s.Time
		}
	}
	return t
}

func pick(m map[string]string, ks ...string) map[string]string {
	out := map[string]string{}
	for _, k := range ks {
		if v, ok := m[k]; ok && v != "" {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// Compact renders a conversation as plain text for the judge model.
func (c Conversation) Compact(maxChars int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Agent: %s\nStatus: %s (stop: %s)\nTools available: %s\n",
		c.Agent, c.Status, c.StopReason, strings.Join(c.ToolDefs, ", "))
	if c.SystemPrompt != "" {
		fmt.Fprintf(&b, "System instructions:\n%s\n", truncate(c.SystemPrompt, 1500))
	}
	fmt.Fprintf(&b, "\nUser input:\n%s\n\nSteps:\n", nz(truncate(c.Input, 2000), "(not captured in telemetry)"))
	for _, s := range c.Steps {
		if s.Kind == "http" || s.Kind == "user" || s.Kind == "answer" {
			continue
		}
		status := "ok"
		if !s.OK {
			status = "FAILED"
		}
		fmt.Fprintf(&b, "%s- [%s] %s (%s, %s)", strings.Repeat("  ", s.Depth), s.Kind, s.Title, status, dur(s.DurationMs))
		if s.Detail != "" && s.Kind != "llm" {
			fmt.Fprintf(&b, ": %s", truncate(strings.ReplaceAll(s.Detail, "\n", " "), 300))
		} else if s.Kind == "llm" && s.Detail != "" {
			fmt.Fprintf(&b, ": %s", firstLine(s.Detail, 200))
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "\nFinal output:\n%s\n", nz(truncate(c.Output, 2500), "(not captured in telemetry)"))
	return truncate(b.String(), maxChars)
}
