import { useMemo, useState, type ReactNode } from "react";
import { TimePicker, useApp } from "../App";
import { fmtAgo, fmtDur, fmtNum, useLoad, type Agent, type JudgeInfo } from "../api";
import { Spark } from "../components/charts";
import { Icon } from "../components/icons";
import { AgentAvatar, Empty, PageHead, Panel, Skeleton, Status } from "../components/ui";
import { Single } from "./Overview";

const ACTIVE_MS = 3600_000;

type Col = { key: string; label: string; right?: boolean; sort?: (a: Agent) => number | string; render: (a: Agent) => ReactNode };

/** Every agent in the Foundry project, joined with its telemetry, plus the judge. */
export function Agents() {
  const { api, go, agentIds, toast, range } = useApp();
  const agents = useLoad<Agent[]>(() => api.get(`/api/agents?window=${range.window}`), [range.window]);
  const judge = useLoad<JudgeInfo>(() => api.get("/api/judge"), []);
  const [sort, setSort] = useState<{ key: string; dir: 1 | -1 }>({ key: "runs", dir: -1 });
  const [filter, setFilter] = useState("");
  const all = agentIds.length ? agentIds : (agents.data || []).map((a) => a.traceId);

  const refresh = async () => {
    try { agents.setData(await api.get<Agent[]>(`/api/agents?refresh=1&window=${range.window}`)); toast({ kind: "good", title: "Agents refreshed from Foundry" }); }
    catch (e: any) { toast({ kind: "error", title: "Refresh failed", body: e.message }); }
  };

  const registerJudge = async () => {
    try {
      await api.post("/api/judge/register");
      await Promise.all([judge.reload(), agents.reload()]);
      toast({ kind: "good", title: "Judge registered in Foundry", body: "It now appears as an external agent, oversight-judge." });
    } catch (e: any) { toast({ kind: "error", title: "Registration failed", body: e.message }); }
  };
  const status = (a: Agent) => !a.stats ? "nodata" : Date.now() - new Date(a.stats.lastSeen).getTime() < ACTIVE_MS ? "active" : "idle";
  const errRate = (a: Agent) => (a.stats && a.stats.toolCalls ? a.stats.toolErrors / a.stats.toolCalls : 0);
  const cols: Col[] = [
    {
      key: "name", label: "Agent", sort: (a) => a.name,
      render: (a) => (
        <div className="row" style={{ gap: 10, alignItems: "flex-start" }}>
          <AgentAvatar id={a.traceId} all={all} />
          <div style={{ minWidth: 0 }}>
            <div className="strong">{a.name}</div>
            <div className="task-sub trunc" style={{ maxWidth: 320 }} title={a.description}>{a.description || "No description"}</div>
          </div>
        </div>
      ),
    },
    { key: "kind", label: "Kind", sort: (a) => a.kind, render: (a) => <span className="tag">{a.kind}{a.version ? ` · v${a.version}` : ""}</span> },
    {
      key: "state", label: "State", sort: (a) => status(a),
      render: (a) => status(a) === "active" ? <Status status="running" label="Active" /> : status(a) === "idle" ? <Status status="queued" label="Idle" /> : <Status status="canceled" label="No data" />,
    },
    { key: "activity", label: "Activity", render: (a) => a.stats ? <Spark mini values={(a.stats.timeline || []).map((b) => b.toolCalls + b.runs)} labels={(a.stats.timeline || []).map((b) => b.start)} format={(v) => `${v} events`} /> : <span className="muted">—</span> },
    { key: "runs", label: "Runs", right: true, sort: (a) => a.stats?.runs ?? -1, render: (a) => a.stats?.runs ?? 0 },
    { key: "tools", label: "Tool calls", right: true, sort: (a) => a.stats?.toolCalls ?? -1, render: (a) => a.stats?.toolCalls ?? 0 },
    {
      key: "err", label: "Tool errors", right: true, sort: errRate,
      render: (a) => a.stats ? <span className={errRate(a) > 0.05 ? "bad-text" : ""}>{(errRate(a) * 100).toFixed(1)}%</span> : "—",
    },
    { key: "tokens", label: "Tokens", right: true, sort: (a) => (a.stats ? a.stats.inputTokens + a.stats.outputTokens : -1), render: (a) => a.stats ? fmtNum(a.stats.inputTokens + a.stats.outputTokens) : "—" },
    { key: "avg", label: "Avg run", right: true, sort: (a) => a.stats?.avgDurationMs ?? -1, render: (a) => a.stats ? fmtDur(a.stats.avgDurationMs) : "—" },
    { key: "p95", label: "p95 run", right: true, sort: (a) => a.stats?.p95DurationMs ?? -1, render: (a) => a.stats ? fmtDur(a.stats.p95DurationMs ?? 0) : "—" },
    { key: "seen", label: "Last seen", right: true, sort: (a) => (a.stats ? +new Date(a.stats.lastSeen) : 0), render: (a) => <span className="muted nowrap">{a.stats ? fmtAgo(a.stats.lastSeen) : "—"}</span> },
    { key: "evals", label: "Eval cases", right: true, sort: (a) => a.evalCases, render: (a) => a.evalCases },
  ];

  const rows = useMemo(() => {
    const col = cols.find((c) => c.key === sort.key);
    const list = (agents.data || []).filter((a) => !filter || (a.name + a.description + a.kind).toLowerCase().includes(filter.toLowerCase()));
    if (col?.sort) list.sort((x, y) => { const a = col.sort!(x), b = col.sort!(y); return (a < b ? -1 : a > b ? 1 : 0) * sort.dir; });
    return list;
  }, [agents.data, sort, filter]); // eslint-disable-line react-hooks/exhaustive-deps

  const list = agents.data || [];
  const registered = list.filter((a) => a.inFoundry).length;
  const active = list.filter((a) => status(a) === "active").length;
  const runs = list.reduce((n, a) => n + (a.stats?.runs || 0), 0);
  const calls = list.reduce((n, a) => n + (a.stats?.toolCalls || 0), 0);
  const errs = list.reduce((n, a) => n + (a.stats?.toolErrors || 0), 0);

  return (
    <>
      <PageHead title="Agents" sub="Agents registered in the Foundry project, joined with their telemetry"
        actions={<><button className="btn" onClick={refresh}><Icon name="refresh" size={13} /> Refresh from Foundry</button><TimePicker /></>} />

      {!agents.data ? <div className="singles">{[0, 1, 2, 3].map((i) => <div key={i} className="panel single" style={{ gap: 8 }}><Skeleton h={12} w={80} /><Skeleton h={28} w={60} /></div>)}</div> : (
        <div className="singles">
          <Single label="Registered in Foundry" value={registered} sub={list.length > registered ? `${list.length - registered} unregistered emitting telemetry` : "all agents with telemetry are registered"} />
          <Single label="Active in the last hour" value={<>{active}<small>/ {list.length}</small></>} sub={active ? "emitting spans now" : "no recent spans"} />
          <Single label="Runs" value={fmtNum(runs)} sub={range.label.toLowerCase()} />
          <Single label="Tool error rate" value={<>{calls ? ((errs / calls) * 100).toFixed(1) : "0.0"}<small>%</small></>} sub={`${errs} of ${fmtNum(calls)} tool calls`} bad={calls > 0 && errs / calls > 0.05} />
        </div>
      )}

      <Panel title="Agents" icon="bot" sub={agents.data ? `${rows.length} shown` : ""} bodyClass="flush"
        actions={<div className="input-icon" style={{ width: 240 }}><Icon name="search" size={13} /><input className="input" placeholder="Filter agents…" value={filter} onChange={(e) => setFilter(e.target.value)} /></div>}>
        {agents.error && <div className="banner error" style={{ margin: 12 }}><Icon name="alert" /><div>{agents.error}</div></div>}
        {!agents.data && agents.loading ? <div style={{ padding: 12, display: "grid", gap: 8 }}>{[0, 1, 2].map((i) => <Skeleton key={i} h={36} />)}</div>
          : rows.length === 0 ? <Empty icon="bot" title="No agents">Register agents in your Foundry project and they'll show up here.</Empty>
          : (
            <div className="table-wrap">
              <table className="t">
                <thead>
                  <tr>
                    {cols.map((c) => (
                      <th key={c.key} className={`${c.right ? "right" : ""} ${c.sort ? "sortable" : ""}`} aria-sort={sort.key === c.key ? (sort.dir === 1 ? "ascending" : "descending") : "none"}
                        onClick={() => c.sort && setSort((s) => ({ key: c.key, dir: s.key === c.key ? (s.dir === 1 ? -1 : 1) : -1 }))}>
                        {c.label}<span className="arr">{sort.key === c.key ? (sort.dir === 1 ? "▲" : "▼") : ""}</span>
                      </th>
                    ))}
                  </tr>
                </thead>
                <tbody>
                  {rows.map((a) => (
                    <tr key={a.id} className="click" onClick={() => go({ view: "agent", id: a.name })}>
                      {cols.map((c) => <td key={c.key} className={c.right ? "right num" : ""}>{c.render(a)}</td>)}
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
      </Panel>

      <Panel title={<><span className="judge-tag"><Icon name="cpu" size={11} /> System</span> Oversight judge</>}
        sub={judge.data ? (judge.data.foundryAgent ? `Foundry agent ${judge.data.agentId}` : "not registered in Foundry") : ""}
        actions={judge.data && <>
          {judge.data.foundryAgent && <button className="btn sm" onClick={() => go({ view: "agent", id: judge.data!.agentId })}>Open agent</button>}
          {!judge.data.foundryAgent && <button className="btn sm primary" onClick={registerJudge}>Register in Foundry</button>}
        </>}
        bodyClass="flush">
        {!judge.data ? <div style={{ padding: 12 }}><Skeleton h={80} /></div> : (
          <div className="grid g-2" style={{ gap: 0 }}>
            <dl className="kv" style={{ borderRight: "1px solid var(--border)" }}>
              <dt>What it is</dt><dd>The model that judges runs, summarises the fleet and suggests evals. It runs inside the Oversight API and calls the deployment directly.</dd>
              <dt>Foundry</dt><dd>{judge.data.foundryAgent ? <Status status="completed" label={`Registered · ${judge.data.agentId} (external)`} /> : <Status status="queued" label="Not registered" />}</dd>
              <dt>Tracing</dt><dd>{judge.data.traced ? <Status status="completed" label={`App Insights · gen_ai.agent.id = ${judge.data.agentId}`} /> : <Status status="queued" label="Off: set APPLICATIONINSIGHTS_CONNECTION_STRING" />}</dd>
              <dt>Deployment</dt><dd className="mono">{judge.data.deployment} <span className="muted">· graders: {judge.data.graderModel}</span></dd>
              <dt>Auto-judge</dt><dd>{judge.data.autoJudge ? <Status status="running" label="On" /> : <Status status="queued" label="Off: on demand only" />}</dd>
            </dl>
            <dl className="kv">
              <dt>Calls</dt><dd className="num">{judge.data.stats.calls} <span className="muted">since API start {fmtAgo(judge.data.stats.since)}</span>{judge.data.stats.errors > 0 && <span className="bad-text"> · {judge.data.stats.errors} failed</span>}</dd>
              <dt>Tokens</dt><dd className="num">{fmtNum(judge.data.stats.inputTokens + judge.data.stats.outputTokens)} <span className="muted">({fmtNum(judge.data.stats.inputTokens)} in · {fmtNum(judge.data.stats.outputTokens)} out)</span></dd>
              <dt>By task</dt><dd>{Object.keys(judge.data.stats.byKind).length ? Object.entries(judge.data.stats.byKind).map(([k, v]) => <span key={k} className="tag" style={{ marginRight: 4 }}>{k.replace("_", " ")} · {v}</span>) : <span className="muted">no calls yet</span>}</dd>
              <dt>Last call</dt><dd>{judge.data.stats.calls ? fmtAgo(judge.data.stats.lastCall) : "—"}{judge.data.stats.lastError && <div className="bad-text" style={{ fontSize: 12 }}>{judge.data.stats.lastError}</div>}</dd>
              <dt>Judged runs</dt><dd className="num">{judge.data.cachedIntents} <span className="muted">cached on disk</span></dd>
            </dl>
          </div>
        )}
      </Panel>
    </>
  );
}
