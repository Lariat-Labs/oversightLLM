package metrics

import (
	"errors"
	"strings"
	"testing"
)

func TestExposition(t *testing.T) {
	r := &Registry{}
	c := r.NewCounter("t_requests_total", "Requests.")
	c.Inc("route", "GET /a", "code", "200")
	c.Add(2, "route", "GET /a", "code", "200")
	c.Inc("route", `GET /"b"`, "code", "500")
	h := r.NewHistogram("t_seconds", "Latency.", []float64{0.1, 1})
	h.Observe(0.05, "svc", "x")
	h.Observe(0.5, "svc", "x")
	h.Observe(5, "svc", "x")
	r.NewGauge("t_up", "Up.", func() []Sample { return []Sample{{Value: 1}, {Labels: []string{"k", "v"}, Value: 2.5}} })

	var b strings.Builder
	r.Write(&b)
	out := b.String()
	for _, want := range []string{
		"# TYPE t_requests_total counter",
		`t_requests_total{route="GET /a",code="200"} 3`,
		`t_requests_total{route="GET /\"b\"",code="500"} 1`,
		"# TYPE t_seconds histogram",
		`t_seconds_bucket{svc="x",le="0.1"} 1`,
		`t_seconds_bucket{svc="x",le="1"} 2`,
		`t_seconds_bucket{svc="x",le="+Inf"} 3`,
		`t_seconds_sum{svc="x"} 5.55`,
		`t_seconds_count{svc="x"} 3`,
		"t_up 1",
		`t_up{k="v"} 2.5`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if Outcome(nil) != "ok" || Outcome(errors.New("x")) != "error" {
		t.Fatal("Outcome wrong")
	}
}
