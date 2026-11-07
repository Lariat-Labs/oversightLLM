package evals

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put(Case{Agent: "a"}); err == nil {
		t.Fatal("query should be required")
	}
	c, err := s.Put(Case{Agent: "a", Query: "q", ExpectedTools: []string{"kev_lookup"}})
	if err != nil || c.ID == "" || c.Status != StatusApproved || c.Origin != "manual" {
		t.Fatalf("put: %+v %v", c, err)
	}
	c.Status = StatusRejected
	if _, err := s.Put(c); err != nil {
		t.Fatal(err)
	}
	s2, _ := NewStore(dir)
	got, ok := s2.Get(c.ID)
	if !ok || got.Status != StatusRejected || got.CreatedAt.IsZero() {
		t.Fatalf("reload: %+v", got)
	}
	if len(s2.List("b")) != 0 || len(s2.List("a")) != 1 {
		t.Fatal("agent filter wrong")
	}
}

func TestEvalDefinition(t *testing.T) {
	b, _ := json.Marshal(EvalDefinition(AgentTarget{Name: "cve-triage"}, "gpt-5-mini"))
	s := string(b)
	for _, want := range []string{`"name":"oversight-cve-triage"`, `builtin.intent_resolution`, `{{item.response}}`, `used_expected_tools`, `"include_sample_schema":false`} {
		if !strings.Contains(s, want) {
			t.Errorf("eval definition missing %s", want)
		}
	}
}

func TestTraceEvalDefinition(t *testing.T) {
	b, _ := json.Marshal(TraceEvalDefinition("cve-triage", "gpt-5-mini"))
	s := string(b)
	for _, want := range []string{`"name":"oversight-traces-cve-triage"`, `"scenario":"traces"`, `"mode":"traces"`,
		`builtin.task_adherence`, `builtin.tool_call_accuracy`, `"tool_calls":"{{item.tool_calls}}"`} {
		if !strings.Contains(s, want) {
			t.Errorf("trace eval missing %s", want)
		}
	}
}
