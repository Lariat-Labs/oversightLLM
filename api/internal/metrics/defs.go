package metrics

// Service metrics, shared across packages. Gauges that read live state are
// registered by the server at startup (see server.RegisterGauges).
var (
	HTTPRequests = Default.NewCounter("oversight_http_requests_total",
		"API requests by method, route pattern and status code.")
	HTTPDuration = Default.NewHistogram("oversight_http_request_duration_seconds",
		"API request latency by route pattern (SSE streams excluded).", DurationBuckets)

	UpstreamRequests = Default.NewCounter("oversight_upstream_requests_total",
		"Calls to Azure by service (foundry, openai, loganalytics, blob, appinsights) and outcome.")
	UpstreamDuration = Default.NewHistogram("oversight_upstream_request_duration_seconds",
		"Latency of calls to Azure by service.", DurationBuckets)

	Polls = Default.NewCounter("oversight_telemetry_polls_total",
		"Activity polls against Log Analytics by outcome.")
	PolledSpans = Default.NewCounter("oversight_telemetry_new_spans_total",
		"GenAI spans seen for the first time by the poller.")

	JudgeCalls = Default.NewCounter("oversight_judge_calls_total",
		"Judge model calls by task (intent, fleet_summary, eval_suggestions) and outcome.")
	JudgeTokens = Default.NewCounter("oversight_judge_tokens_total",
		"Tokens used by the judge by task and direction (input, output).")
	JudgeDuration = Default.NewHistogram("oversight_judge_call_duration_seconds",
		"Judge model call latency by task.", DurationBuckets)

	AutoJudgeAlerts = Default.NewCounter("oversight_autojudge_alerts_total",
		"Runs the auto-judge flagged, by judged status.")
	AutoJudgeSkipped = Default.NewCounter("oversight_autojudge_skipped_total",
		"Finished runs the auto-judge did not queue, by reason (disabled, duplicate, queue_full, self).")

	EvalUploads = Default.NewCounter("oversight_eval_uploads_total",
		"Uploads of eval cases to Foundry by outcome.")
	EvalCasesUploaded = Default.NewCounter("oversight_eval_cases_uploaded_total",
		"Eval cases sent to Foundry.")

	EvalTraceRuns = Default.NewCounter("oversight_eval_trace_runs_total",
		"Trace eval runs started in Foundry (grading an agent's recent runs), by outcome.")

	TraceExports = Default.NewCounter("oversight_trace_export_spans_total",
		"Judge spans exported to App Insights by outcome.")
)
