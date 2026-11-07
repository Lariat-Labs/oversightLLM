package server

import (
	"net/http"
	"runtime"
	"time"

	"github.com/Lariat-Labs/oversightLLM/api/internal/metrics"
)

var startedAt = time.Now()

// metricsHandler serves Prometheus text format for the Oversight service itself.
func (s *Server) metricsHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	metrics.Default.Write(w)
}

// RegisterGauges adds gauges that read live server state at scrape time. Call once at startup.
func (s *Server) RegisterGauges() {
	g := metrics.Default.NewGauge
	one := func(v float64) []metrics.Sample { return []metrics.Sample{{Value: v}} }
	b2f := func(b bool) float64 {
		if b {
			return 1
		}
		return 0
	}
	g("oversight_build_info", "Build information (value is always 1).", func() []metrics.Sample {
		return []metrics.Sample{{Labels: []string{"go_version", runtime.Version(), "project", s.Cfg.ProjectName(), "judge_deployment", s.Cfg.JudgeDeployment}, Value: 1}}
	})
	g("oversight_uptime_seconds", "Seconds since the API started.", func() []metrics.Sample { return one(time.Since(startedAt).Seconds()) })
	g("oversight_sse_subscribers", "Connected Server-Sent Events clients.", func() []metrics.Sample { return one(float64(s.Hub.Subscribers())) })
	g("oversight_telemetry_ready", "1 once the first activity poll has succeeded.", func() []metrics.Sample { return one(b2f(s.Live.StatusLocked().Ready)) })
	g("oversight_telemetry_spans_in_memory", "GenAI spans held in the rolling window.", func() []metrics.Sample { return one(float64(s.Live.StatusLocked().Spans)) })
	g("oversight_telemetry_last_success_age_seconds", "Seconds since the last successful activity poll (-1 if none yet).", func() []metrics.Sample {
		s.Live.mu.RLock()
		ok := s.Live.lastOK
		s.Live.mu.RUnlock()
		if ok.IsZero() {
			return one(-1)
		}
		return one(time.Since(ok).Seconds())
	})
	g("oversight_autojudge_enabled", "1 when the auto-judge switch is on.", func() []metrics.Sample { return one(b2f(s.currentSettings().AutoJudge)) })
	g("oversight_autojudge_queue_depth", "Runs waiting for the auto-judge.", func() []metrics.Sample { return one(float64(len(s.auto.queue))) })
	g("oversight_judge_cached_intents", "Judged runs cached on disk.", func() []metrics.Sample { return one(float64(len(s.Judge.Cached()))) })
	g("oversight_judge_tracing_enabled", "1 when judge calls are traced to App Insights.", func() []metrics.Sample { return one(b2f(s.Judge.Tracer.Enabled())) })
	g("oversight_eval_cases", "Eval cases by status.", func() []metrics.Sample {
		counts := map[string]float64{"suggested": 0, "approved": 0, "uploaded": 0, "rejected": 0}
		for _, c := range s.Evals.List("") {
			counts[c.Status]++
		}
		out := make([]metrics.Sample, 0, len(counts))
		for _, st := range []string{"suggested", "approved", "uploaded", "rejected"} {
			out = append(out, metrics.Sample{Labels: []string{"status", st}, Value: counts[st]})
		}
		return out
	})
}
