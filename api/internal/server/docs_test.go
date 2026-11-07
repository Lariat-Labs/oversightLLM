package server

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Lariat-Labs/oversightLLM/api/internal/activity"
	"github.com/Lariat-Labs/oversightLLM/api/internal/config"
)

// Every registered route must be documented in openapi.json, and vice versa.
func TestOpenAPICoversRoutes(t *testing.T) {
	var spec struct {
		OpenAPI string                            `json:"openapi"`
		Paths   map[string]map[string]interface{} `json:"paths"`
	}
	if err := json.Unmarshal(openapiJSON, &spec); err != nil {
		t.Fatalf("openapi.json is invalid: %v", err)
	}
	if !strings.HasPrefix(spec.OpenAPI, "3.") {
		t.Fatalf("unexpected openapi version %q", spec.OpenAPI)
	}
	s := &Server{Cfg: &config.Config{}}
	s.Routes()
	documented := map[string]bool{}
	for path, ops := range spec.Paths {
		for method := range ops {
			documented[strings.ToUpper(method)+" "+path] = true
		}
	}
	registered := map[string]bool{}
	for _, p := range s.patterns {
		registered[p] = true
		if !documented[p] {
			t.Errorf("route %q is not in openapi.json (edit scripts/gen_openapi.py, then make openapi)", p)
		}
	}
	for d := range documented {
		if !registered[d] {
			t.Errorf("openapi.json documents %q but no such route is registered", d)
		}
	}
}

// Requests are counted under their route pattern, not the raw path.
func TestMetricsUseRoutePatterns(t *testing.T) {
	s := &Server{Cfg: &config.Config{CORSOrigins: []string{"*"}}}
	h := s.Routes()
	for _, path := range []string{"/api/openapi.json", "/nope"} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", path, nil))
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	out := rec.Body.String()
	for _, want := range []string{
		`oversight_http_requests_total{route="GET /api/openapi.json",code="200"}`,
		`oversight_http_requests_total{route="unmatched",code="404"}`,
		"# TYPE oversight_judge_tokens_total counter",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("metrics missing %q", want)
		}
	}
}

// An agent with no tools/tasks must get [] (not null) lists, or the UI breaks.
func TestFilterOverviewReturnsEmptyLists(t *testing.T) {
	ov := filterOverview(activity.Overview{Tools: []activity.ToolStats{{Name: "x", Agents: []string{"other"}}}}, "oversight-judge")
	b, _ := json.Marshal(ov)
	for _, k := range []string{`"tools":[]`, `"tasks":[]`, `"feed":[]`, `"agents":[]`} {
		if !strings.Contains(string(b), k) {
			t.Errorf("want %s in %s", k, b)
		}
	}
}
