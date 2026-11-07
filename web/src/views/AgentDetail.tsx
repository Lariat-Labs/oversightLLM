import { useEffect, useMemo, useState } from "react";
import { TimePicker, useApp } from "../App";
import { fmtAgo, fmtDur, fmtNum, normalizeOverview, useLoad, type Agent, type Conversation, type EvalCase, type FoundryEval, type Intent, type Overview, type Task } from "../api";
import { PassBar, TrendChart, type Metric } from "../components/charts";
import { Icon } from "../components/icons";
import { TasksTable } from "../components/TasksTable";
import { TraceEvalButton } from "../components/TraceEvalButton";
import { AgentAvatar, Empty, Panel, Segmented, Skeleton, Status } from "../components/ui";
import { Single } from "./Overview";

/** One agent: health, activity, tools, judged outcomes, definition and evals. */
export function AgentDetail({ id }: { id: string }) {
  const { api, go, agentIds, range, lastEvent } = useApp();
  const data = useLoad<{ agent: Agent; overview: Overview }>(() => api.get(`/api/agents/${encodeURIComponent(id)}?window=${range.window}`), [id, range.window]);
  const agent = data.data?.agent;
  const ov = data.data?.overview ? normalizeOverview(data.data.overview) : undefined;
  const trace = agent?.traceId || id;
  const tasks = useLoad<Task[]>(() => api.get(`/api/tasks?limit=200&window=${range.window}&agent=${encodeURIComponent(trace)}`), [trace, range.window]);
  const intents = useLoad<Intent[]>(() => api.get(`/api/intents?agent=${encodeURIComponent(trace)}`), [trace]);
  const cases = useLoad<EvalCase[]>(() => api.get(`/api/evals?agent=${encodeURIComponent(agent?.name || id)}`), [agent?.name]);
  const foundry = useLoad<FoundryEval[]>(() => api.get(`/api/evals/foundry?agent=${encodeURIComponent(agent?.name || id)}`), [agent?.name]);
  const [instructions, setInstructions] = useState<string | null>(null);
  useEffect(() => { if (lastEvent?.type === "overview") { data.reload(); tasks.reload(); } }, [lastEvent]); // eslint-disable-line react-hooks/exhaustive-deps

  // Instructions aren't in the Foundry record for external agents; read them from the latest captured run.
  useEffect(() => {
    const latest = (tasks.data || []).find((t) => t.input);
    if (!latest || instructions !== null) return;
    api.get<{ conversation: Conversation }>(`/api/conversations/${encodeURIComponent(latest.conversationId)}`)
      .then((r) => setInstructions(r.conversation.systemPrompt || "")).catch(() => setInstructions(""));
  }, [tasks.data]); // eslint-disable-line react-hooks/exhaustive-deps

  const stats = ov?.agents[0];
  const tl = stats?.timeline || [];
  const buckets = tl.map((b) => b.start);
  const metrics: Metric[] = useMemo(() => !stats ? [] : [
    { key: "toolCalls", label: "Tool calls", values: tl.map((b) => b.toolCalls) },
    { key: "runs", label: "Runs finished", values: tl.map((b) => b.runs) },
    { key: "tokens", label: "Tokens", values: tl.map((b) => b.tokens) },
    { key: "errors", label: "Errors", values: tl.map((b) => b.errors) },
  ], [stats, tl]);
  const [metricKey, setMetricKey] = useState("toolCalls");
  const metric = metrics.find((m) => m.key === metricKey) || metrics[0];

  const judged = useMemo(() => {
    const c = new Map<string, number>();
    (intents.data || []).forEach((i) => c.set(i.status, (c.get(i.status) || 0) + 1));
    return [...c.entries()].sort((a, b) => b[1] - a[1]);
  }, [intents.data]);
  const categories = useMemo(() => {
    const c = new Map<string, number>();
    (intents.data || []).forEach((i) => i.category && c.set(i.category, (c.get(i.category) || 0) + 1));
    return [...c.entries()].sort((a, b) => b[1] - a[1]).slice(0, 6);
  }, [intents.data]);
  const caseCounts = useMemo(() => {
    const c: Record<string, number> = { suggested: 0, approved: 0, uploaded: 0, rejected: 0 };
    (cases.data || []).forEach((x) => c[x.status]++);
    return c;
  }, [cases.data]);
  const runs = (foundry.data || []).flatMap((e) => (e.runs || []).map((r) => ({ ...r, mode: e.metadata?.mode || "" }))).sort((a, b) => b.created_at - a.created_at);

  if (data.error) {
    return <div className="panel"><Empty icon="bot" title="Agent not found" action={<button className="btn" onClick={() => go({ view: "agents" })}>Back to agents</button>}>{data.error}</Empty></div>;
  }
  const all = agentIds.length ? agentIds : [trace];
  const toolMax = Math.max(1, ...(ov?.tools || []).map((t) => t.calls));
  const errRate = stats && stats.toolCalls ? (stats.toolErrors / stats.toolCalls) * 100 : 0;
  const active = stats && Date.now() - new Date(stats.lastSeen).getTime() < 3600_000;

  return (
    <>
      <div className="crumb"><button onClick={() => go({ view: "agents" })}>Agents</button><Icon name="chevronRight" size={12} /><span>{id}</span></div>
      <div className="page-head">
        <div className="agent-title grow">
          <AgentAvatar id={trace} all={all} size="lg" />
          <div style={{ minWidth: 0 }}>
            <h1>{agent?.name || id}</h1>
            <div className="agent-meta">
              {agent ? <span className="tag">{agent.kind}{agent.version ? ` · v${agent.version}` : ""}</span> : <Skeleton h={18} w={120} />}
              {stats ? (active ? <Status status="running" label="Active" /> : <span>Last seen {fmtAgo(stats.lastSeen)}</span>) : agent && <span>No telemetry in this range</span>}
              {agent?.inFoundry === false && <span className="tag">not registered in Foundry</span>}
            </div>
          </div>
        </div>
        <button className="btn" onClick={() => go({ view: "search", q: `agent=${trace}` })}><Icon name="search" size={13} /> Search runs</button>
        <button className="btn" onClick={() => go({ view: "evals", agent: agent?.name || id })}><Icon name="flask" size={13} /> Evals</button>
        <TimePicker />
      </div>
      {agent?.description && <p className="ink2" style={{ maxWidth: 1000, marginTop: -6 }}>{agent.description}</p>}

      <div className="singles">
        {!data.data ? [0, 1, 2, 3, 4].map((i) => <div key={i} className="panel single" style={{ gap: 8 }}><Skeleton h={12} w={80} /><Skeleton h={28} w={60} /></div>) : (
          <>
            <Single label="Runs" value={fmtNum(stats?.runs || 0)} sub={`${stats?.failedRuns || 0} failed · ${stats?.running || 0} running`} values={metrics[1]?.values} buckets={buckets} />
            <Single label="Tool calls" value={fmtNum(stats?.toolCalls || 0)} sub={`${fmtNum(stats?.llmCalls || 0)} model calls`} values={metrics[0]?.values} buckets={buckets} />
            <Single label="Tool error rate" value={<>{errRate.toFixed(1)}<small>%</small></>} sub={`${stats?.toolErrors || 0} failed`} bad={errRate > 5} values={metrics[3]?.values} buckets={buckets} />
            <Single label="Tokens" value={fmtNum((stats?.inputTokens || 0) + (stats?.outputTokens || 0))} sub={stats?.runs ? `${fmtNum(Math.round(((stats.inputTokens + stats.outputTokens) / stats.runs)))} per run` : "—"} values={metrics[2]?.values} buckets={buckets} />
            <Single label="Run duration" value={fmtDur(stats?.avgDurationMs || 0)} sub={`avg · p95 ${fmtDur(stats?.p95DurationMs ?? 0)}`} />
          </>
        )}
      </div>

      <div className="grid g-main">
        <Panel title="Activity over time" icon="activity" actions={metrics.length > 0 && <Segmented value={metricKey} onChange={setMetricKey} options={metrics.map((m) => ({ value: m.key, label: m.label }))} />}>
          {stats && metric ? <TrendChart buckets={buckets} metric={metric} all={metrics} /> : data.data ? <Empty icon="activity" title="No activity in this range" /> : <Skeleton h={240} />}
        </Panel>
        <Panel title={<><span className="judge-tag"><Icon name="cpu" size={11} /> Judge</span> Judged outcomes</>} sub={`${intents.data?.length ?? 0} judged runs`}>
          {!intents.data ? <Skeleton h={120} /> : intents.data.length === 0 ? (
            <p className="note">No runs judged yet. Open a run to judge it, or turn on auto-judge in the top bar.</p>
          ) : (
            <div style={{ display: "grid", gap: 14 }}>
              <div style={{ display: "grid", gap: 6 }}>
                {judged.map(([s, n]) => (
                  <div key={s} className="row" style={{ justifyContent: "space-between" }}>
                    <Status status={s} />
                    <div className="bar-cell" style={{ flex: 1, maxWidth: 200 }}><span className="track"><span className="fill" style={{ display: "block", width: `${(n / intents.data!.length) * 100}%` }} /></span><span className="v">{n}</span></div>
                  </div>
                ))}
              </div>
              {categories.length > 0 && (
                <div>
                  <div className="sub-h">Task categories</div>
                  <div className="row wrap" style={{ gap: 4 }}>{categories.map(([c, n]) => <span key={c} className="tag">{c} · {n}</span>)}</div>
                </div>
              )}
              <div>
                <div className="sub-h">Latest flags</div>
                {intents.data.flatMap((i) => (i.riskFlags || []).map((f) => ({ f, id: i.conversationId }))).slice(0, 3).map((x, i) => (
                  <div key={i} className="flag"><Icon name="flag" size={13} /><button className="link" style={{ color: "inherit" }} onClick={() => go({ view: "conversation", id: x.id, from: { view: "agent", id } })}>{x.f}</button></div>
                ))}
              </div>
            </div>
          )}
        </Panel>
      </div>

      <div className="grid g-2">
        <Panel title="Tool usage" icon="wrench" sub={ov ? `${ov.tools.length} tools used` : ""} bodyClass="flush">
          {!ov ? <div style={{ padding: 12 }}><Skeleton h={120} /></div> : ov.tools.length === 0 ? <Empty icon="wrench" title="No tool calls in this range" /> : (
            <table className="t">
              <thead><tr><th>Tool</th><th>Calls</th><th className="right">Failed</th><th className="right">Avg</th><th>Outcomes</th></tr></thead>
              <tbody>
                {ov.tools.map((t) => (
                  <tr key={t.name} className="click" onClick={() => go({ view: "search", q: `agent=${trace} tool=${t.name}` })}>
                    <td className="mono">{t.name}</td>
                    <td><div className="bar-cell"><span className="track"><span className="fill" style={{ display: "block", width: `${(t.calls / toolMax) * 100}%` }} /></span><span className="v">{t.calls}</span></div></td>
                    <td className={`right num ${t.errors ? "bad-text" : ""}`}>{t.errors}</td>
                    <td className="right num">{fmtDur(t.avgMs)}</td>
                    <td><span className="muted" style={{ fontSize: 12 }}>{t.outcomes.map((o) => `${o.name} ${o.count}`).join(" · ")}</span></td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </Panel>
        <Panel title="Foundry definition" icon="database" bodyClass="flush">
          {!agent ? <div style={{ padding: 12 }}><Skeleton h={120} /></div> : (
            <dl className="kv">
              <dt>Name</dt><dd className="mono">{agent.name}</dd>
              <dt>Kind</dt><dd>{agent.kind}</dd>
              <dt>Version</dt><dd>{agent.version || "—"}{agent.createdAt && agent.inFoundry && <span className="muted"> · created {new Date(agent.createdAt).toLocaleDateString()}</span>}</dd>
              <dt>Trace match</dt><dd className="mono">gen_ai.agent.id = {agent.traceId}</dd>
              {Object.entries(agent.metadata || {}).filter(([k]) => k !== "trace_match").map(([k, v]) => (
                <span key={k} style={{ display: "contents" }}><dt>{k.replace(/_/g, " ")}</dt><dd>{/^https?:/.test(v) ? <a href={v} target="_blank" rel="noreferrer">{v}</a> : v}</dd></span>
              ))}
            </dl>
          )}
        </Panel>
      </div>

      <Panel title="Runs" icon="list" sub={tasks.data ? `${tasks.data.length} in range` : ""} bodyClass="flush"
        actions={<button className="btn sm" onClick={() => go({ view: "search", q: `agent=${trace}` })}><Icon name="search" size={12} /> Open in Search</button>}>
        <TasksTable tasks={tasks.data} loading={tasks.loading} from={{ view: "agent", id }} limit={15} />
      </Panel>

      <div className="grid g-2">
        <Panel title="Evals" icon="flask" bodyClass="flush" actions={<>
          {agent?.inFoundry && <TraceEvalButton small agent={agent.name} onDone={foundry.reload} />}
          <button className="btn sm" onClick={() => go({ view: "evals", agent: agent?.name || id })}>Manage cases</button>
        </>}>
          <div className="row wrap" style={{ gap: 6, padding: "10px 12px" }}>
            <span className="muted" style={{ fontSize: 12 }}>Curated cases:</span>
            <span className="tag">{caseCounts.suggested} to review</span>
            <span className="tag">{caseCounts.approved} approved</span>
            <span className="tag">{caseCounts.uploaded} in Foundry</span>
          </div>
          {runs.length === 0 ? <p className="note" style={{ padding: "0 12px 12px" }}>No Foundry eval runs yet. "Evaluate recent runs" grades this agent's real runs from its traces; they appear under the agent's Evaluations tab in Foundry.</p> : (
            <table className="t">
              <thead><tr><th>Foundry run</th><th>Type</th><th>Result</th><th className="right">Started</th><th /></tr></thead>
              <tbody>
                {runs.slice(0, 6).map((r) => (
                  <tr key={r.id}>
                    <td><div className="trunc" style={{ maxWidth: 220 }} title={r.name}>{r.name}</div>{r.status !== "completed" && <Status status={r.status} />}</td>
                    <td><span className="tag">{r.mode === "traces" ? "Recent runs" : "Curated cases"}</span></td>
                    <td><PassBar counts={r.result_counts} /></td>
                    <td className="right muted nowrap">{fmtAgo(new Date(r.created_at * 1000).toISOString())}</td>
                    <td className="right">{r.report_url && <a href={r.report_url} target="_blank" rel="noreferrer">Report <Icon name="external" size={11} /></a>}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </Panel>
        <Panel title="Instructions" icon="terminal" sub="from the latest captured run">
          {instructions === null ? <Skeleton h={100} /> : instructions ? <pre className="pre">{instructions}</pre> : <p className="note">No system instructions captured in telemetry for this agent.</p>}
        </Panel>
      </div>
    </>
  );
}
