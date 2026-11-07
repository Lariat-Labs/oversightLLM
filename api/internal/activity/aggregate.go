package activity

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// RunningWindow is how long a run without a closing invoke_agent span counts as running.
const RunningWindow = 10 * time.Minute

// BuildTasks groups spans into runs. A run is keyed by conversation + trace, so a
// multi-turn conversation yields one task per user turn.
func BuildTasks(spans []Span, now time.Time) []Task {
	spans = Normalize(spans)
	type acc struct {
		t     Task
		agent *Span
	}
	byKey := map[string]*acc{}
	var order []string
	for i := range spans {
		s := spans[i]
		if s.Operation == "" {
			continue
		}
		conv := s.ConversationID
		if conv == "" {
			conv = s.OperationID
		}
		key := conv + "|" + s.OperationID
		a, ok := byKey[key]
		if !ok {
			a = &acc{t: Task{ConversationID: conv, Agent: s.Agent, Started: s.Time, LastActivity: s.Time, ToolSequence: []string{}}}
			byKey[key] = a
			order = append(order, key)
		}
		t := &a.t
		if s.Time.Before(t.Started) {
			t.Started = s.Time
		}
		end := s.Time.Add(time.Duration(s.DurationMs) * time.Millisecond)
		if end.After(t.LastActivity) {
			t.LastActivity = end
		}
		if t.Agent == "" {
			t.Agent = s.Agent
		}
		if t.User == "" {
			t.User = s.User
		}
		switch s.Kind {
		case "agent":
			a.agent = &spans[i]
			t.Input = firstLine(s.Input, 240)
			t.OutputPreview = firstLine(s.Output, 240)
			t.DurationMs = s.DurationMs
			t.StopReason = s.StopReason
			t.Model = s.Model
			t.Tokens = s.InputTokens + s.OutputTokens
		case "llm":
			t.Turns++
			if a.agent == nil {
				t.Tokens += s.InputTokens + s.OutputTokens
			}
			if t.Model == "" {
				t.Model = s.Model
			}
		case "tool":
			if s.Failed() {
				t.ToolErrors++
			}
		}
	}
	// Tool sequence in time order.
	sorted := append([]Span(nil), spans...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Time.Before(sorted[j].Time) })
	for _, s := range sorted {
		if s.Kind != "tool" {
			continue
		}
		conv := s.ConversationID
		if conv == "" {
			conv = s.OperationID
		}
		if a := byKey[conv+"|"+s.OperationID]; a != nil {
			a.t.ToolSequence = append(a.t.ToolSequence, s.Tool)
		}
	}
	tasks := make([]Task, 0, len(order))
	for _, k := range order {
		a := byKey[k]
		t := a.t
		switch {
		case a.agent != nil && a.agent.Failed():
			t.Status = "failed"
		case a.agent != nil:
			t.Status = "completed"
		case now.Sub(t.LastActivity) < RunningWindow:
			t.Status = "running"
			t.DurationMs = float64(t.LastActivity.Sub(t.Started).Milliseconds())
		default:
			t.Status = "incomplete"
			t.DurationMs = float64(t.LastActivity.Sub(t.Started).Milliseconds())
		}
		tasks = append(tasks, t)
	}
	sort.SliceStable(tasks, func(i, j int) bool { return tasks[i].Started.After(tasks[j].Started) })
	return tasks
}

// BuildOverview aggregates spans from the last window into the central dashboard.
func BuildOverview(spans []Span, window time.Duration, now time.Time) Overview {
	ov := Overview{GeneratedAt: now, Window: FormatWindow(window), Agents: []AgentStats{}, Tools: []ToolStats{}}
	spans = Normalize(spans)
	tasks := BuildTasks(spans, now)

	agents := map[string]*AgentStats{}
	agentTools := map[string]map[string]int{}
	agentUsers := map[string]map[string]bool{}
	agentDur := map[string]float64{}
	tools := map[string]*ToolStats{}
	toolAgents := map[string]map[string]bool{}
	toolOutcomes := map[string]map[string]int{}
	convs := map[string]bool{}

	getAgent := func(id string) *AgentStats {
		if id == "" {
			id = "(unknown)"
		}
		a, ok := agents[id]
		if !ok {
			a = &AgentStats{ID: id}
			agents[id] = a
			agentTools[id] = map[string]int{}
			agentUsers[id] = map[string]bool{}
		}
		return a
	}

	for _, s := range spans {
		if s.Operation == "" {
			continue
		}
		a := getAgent(s.Agent)
		if s.Time.After(a.LastSeen) {
			a.LastSeen = s.Time
		}
		if s.User != "" {
			agentUsers[a.ID][s.User] = true
		}
		if s.ConversationID != "" {
			convs[s.ConversationID] = true
		}
		switch s.Kind {
		case "agent":
			ov.Totals.InputTokens += s.InputTokens
			ov.Totals.OutputTokens += s.OutputTokens
			a.InputTokens += s.InputTokens
			a.OutputTokens += s.OutputTokens
			agentDur[a.ID] += s.DurationMs
		case "llm":
			ov.Totals.LLMCalls++
			a.LLMCalls++
		case "tool":
			ov.Totals.ToolCalls++
			a.ToolCalls++
			agentTools[a.ID][s.Tool]++
			t, ok := tools[s.Tool]
			if !ok {
				t = &ToolStats{Name: s.Tool}
				tools[s.Tool] = t
				toolAgents[s.Tool] = map[string]bool{}
				toolOutcomes[s.Tool] = map[string]int{}
			}
			t.Calls++
			t.AvgMs += s.DurationMs
			toolAgents[s.Tool][a.ID] = true
			outcome := s.Outcome
			if outcome == "" {
				outcome = map[bool]string{true: "error", false: "ok"}[s.Failed()]
			}
			toolOutcomes[s.Tool][outcome]++
			if s.Failed() {
				t.Errors++
				a.ToolErrors++
				ov.Totals.ToolErrors++
			}
		}
	}

	for _, t := range tasks {
		a := getAgent(t.Agent)
		switch t.Status {
		case "running":
			a.Running++
			ov.Totals.Running++
		case "failed":
			a.FailedRuns++
			a.Runs++
			ov.Totals.FailedRuns++
			ov.Totals.Runs++
		case "completed":
			a.Runs++
			ov.Totals.Runs++
		}
	}
	ov.Totals.Conversations = len(convs)

	runDurations := map[string][]float64{}
	for _, t := range tasks {
		if t.Status == "completed" || t.Status == "failed" {
			id := t.Agent
			if id == "" {
				id = "(unknown)"
			}
			runDurations[id] = append(runDurations[id], t.DurationMs)
		}
	}
	spansByAgent := map[string][]Span{}
	for _, s := range spans {
		id := s.Agent
		if id == "" {
			id = "(unknown)"
		}
		spansByAgent[id] = append(spansByAgent[id], s)
	}
	for id, a := range agents {
		if a.Runs > 0 {
			a.AvgDurationMs = agentDur[id] / float64(a.Runs)
		}
		a.P95DurationMs = percentile(runDurations[id], 95)
		a.Timeline = buildTimeline(spansByAgent[id], window, now)
		a.TopTools = topN(agentTools[id], 5)
		a.Users = keys(agentUsers[id])
		if now.Sub(a.LastSeen) < time.Hour {
			ov.Totals.ActiveAgents++
		}
		ov.Agents = append(ov.Agents, *a)
	}
	sort.Slice(ov.Agents, func(i, j int) bool { return ov.Agents[i].LastSeen.After(ov.Agents[j].LastSeen) })

	for name, t := range tools {
		if t.Calls > 0 {
			t.AvgMs /= float64(t.Calls)
		}
		t.Agents = keys(toolAgents[name])
		t.Outcomes = topN(toolOutcomes[name], 10)
		ov.Tools = append(ov.Tools, *t)
	}
	sort.Slice(ov.Tools, func(i, j int) bool { return ov.Tools[i].Calls > ov.Tools[j].Calls })

	if len(tasks) > 100 {
		tasks = tasks[:100]
	}
	ov.Tasks = tasks
	ov.Timeline = buildTimeline(spans, window, now)
	ov.Feed = BuildFeed(spans, 60)
	return ov
}

func buildTimeline(spans []Span, window time.Duration, now time.Time) []Bucket {
	const n = 48
	size := (window / n).Truncate(time.Minute)
	if size <= 0 {
		size = time.Minute
	}
	start := now.Add(-size * n).Truncate(size)
	buckets := make([]Bucket, n+1)
	for i := range buckets {
		buckets[i].Start = start.Add(time.Duration(i) * size)
	}
	for _, s := range spans {
		i := int(s.Time.Sub(start) / size)
		if i < 0 || i >= len(buckets) {
			continue
		}
		b := &buckets[i]
		switch s.Kind {
		case "agent":
			b.Runs++
			b.Tokens += s.InputTokens + s.OutputTokens
		case "tool":
			b.ToolCalls++
		}
		if s.Operation != "" && s.Failed() {
			b.Errors++
		}
	}
	return buckets
}

// BuildFeed renders the newest spans as one-line, human-readable events.
func BuildFeed(spans []Span, limit int) []FeedItem {
	sorted := make([]Span, 0, len(spans))
	for _, s := range Normalize(spans) {
		if s.Operation != "" && s.Kind != "client" {
			sorted = append(sorted, s)
		}
	}
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Time.After(sorted[j].Time) })
	if len(sorted) > limit {
		sorted = sorted[:limit]
	}
	feed := make([]FeedItem, 0, len(sorted))
	for _, s := range sorted {
		feed = append(feed, FeedItem{
			Time: s.Time, Agent: s.Agent, ConversationID: s.ConversationID,
			Kind: s.Kind, Text: Describe(s), OK: !s.Failed(),
		})
	}
	return feed
}

// Describe summarises what a span says the agent did.
func Describe(s Span) string {
	agent := s.Agent
	if agent == "" {
		agent = "agent"
	}
	switch s.Kind {
	case "agent":
		verb := "finished"
		if s.Failed() {
			verb = "failed"
		}
		msg := fmt.Sprintf("%s %s a run", agent, verb)
		if in := firstLine(s.Input, 90); in != "" {
			msg += fmt.Sprintf(": “%s”", in)
		}
		return msg + fmt.Sprintf(" (%s, %d tokens)", dur(s.DurationMs), s.InputTokens+s.OutputTokens)
	case "llm":
		msg := fmt.Sprintf("%s reasoned with %s", agent, nz(s.Model, "a model"))
		if s.Turn >= 0 {
			msg += fmt.Sprintf(" (turn %d)", s.Turn)
		}
		if len(s.ToolsRequested) > 0 {
			msg += " → wants " + strings.Join(s.ToolsRequested, ", ")
		}
		return msg
	case "tool":
		out := nz(s.Outcome, map[bool]string{true: "error", false: "ok"}[s.Failed()])
		if s.Error != "" {
			out = s.Error
		}
		return fmt.Sprintf("%s called %s → %s (%s)", agent, s.Tool, out, dur(s.DurationMs))
	}
	return agent + ": " + s.Name
}

// FilterSince keeps spans newer than t.
func FilterSince(spans []Span, t time.Time) []Span {
	var out []Span
	for _, s := range spans {
		if s.Time.After(t) {
			out = append(out, s)
		}
	}
	return out
}

func topN(m map[string]int, n int) []NameCount {
	out := make([]NameCount, 0, len(m))
	for k, v := range m {
		out = append(out, NameCount{k, v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Name < out[j].Name
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func firstLine(s string, n int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return truncate(s, n)
}

func dur(ms float64) string {
	if ms >= 1000 {
		return fmt.Sprintf("%.1fs", ms/1000)
	}
	return fmt.Sprintf("%.0fms", ms)
}

func nz(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// Normalize returns a copy of spans where outer invoke_agent spans that wrap another
// invoke_agent in the same trace (e.g. a chat UI's client span) are re-labelled
// "client", so each run is counted once. The innermost (latest-starting) one is the run.
func Normalize(spans []Span) []Span {
	out := append([]Span(nil), spans...)
	inner := map[string]int{} // conv|operation -> index of innermost agent span
	for i, s := range out {
		if s.Kind != "agent" {
			continue
		}
		key := s.ConversationID + "|" + s.OperationID
		j, ok := inner[key]
		if !ok {
			inner[key] = i
			continue
		}
		if s.Time.After(out[j].Time) {
			out[j].Kind = "client"
			inner[key] = i
		} else {
			out[i].Kind = "client"
		}
	}
	return out
}

// FormatWindow renders a window as 7d, 24h, 90m.
func FormatWindow(d time.Duration) string {
	switch {
	case d >= 48*time.Hour && d%(24*time.Hour) == 0:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	case d >= time.Hour && d%time.Hour == 0:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
}

// percentile returns the p-th percentile (nearest rank) of vals, or 0 when empty.
func percentile(vals []float64, p float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	v := append([]float64(nil), vals...)
	sort.Float64s(v)
	rank := int(math.Ceil(p/100*float64(len(v)))) - 1
	if rank < 0 {
		rank = 0
	}
	return v[rank]
}
