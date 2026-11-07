"""Generate api/internal/server/openapi.json (run: make openapi). Edit the paths and schemas here, not the JSON."""
import json, os, sys

VERSION = open(os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "VERSION")).read().strip()
R = lambda name: {"$ref": f"#/components/schemas/{name}"}
arr = lambda x: {"type": "array", "items": x}
S = lambda **kw: {"type": "string", **kw}
I = {"type": "integer"}
N = {"type": "number"}
B = {"type": "boolean"}
T = {"type": "string", "format": "date-time"}
def obj(props, req=None, desc=None):
    o = {"type": "object", "properties": props}
    if req: o["required"] = req
    if desc: o["description"] = desc
    return o
def ok(schema, desc="OK"):
    return {"200": {"description": desc, "content": {"application/json": {"schema": schema}}}, "default": {"$ref": "#/components/responses/Error"}}
def body(schema, required=True):
    return {"required": required, "content": {"application/json": {"schema": schema}}}
qp = lambda name, desc, schema=None: {"name": name, "in": "query", "required": False, "description": desc, "schema": schema or S()}
pp = lambda name, desc: {"name": name, "in": "path", "required": True, "description": desc, "schema": S()}
WINDOW = qp("window", "Time window as a Go duration, e.g. 1h, 24h, 168h. Defaults to OVERSIGHT_LOOKBACK.")
AGENT = qp("agent", "Filter to one agent (gen_ai.agent.id).")

schemas = {
  "Error": obj({"error": S()}, ["error"]),
  "NameCount": obj({"name": S(), "count": I}),
  "Bucket": obj({"start": T, "runs": I, "toolCalls": I, "errors": I, "tokens": I}),
  "Totals": obj({k: I for k in ["runs","running","failedRuns","conversations","llmCalls","toolCalls","toolErrors","inputTokens","outputTokens","activeAgents"]}),
  "AgentStats": obj({"id": S(), "runs": I, "running": I, "failedRuns": I, "toolCalls": I, "toolErrors": I, "llmCalls": I,
      "inputTokens": I, "outputTokens": I, "avgDurationMs": N, "p95DurationMs": N, "lastSeen": T,
      "topTools": arr(R("NameCount")), "users": arr(S()), "timeline": arr(R("Bucket"))}),
  "ToolStats": obj({"name": S(), "calls": I, "errors": I, "avgMs": N, "agents": arr(S()), "outcomes": arr(R("NameCount"))}),
  "Task": obj({"conversationId": S(), "agent": S(), "started": T, "lastActivity": T, "durationMs": N,
      "status": S(enum=["running","completed","failed","incomplete"]), "input": S(), "outputPreview": S(),
      "toolSequence": arr(S()), "toolErrors": I, "turns": I, "tokens": I, "user": S(), "stopReason": S(), "model": S(),
      "intent": R("Intent")}, desc="One agent run (conversation id + trace)."),
  "FeedItem": obj({"time": T, "agent": S(), "conversationId": S(), "kind": S(), "text": S(), "ok": B}),
  "Overview": obj({"generatedAt": T, "window": S(), "totals": R("Totals"), "agents": arr(R("AgentStats")), "tools": arr(R("ToolStats")),
      "tasks": arr(R("Task")), "timeline": arr(R("Bucket")), "feed": arr(R("FeedItem"))}),
  "Insight": obj({"headline": S(), "themes": arr(obj({"theme": S(), "agents": arr(S()), "count": I})),
      "agents": arr(obj({"agent": S(), "doing": S(), "health": S(enum=["good","watch","bad"])})), "concerns": arr(S()), "generatedAt": T, "model": S()}),
  "Intent": obj({"conversationId": S(), "agent": S(), "intent": S(), "category": S(), "trajectory": S(), "direction": S(),
      "status": S(enum=["on_track","drifting","stuck","failed","completed"]), "riskFlags": arr(S()), "confidence": N,
      "summary": S(), "model": S(), "judgedAt": T, "cacheKey": S()}, desc="The judge's reading of one conversation."),
  "Agent": obj({"id": S(), "name": S(), "traceId": S(description="gen_ai.agent.id its spans carry"), "kind": S(), "version": S(),
      "description": S(), "state": S(), "metadata": {"type": "object", "additionalProperties": S()}, "createdAt": T,
      "inFoundry": B, "stats": R("AgentStats"), "evalCases": I}),
  "Step": obj({"id": S(), "parentId": S(), "time": T, "kind": S(enum=["user","llm","tool","subcall","http","answer","log"]), "title": S(),
      "detail": S(), "durationMs": N, "ok": B, "turn": I, "depth": I, "tokens": I, "attributes": {"type": "object", "additionalProperties": S()}}),
  "Conversation": obj({"id": S(), "agent": S(), "user": S(), "model": S(), "started": T, "ended": T, "durationMs": N, "status": S(),
      "stopReason": S(), "input": S(), "output": S(), "systemPrompt": S(), "toolDefs": arr(S()), "inputTokens": I, "outputTokens": I,
      "toolCalls": I, "toolErrors": I, "runs": I, "steps": arr(R("Step"))}, desc="Run timeline rebuilt from OpenTelemetry spans."),
  "UploadRef": obj({"evalId": S(), "runId": S(), "dataset": S(), "datasetVersion": S(), "reportUrl": S(), "at": T}),
  "EvalCase": obj({"id": S(), "agent": S(), "query": S(), "expectedBehavior": S(), "expectedTools": arr(S()), "response": S(),
      "actualTools": arr(S()), "sourceConversationId": S(), "category": S(), "rationale": S(), "origin": S(enum=["judge","manual","conversation"]),
      "status": S(enum=["suggested","approved","rejected","uploaded"]), "createdAt": T, "updatedAt": T, "upload": R("UploadRef")}),
  "EvalCaseInput": obj({"agent": S(), "query": S(), "expectedBehavior": S(), "expectedTools": arr(S()), "response": S(),
      "status": S(enum=["suggested","approved","rejected","uploaded"]), "category": S()}, ["agent","query"]),
  "PublishResult": obj({"evalId": S(), "evalName": S(), "runId": S(), "runStatus": S(), "reportUrl": S(), "dataset": S(), "datasetVersion": S(), "cases": I}),
  "FoundryEval": obj({"id": S(), "name": S(), "created_at": I, "metadata": {"type": "object", "additionalProperties": S()},
      "runs": arr(obj({"id": S(), "eval_id": S(), "name": S(), "status": S(), "created_at": I, "report_url": S(),
        "result_counts": {"type": "object", "additionalProperties": I}}))}),
  "TraceRunResult": obj({"evalId": S(), "evalName": S(), "runId": S(), "runStatus": S(), "reportUrl": S(), "agentId": S(), "lookbackHours": I, "maxTraces": I}),
  "Settings": obj({"autoJudge": B}, ["autoJudge"]),
  "Alert": obj({"conversationId": S(), "agent": S(), "status": S(), "summary": S(), "riskFlags": arr(S()), "at": T}),
  "JudgeInfo": obj({"agentId": S(), "deployment": S(), "graderModel": S(), "endpoint": S(), "foundryAgent": B, "traced": B, "autoJudge": B,
      "cachedIntents": I, "stats": obj({"calls": I, "errors": I, "inputTokens": I, "outputTokens": I,
        "byKind": {"type": "object", "additionalProperties": I}, "lastCall": T, "lastError": S(), "since": T})}),
  "Health": obj({"ok": B, "project": S(), "projectName": S(), "workspaceId": S(), "judgeDeployment": S(), "evalDeployment": S(),
      "openaiEndpoint": S(), "subscribers": I, "azure": {"type": "object"}, "foundry": {"type": "object"},
      "live": obj({"ready": B, "spans": I, "lastPoll": T, "error": S(), "interval": S(), "lookback": S()})}),
}

paths = {
 "/api/health": {"get": {"tags": ["System"], "summary": "Health of the Azure CLI session, Foundry and the telemetry poller", "responses": ok(R("Health"))}},
 "/api/events": {"get": {"tags": ["System"], "summary": "Server-Sent Events stream",
   "description": "text/event-stream. Event types: status, overview, activity, insight, intent, alert, settings, evals, evals.uploaded, heartbeat. Each data line is JSON `{type, agent?, conversationId?, data}`.",
   "parameters": [AGENT, qp("conversation", "Only events for this conversation id.")],
   "responses": {"200": {"description": "Event stream", "content": {"text/event-stream": {"schema": S()}}}}}},
 "/metrics": {"get": {"tags": ["System"], "summary": "Prometheus metrics for the Oversight service itself",
   "description": "Text exposition format 0.0.4. Service health only (HTTP, Azure calls, poller, judge usage, auto-judge, eval uploads, trace export); fleet activity lives in App Insights and /api/overview.",
   "responses": {"200": {"description": "Metrics", "content": {"text/plain": {"schema": S()}}}}}},
 "/api/openapi.json": {"get": {"tags": ["System"], "summary": "This OpenAPI document", "responses": {"200": {"description": "OpenAPI 3.1", "content": {"application/json": {"schema": {"type": "object"}}}}}}},
 "/api/docs": {"get": {"tags": ["System"], "summary": "Interactive API docs (Swagger UI)", "responses": {"200": {"description": "HTML page", "content": {"text/html": {"schema": S()}}}}}},
 "/api/overview": {"get": {"tags": ["Activity"], "summary": "Fleet overview: totals, agents, tools, tasks, timeline, live feed", "parameters": [AGENT, WINDOW], "responses": ok(R("Overview"))}},
 "/api/overview/insight": {
   "get": {"tags": ["Judge"], "summary": "Latest judge summary of the fleet (null if none yet)", "responses": ok({"oneOf": [R("Insight"), {"type": "null"}]})},
   "post": {"tags": ["Judge"], "summary": "Ask the judge to summarise the fleet now (one model call)", "parameters": [WINDOW], "responses": ok(R("Insight"))}},
 "/api/tasks": {"get": {"tags": ["Activity"], "summary": "Runs in the window, newest first, with cached judge intent",
   "parameters": [AGENT, WINDOW, qp("status", "running | completed | failed | incomplete"), qp("limit", "Max rows (default 200)", I)], "responses": ok(arr(R("Task")))}},
 "/api/conversations/{id}": {"get": {"tags": ["Activity"], "summary": "Run timeline for a conversation", "parameters": [pp("id", "gen_ai.conversation.id")],
   "responses": ok(obj({"conversation": R("Conversation"), "intent": {"oneOf": [R("Intent"), {"type": "null"}]}}))}},
 "/api/conversations/{id}/intent": {"post": {"tags": ["Judge"], "summary": "Judge a conversation's intent, direction and risk flags (cached)",
   "parameters": [pp("id", "gen_ai.conversation.id"), qp("force", "1 to ignore the cache", S(enum=["1"]))], "responses": ok(R("Intent"))}},
 "/api/intents": {"get": {"tags": ["Judge"], "summary": "All cached judge intents", "parameters": [AGENT], "responses": ok(arr(R("Intent")))}},
 "/api/agents": {"get": {"tags": ["Agents"], "summary": "Foundry agents joined with telemetry (plus unregistered agents that emit spans)",
   "parameters": [WINDOW, qp("refresh", "1 to re-read agents from Foundry", S(enum=["1"]))], "responses": ok(arr(R("Agent")))}},
 "/api/agents/{id}": {"get": {"tags": ["Agents"], "summary": "One agent with its filtered overview", "parameters": [pp("id", "Agent name or trace id"), WINDOW],
   "responses": ok(obj({"agent": R("Agent"), "overview": R("Overview")}))}},
 "/api/judge": {"get": {"tags": ["Judge"], "summary": "Judge deployment, Foundry registration, tracing and usage", "responses": ok(R("JudgeInfo"))}},
 "/api/judge/register": {"post": {"tags": ["Judge"], "summary": "Register (or add a version of) the judge as a Foundry external agent", "responses": ok({"type": "object"})}},
 "/api/settings": {
   "get": {"tags": ["System"], "summary": "Runtime switches", "responses": ok(R("Settings"))},
   "put": {"tags": ["System"], "summary": "Change runtime switches (e.g. turn auto-judge on)", "requestBody": body(R("Settings")), "responses": ok(R("Settings"))}},
 "/api/alerts": {"get": {"tags": ["Judge"], "summary": "Runs the auto-judge marked drifting, stuck or failed (in memory)", "responses": ok(arr(R("Alert")))}},
 "/api/evals": {
   "get": {"tags": ["Evals"], "summary": "Eval cases", "parameters": [qp("agent", "Agent name")], "responses": ok(arr(R("EvalCase")))},
   "post": {"tags": ["Evals"], "summary": "Create a case (defaults to approved)", "requestBody": body(R("EvalCaseInput")),
     "responses": {"201": {"description": "Created", "content": {"application/json": {"schema": R("EvalCase")}}}, "default": {"$ref": "#/components/responses/Error"}}}},
 "/api/evals/{id}": {
   "put": {"tags": ["Evals"], "summary": "Update a case (partial; e.g. {\"status\":\"approved\"})", "parameters": [pp("id", "Case id")], "requestBody": body(R("EvalCaseInput")), "responses": ok(R("EvalCase"))},
   "delete": {"tags": ["Evals"], "summary": "Delete a case", "parameters": [pp("id", "Case id")], "responses": {"204": {"description": "Deleted"}, "default": {"$ref": "#/components/responses/Error"}}}},
 "/api/evals/from-conversation/{id}": {"post": {"tags": ["Evals"], "summary": "Draft a regression case from a real run", "parameters": [pp("id", "Conversation id")],
   "requestBody": body(obj({"expectedBehavior": S()}), False),
   "responses": {"201": {"description": "Created", "content": {"application/json": {"schema": R("EvalCase")}}}, "default": {"$ref": "#/components/responses/Error"}}}},
 "/api/evals/suggest": {"post": {"tags": ["Evals"], "summary": "Judge proposes cases from the agent's recent runs (saved as suggested)",
   "requestBody": body(obj({"agent": S(), "count": {"type": "integer", "minimum": 1, "maximum": 20, "default": 5}, "conversationIds": arr(S())}, ["agent"])),
   "responses": ok(arr(R("EvalCase")))}},
 "/api/evals/upload": {"post": {"tags": ["Evals"], "summary": "Upload approved cases to Foundry: dataset version + eval + run",
   "description": "Writes to the Foundry project: a new version of dataset `oversight-<agent>-evals`, the eval `oversight-<agent>` (created once), and an eval run graded on each case's logged response.",
   "requestBody": body(obj({"agent": S(), "caseIds": arr(S())}, ["agent"])), "responses": ok(R("PublishResult"))}},
 "/api/evals/traces": {"post": {"tags": ["Evals"], "summary": "Grade an agent's recent runs from its App Insights traces (shows under the agent in Foundry)",
   "description": "Creates (once) the eval `oversight-traces-<agent>` with intent resolution, task adherence and tool call accuracy graders, then starts a run with data source `azure_ai_traces` for the agent's gen_ai.agent.id. Only runs whose spans record gen_ai.input/output messages are graded.",
   "requestBody": body(obj({"agent": S(), "lookbackHours": {"type": "integer", "default": 168, "maximum": 720}, "maxTraces": {"type": "integer", "default": 50, "maximum": 500}}, ["agent"])),
   "responses": ok(R("TraceRunResult"))}},
 "/api/evals/foundry": {"get": {"tags": ["Evals"], "summary": "Foundry evals and their recent runs", "parameters": [qp("agent", "Agent name")], "responses": ok(arr(R("FoundryEval")))}},
}

spec = {
  "openapi": "3.1.0",
  "info": {"title": "Oversight API", "version": VERSION,
    "description": "Watch Microsoft Foundry agents through their Application Insights telemetry, read runs step by step, ask a judge model for intent, and publish eval cases to Foundry.\n\nAuth: none. The API is single-user, listens on 127.0.0.1, and calls Azure as whoever ran `az login`."},
  "servers": [{"url": "/", "description": "This server"}],
  "tags": [{"name": n} for n in ["Activity", "Agents", "Judge", "Evals", "System"]],
  "paths": paths,
  "components": {"schemas": schemas, "responses": {"Error": {"description": "Error", "content": {"application/json": {"schema": R("Error")}}}}},
}
json.dump(spec, open(sys.argv[1], "w"), indent=2)
print(len(paths), "paths")
