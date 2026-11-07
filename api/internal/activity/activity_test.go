package activity

import (
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 1, 23, 1, 0, 0, time.UTC)

func ts(sec int) string { return t0.Add(time.Duration(sec) * time.Second).Format(time.RFC3339Nano) }

// Rows shaped like ActivityQuery output (Log Analytics REST returns typed values).
func activityRows() []Row {
	return []Row{
		{"TimeGenerated": ts(0), "Id": "a1", "ParentId": "root", "OperationId": "op1", "Name": "invoke_agent cve-triage",
			"DurationMs": 21000.0, "Success": true, "op": "invoke_agent", "agent": "cve-triage", "conv": "c1",
			"model": "gpt-5-mini", "inTok": 4000.0, "outTok": 1900.0, "stop": "completed", "usr": "lariat",
			"input":  `[{"role":"user","parts":[{"type":"text","content":"Prioritise CVE-2021-44228"}]}]`,
			"output": `[{"role":"assistant","parts":[{"type":"text","content":"P1: patch now"}],"finish_reason":"stop"}]`},
		{"TimeGenerated": ts(1), "Id": "l1", "ParentId": "a1", "OperationId": "op1", "Name": "chat gpt-5-mini",
			"DurationMs": 2000.0, "Success": true, "op": "chat", "agent": "cve-triage", "conv": "c1", "turn": "0",
			"toolsReq": "intel_cve_priority,epss_score", "inTok": 1000.0, "outTok": 400.0},
		{"TimeGenerated": ts(3), "Id": "t1", "ParentId": "a1", "OperationId": "op1", "Name": "execute_tool intel_cve_priority",
			"DurationMs": 800.0, "Success": true, "op": "execute_tool", "agent": "cve-triage", "conv": "c1", "tool": "intel_cve_priority", "outcome": "ok", "turn": "0"},
		{"TimeGenerated": ts(4), "Id": "t2", "ParentId": "a1", "OperationId": "op1", "Name": "execute_tool epss_score",
			"DurationMs": 300.0, "Success": true, "op": "execute_tool", "agent": "cve-triage", "conv": "c1", "tool": "epss_score", "outcome": "upstream_error", "turn": "0"},
		// A second run with no closing invoke_agent span yet (still running).
		{"TimeGenerated": ts(100), "Id": "t3", "ParentId": "a2", "OperationId": "op2", "Name": "execute_tool feeds_news",
			"DurationMs": "50", "Success": "True", "op": "execute_tool", "agent": "threat-brief", "conv": "c2", "tool": "feeds_news", "outcome": "ok", "inTok": "None"},
	}
}

func spans() []Span {
	var out []Span
	for _, r := range activityRows() {
		out = append(out, SpanFromActivityRow(r))
	}
	return out
}

func TestSpanFromActivityRow(t *testing.T) {
	s := spans()
	if s[0].Kind != "agent" || s[0].Input != "Prioritise CVE-2021-44228" || s[0].Output != "P1: patch now" {
		t.Fatalf("agent span parsed wrong: %+v", s[0])
	}
	if s[1].Kind != "llm" || s[1].Turn != 0 || len(s[1].ToolsRequested) != 2 {
		t.Fatalf("llm span parsed wrong: %+v", s[1])
	}
	if !s[3].Failed() || s[2].Failed() {
		t.Fatal("tool outcome should drive failure")
	}
	if s[4].DurationMs != 50 || !s[4].Success || s[4].InputTokens != 0 || s[4].Turn != -1 {
		t.Fatalf("stringly typed row parsed wrong: %+v", s[4])
	}
}

func TestMessagesText(t *testing.T) {
	cases := map[string]string{
		`[{"role":"user","content":"hi"}]`: "hi",
		`[{"role":"assistant","parts":[{"type":"tool_call","name":"kev_lookup","arguments":{"cve":"X"}}]}]`: `[tool call kev_lookup {"cve":"X"}]`,
		`[{"role":"user","parts":[{"type":"text","content":"trunc`:                                          "trunc",
		`[{"role":"assistant","parts":[{"type":"text","content":"Line one\nsays \"hi\" and`:                 "Line one\nsays \"hi\" and",
		`not json at all`: "not json at all",
		"":                "",
	}
	for in, want := range cases {
		if got := MessagesText(in); got != want {
			t.Errorf("MessagesText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuildTasksAndOverview(t *testing.T) {
	now := t0.Add(2 * time.Minute)
	tasks := BuildTasks(spans(), now)
	if len(tasks) != 2 {
		t.Fatalf("want 2 tasks, got %d", len(tasks))
	}
	running, done := tasks[0], tasks[1]
	if running.Status != "running" || running.Agent != "threat-brief" {
		t.Fatalf("expected running threat-brief first, got %+v", running)
	}
	if done.Status != "completed" || strings.Join(done.ToolSequence, ">") != "intel_cve_priority>epss_score" || done.ToolErrors != 1 || done.Turns != 1 {
		t.Fatalf("completed task wrong: %+v", done)
	}
	if later := BuildTasks(spans(), t0.Add(time.Hour)); later[0].Status != "incomplete" {
		t.Fatalf("stale run should be incomplete, got %s", later[0].Status)
	}

	ov := BuildOverview(spans(), 24*time.Hour, now)
	if ov.Totals.Runs != 1 || ov.Totals.Running != 1 || ov.Totals.ToolCalls != 3 || ov.Totals.ToolErrors != 1 || ov.Totals.LLMCalls != 1 {
		t.Fatalf("totals wrong: %+v", ov.Totals)
	}
	if ov.Totals.InputTokens != 4000 || ov.Totals.Conversations != 2 || len(ov.Agents) != 2 {
		t.Fatalf("totals/agents wrong: %+v %d", ov.Totals, len(ov.Agents))
	}
	if ov.Tools[0].Calls != 1 || len(ov.Feed) != 5 || !strings.Contains(ov.Feed[0].Text, "feeds_news") {
		t.Fatalf("tools/feed wrong: %+v / %+v", ov.Tools, ov.Feed[0])
	}
	var tl int
	for _, b := range ov.Timeline {
		tl += b.ToolCalls
	}
	if tl != 3 {
		t.Fatalf("timeline tool calls = %d", tl)
	}
}

func TestBuildConversation(t *testing.T) {
	props := func(p string) string { return p }
	rows := []Row{
		{"TimeGenerated": ts(-1), "Id": "req", "ParentId": "", "OperationId": "op1", "Name": "POST /agents/{agent}/runs", "Kind": "request", "Success": true, "DurationMs": 22000.0, "Properties": `{}`},
		{"TimeGenerated": ts(-1), "Id": "fx", "ParentId": "req", "OperationId": "op1", "Name": "fastapi.endpoint", "Kind": "internal", "Success": true, "Properties": `{}`},
		{"TimeGenerated": ts(0), "Id": "a1", "ParentId": "req", "OperationId": "op1", "Name": "invoke_agent cve-triage", "Kind": "internal", "Success": true, "DurationMs": 21000.0,
			"Properties": props(`{"gen_ai.operation.name":"invoke_agent","gen_ai.agent.id":"cve-triage","gen_ai.conversation.id":"c1",
			"gen_ai.input.messages":"[{\"role\":\"user\",\"parts\":[{\"type\":\"text\",\"content\":\"Prioritise CVE-2021-44228\"}]}]",
			"gen_ai.output.messages":"[{\"role\":\"assistant\",\"parts\":[{\"type\":\"text\",\"content\":\"P1\"}]}]",
			"gen_ai.system_instructions":"[{\"type\":\"text\",\"content\":\"You are a triage analyst\"}]",
			"gen_ai.tool.definitions":"[{\"type\":\"function\",\"name\":\"kev_lookup\"}]",
			"gen_ai.usage.input_tokens":"100","gen_ai.usage.output_tokens":"20","agw.stop_reason":"completed"}`)},
		{"TimeGenerated": ts(1), "Id": "l1", "ParentId": "a1", "OperationId": "op1", "Name": "chat gpt-5-mini", "Kind": "internal", "Success": true,
			"Properties": `{"gen_ai.operation.name":"chat","gen_ai.agent.id":"cve-triage","agw.turn":"0","agw.tools_requested":"kev_lookup"}`},
		{"TimeGenerated": ts(2), "Id": "t1", "ParentId": "a1", "OperationId": "op1", "Name": "execute_tool kev_lookup", "Kind": "internal", "Success": true,
			"Properties": `{"gen_ai.operation.name":"execute_tool","gen_ai.tool.name":"kev_lookup","agw.tool.outcome":"ok","agw.turn":"0"}`},
		{"TimeGenerated": ts(2), "Id": "s1", "ParentId": "t1", "OperationId": "op1", "Name": "secsdk kev.lookup", "Kind": "internal", "Success": true, "Properties": `{"secsdk.outcome":"ok"}`},
		{"TimeGenerated": ts(2), "Id": "h1", "ParentId": "s1", "OperationId": "op1", "Name": "GET www.cisa.gov", "Kind": "http", "Success": true, "Properties": `{"http.response.status_code":"200"}`},
		{"TimeGenerated": ts(3), "Id": "", "ParentId": "t1", "OperationId": "op1", "Name": "log", "Kind": "log", "Message": "cache miss", "Success": true, "Properties": `{}`, "SeverityLevel": 1.0},
	}
	var sp []Span
	for _, r := range rows {
		sp = append(sp, SpanFromDetailRow(r))
	}
	c := BuildConversation("c1", sp, t0.Add(time.Hour))
	if c.Agent != "cve-triage" || c.Status != "completed" || c.Input != "Prioritise CVE-2021-44228" || c.Output != "P1" {
		t.Fatalf("conversation header wrong: %+v", c)
	}
	if c.SystemPrompt != "You are a triage analyst" || len(c.ToolDefs) != 1 || c.ToolCalls != 1 || c.InputTokens != 100 {
		t.Fatalf("conversation details wrong: %+v", c)
	}
	var kinds []string
	depth := map[string]int{}
	for _, s := range c.Steps {
		kinds = append(kinds, s.Kind)
		depth[s.Title] = s.Depth
	}
	got := strings.Join(kinds, ",")
	want := "http,user,llm,tool,subcall,http,log,answer"
	if got != want {
		t.Fatalf("steps = %s, want %s", got, want)
	}
	if depth["Tool: kev_lookup"] != 0 || depth["secsdk kev.lookup"] != 1 || depth["GET www.cisa.gov"] != 2 {
		t.Fatalf("depths wrong: %v", depth)
	}
	if !strings.Contains(c.Compact(5000), "[tool] Tool: kev_lookup") {
		t.Fatal("compact view should list tool steps")
	}
}

func TestKQLEscapes(t *testing.T) {
	q := ConversationQuery(`x" or 1==1 //`)
	if !strings.Contains(q, `"x\" or 1==1 //"`) {
		t.Fatalf("conversation id not escaped: %s", q)
	}
}

func TestNormalizeWrapperAgentSpans(t *testing.T) {
	rows := activityRows()
	// A chat UI's client span wraps the real run in the same trace: earlier start, no input.
	rows = append(rows, Row{"TimeGenerated": ts(-1), "Id": "w1", "OperationId": "op1", "Name": "open-webui chat cve-triage",
		"DurationMs": 22000.0, "Success": true, "op": "invoke_agent", "agent": "cve-triage", "conv": "c1", "inTok": 4000.0, "outTok": 1900.0})
	var sp []Span
	for _, r := range rows {
		sp = append(sp, SpanFromActivityRow(r))
	}
	ov := BuildOverview(sp, 24*time.Hour, t0.Add(2*time.Minute))
	if ov.Totals.Runs != 1 || ov.Totals.InputTokens != 4000 {
		t.Fatalf("wrapper double counted: %+v", ov.Totals)
	}
	tasks := BuildTasks(sp, t0.Add(2*time.Minute))
	if tasks[1].Input != "Prioritise CVE-2021-44228" {
		t.Fatalf("task input lost: %+v", tasks[1])
	}
	for _, f := range ov.Feed {
		if strings.Contains(f.Text, "finished a run") && !strings.Contains(f.Text, "Prioritise") {
			t.Fatalf("wrapper leaked into feed: %s", f.Text)
		}
	}
}

func TestPercentileAndAgentTimeline(t *testing.T) {
	if got := percentile([]float64{10, 20, 30, 40, 100}, 95); got != 100 {
		t.Fatalf("p95 = %v", got)
	}
	if percentile(nil, 95) != 0 {
		t.Fatal("empty percentile should be 0")
	}
	ov := BuildOverview(spans(), 24*time.Hour, t0.Add(2*time.Minute))
	for _, a := range ov.Agents {
		var calls int
		for _, b := range a.Timeline {
			calls += b.ToolCalls
		}
		if calls != a.ToolCalls {
			t.Fatalf("%s timeline has %d tool calls, stats say %d", a.ID, calls, a.ToolCalls)
		}
		if a.ID == "cve-triage" && a.P95DurationMs != 21000 {
			t.Fatalf("p95 = %v", a.P95DurationMs)
		}
	}
}
