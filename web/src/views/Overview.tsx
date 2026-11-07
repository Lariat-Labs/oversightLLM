import { useEffect, useMemo, useState } from "react";
import { DEFAULT_RANGE, TimePicker, useApp } from "../App";
import { fmtAgo, fmtDur, fmtNum, normalizeOverview, useLoad, type FeedItem, type Insight, type Overview as OverviewData, type Task } from "../api";
import { Spark, TrendChart, type Metric } from "../components/charts";
import { ToolTable } from "../components/ToolTable";
import { Icon, type IconName } from "../components/icons";
import { TasksTable } from "../components/TasksTable";
import { AgentChip, Empty, PageHead, Panel, Segmented, Skeleton, Status } from "../components/ui";

/** The central screen: what every agent is doing, live. */
export function Overview({ agent }: { agent?: string }) {
  const { api, overview: live, go, agentIds, lastEvent, range } = useApp();
  const isDefault = range.key === DEFAULT_RANGE.key && !agent;
  const q = new URLSearchParams({ ...(agent && { agent }), window: range.window }).toString();
  const fetched = useLoad<OverviewData | undefined>(() => (isDefault ? Promise.resolve(undefined) : api.get(`/api/overview?${q}`)), [q, isDefault]);
  const tasks = useLoad<Task[]>(() => api.get(`/api/tasks?limit=10&window=${range.window}${agent ? "&agent=" + encodeURIComponent(agent) : ""}`), [q]);
  useEffect(() => {
    if (lastEvent?.type === "overview") { if (!isDefault) fetched.reload(); tasks.reload(); }
    if (lastEvent?.type === "intent") tasks.reload();
  }, [lastEvent]); // eslint-disable-line react-hooks/exhaustive-deps

  const raw = isDefault ? live : fetched.data;
  const ov = useMemo(() => (raw ? normalizeOverview(raw) : undefined), [raw]);
  const all = agentIds.length ? agentIds : ov?.agents.map((a) => a.id) || [];
  const buckets = ov?.timeline.map((b) => b.start) || [];
  const metrics: Metric[] = useMemo(() => !ov ? [] : [
    { key: "toolCalls", label: "Tool calls", values: ov.timeline.map((b) => b.toolCalls) },
    { key: "runs", label: "Runs finished", values: ov.timeline.map((b) => b.runs) },
    { key: "tokens", label: "Tokens", values: ov.timeline.map((b) => b.tokens) },
    { key: "errors", label: "Errors", values: ov.timeline.map((b) => b.errors) },
  ], [ov]);
  const [metricKey, setMetricKey] = useState("toolCalls");
  const metric = metrics.find((m) => m.key === metricKey) || metrics[0];
  const t = ov?.totals;
  const errRate = t && t.toolCalls ? (t.toolErrors / t.toolCalls) * 100 : 0;

  return (
    <>
      <PageHead
        title={agent ? `Overview · ${agent}` : "Overview"}
        sub={ov ? `Updated ${fmtAgo(ov.generatedAt)} · ${range.label.toLowerCase()}` : "Loading activity from Application Insights…"}
        actions={<>{agent && <button className="btn" onClick={() => go({ view: "overview" })}><Icon name="x" size={13} /> Clear agent</button>}<TimePicker /></>}
      />

      <Alerts />

      {!ov || !t ? <div className="singles">{[0, 1, 2, 3, 4].map((i) => <div key={i} className="panel single" style={{ gap: 8 }}><Skeleton h={12} w={80} /><Skeleton h={28} w={90} /><Skeleton h={24} /></div>)}</div> : (
        <div className="singles">
          <Single label="Runs" value={fmtNum(t.runs)} sub={`${t.running} running · ${t.failedRuns} failed`} values={metrics[1].values} buckets={buckets} />
          <Single label="Tool calls" value={fmtNum(t.toolCalls)} sub={`${fmtNum(t.llmCalls)} model calls`} values={metrics[0].values} buckets={buckets} />
          <Single label="Tool error rate" value={<>{errRate.toFixed(1)}<small>%</small></>} sub={`${t.toolErrors} failed tool calls`} values={metrics[3].values} buckets={buckets} bad={errRate > 5} />
          <Single label="Tokens" value={fmtNum(t.inputTokens + t.outputTokens)} sub={`${fmtNum(t.inputTokens)} in · ${fmtNum(t.outputTokens)} out`} values={metrics[2].values} buckets={buckets} />
          <Single label="Active agents" value={<>{t.activeAgents}<small>/ {ov.agents.length}</small></>} sub={`${fmtNum(t.conversations)} conversations`} />
        </div>
      )}

      {!agent && <InsightPanel />}

      <div className="grid g-main">
        <Panel title="Activity over time" icon="activity"
          actions={metrics.length > 0 && <Segmented value={metricKey} onChange={setMetricKey} options={metrics.map((m) => ({ value: m.key, label: m.label }))} />}>
          {ov && metric ? <TrendChart buckets={buckets} metric={metric} all={metrics} /> : <Skeleton h={240} />}
        </Panel>
        <Panel title="Live events" icon="zap" sub={ov ? `latest ${ov.feed.length}` : ""} bodyClass="flush">
          {!ov ? <div style={{ padding: 12 }}><Skeleton h={240} /></div>
            : ov.feed.length === 0 ? <Empty icon="zap" title="No events in this range" />
            : <Feed items={ov.feed} from={agent} />}
        </Panel>
      </div>

      <Panel title="Recent runs" icon="list" sub="click a row to open the run" bodyClass="flush"
        actions={<button className="btn sm" onClick={() => go({ view: "search", q: agent ? `agent=${agent}` : undefined })}><Icon name="search" size={12} /> Open in Search</button>}>
        <TasksTable tasks={tasks.data} loading={tasks.loading} from={{ view: "overview", agent }} compact />
      </Panel>

      <div className="grid g-2">
        <Panel title="Tool usage" icon="wrench" sub={ov ? `${ov.tools.length} tools · click to search` : ""} bodyClass="flush">
          {!ov ? <div style={{ padding: 12 }}><Skeleton h={160} /></div> : ov.tools.length === 0 ? <Empty icon="wrench" title="No tool calls" />
            : <ToolTable tools={ov.tools} onPick={(t) => go({ view: "search", q: `tool=${t.name}${agent ? ` agent=${agent}` : ""}` })} />}
        </Panel>
        <Panel title="Agents" icon="bot" bodyClass="flush" actions={<button className="btn sm" onClick={() => go({ view: "agents" })}>All agents</button>}>
          {!ov ? <div style={{ padding: 12 }}><Skeleton h={100} /></div> : (
            <table className="t">
              <thead><tr><th>Agent</th><th>Activity</th><th className="right">Runs</th><th className="right">Tool calls</th><th className="right">p95 run</th><th className="right">Last seen</th></tr></thead>
              <tbody>
                {ov.agents.map((a) => (
                  <tr key={a.id} className="click" onClick={() => go({ view: "agent", id: a.id })}>
                    <td><AgentChip id={a.id} all={all} /></td>
                    <td><Spark mini values={(a.timeline || []).map((b) => b.toolCalls + b.runs)} labels={(a.timeline || []).map((b) => b.start)} format={(v) => `${v} events`} /></td>
                    <td className="right num">{a.runs}</td>
                    <td className="right num">{a.toolCalls}</td>
                    <td className="right num">{fmtDur(a.p95DurationMs ?? 0)}</td>
                    <td className="right muted nowrap">{fmtAgo(a.lastSeen)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </Panel>
      </div>
    </>
  );
}

export function Single({ label, value, sub, values, buckets, bad }: {
  label: string; value: React.ReactNode; sub?: React.ReactNode; values?: number[]; buckets?: string[]; bad?: boolean;
}) {
  return (
    <div className="panel single">
      <div className="single-label">{label}</div>
      <div className={`single-value ${bad ? "bad-text" : ""}`}>{value}</div>
      {sub && <div className="single-sub">{sub}</div>}
      {values && buckets && <Spark values={values} labels={buckets} format={(v) => `${fmtNum(v)} ${label.toLowerCase()}`} />}
    </div>
  );
}

const FEED_ICON: Record<string, IconName> = { agent: "checkCircle", llm: "cpu", tool: "wrench" };

function Feed({ items, from }: { items: FeedItem[]; from?: string }) {
  const { go, freshFeed } = useApp();
  return (
    <ul className="feed">
      {items.map((f, i) => (
        <li key={i} className={freshFeed.has(f.time + f.text) ? "fresh" : ""}>
          <time>{new Date(f.time).toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit", second: "2-digit" })}</time>
          <span className={`k ${f.ok ? "" : "bad"}`}><Icon name={f.ok ? FEED_ICON[f.kind] || "activity" : "xCircle"} size={13} /></span>
          <button onClick={() => f.conversationId && go({ view: "conversation", id: f.conversationId, from: { view: "overview", agent: from } })}>
            <FeedText text={f.text} agent={f.agent} />
          </button>
        </li>
      ))}
    </ul>
  );
}

function FeedText({ text, agent }: { text: string; agent: string }) {
  const rest = agent && text.startsWith(agent) ? text.slice(agent.length) : text;
  const parts = rest.replace(/reasoned with/, "called model").split(/(called \S+|wants [\w, ]+)/);
  return (
    <>
      {agent && <b>{agent}</b>}
      {parts.map((p, i) => {
        if (p.startsWith("called ") && !p.startsWith("called model")) return <span key={i}>called <span className="code">{p.slice(7)}</span></span>;
        if (p.startsWith("wants ")) return <span key={i}>requested <span className="code">{p.slice(6)}</span></span>;
        return <span key={i}>{p}</span>;
      })}
    </>
  );
}

function Alerts() {
  const { alerts, dismissAlerts, go, agentIds } = useApp();
  if (!alerts.length) return null;
  return (
    <section className="panel alert-panel">
      <div className="panel-head">
        <div className="panel-title"><Icon name="alert" style={{ color: "var(--serious)" }} /> {alerts.length} run{alerts.length === 1 ? "" : "s"} flagged by the auto-judge</div>
        <span className="grow" />
        <button className="btn sm" onClick={dismissAlerts}>Dismiss all</button>
      </div>
      {alerts.slice(0, 5).map((a) => (
        <div key={a.conversationId + a.at} className="alert-item" onClick={() => go({ view: "conversation", id: a.conversationId, from: { view: "overview" } })}>
          <Status status={a.status} />
          <AgentChip id={a.agent} all={agentIds} />
          <span className="trunc ink2">{a.summary}</span>
          <span className="muted nowrap" style={{ fontSize: 12 }}>{fmtAgo(a.at)}</span>
        </div>
      ))}
    </section>
  );
}

function InsightPanel() {
  const { api, lastEvent, toast, agentIds } = useApp();
  const insight = useLoad<Insight | null>(() => api.get("/api/overview/insight"), []);
  const [busy, setBusy] = useState(false);
  const [showAll, setShowAll] = useState(false);
  useEffect(() => { if (lastEvent?.type === "insight") insight.setData(lastEvent.data); }, [lastEvent]); // eslint-disable-line react-hooks/exhaustive-deps
  const ask = async () => {
    setBusy(true);
    try { insight.setData(await api.post<Insight>("/api/overview/insight")); }
    catch (e: any) { toast({ kind: "error", title: "The judge couldn't answer", body: e.message }); }
    finally { setBusy(false); }
  };
  const d = insight.data;
  return (
    <Panel title={<><span className="judge-tag"><Icon name="cpu" size={11} /> Judge</span> Fleet summary</>}
      sub={d ? `${d.model} · ${fmtAgo(d.generatedAt)}` : "on demand"}
      actions={<button className={`btn sm ${d ? "" : "primary"}`} disabled={busy} onClick={ask}>
        <Icon name={busy ? "loader" : "refresh"} size={12} className={busy ? "spin" : undefined} />{busy ? "Reading activity…" : d ? "Refresh" : "Run judge"}
      </button>}>
      {busy && !d ? <div style={{ display: "grid", gap: 8 }}><Skeleton h={16} w="70%" /><Skeleton h={12} w="90%" /></div>
        : !d ? <p className="note">The judge model reads recent runs, tool chains and errors, and summarises what each agent is doing and what needs a look. It runs only when you click.</p>
        : (
          <>
            <p className="headline">{d.headline}</p>
            {d.themes?.length > 0 && <div className="row wrap" style={{ gap: 5, marginTop: 8 }}>{d.themes.map((t) => <span key={t.theme} className="tag">{t.theme} · {t.count}</span>)}</div>}
            <div className="insight-cols">
              <div>
                <div className="sub-h">By agent</div>
                {d.agents?.map((a) => (
                  <div key={a.agent} className="health-row">
                    <AgentChip id={a.agent} all={agentIds} />
                    <p>{a.doing}</p>
                    <Status status={a.health} />
                  </div>
                ))}
              </div>
              <div>
                <div className="sub-h">Needs a look</div>
                {d.concerns?.length ? (
                  <>
                    {(showAll ? d.concerns : d.concerns.slice(0, 3)).map((c, i) => <div key={i} className="concern"><Icon name="flag" size={13} />{c}</div>)}
                    {d.concerns.length > 3 && <button className="more" onClick={() => setShowAll(!showAll)}>{showAll ? "Show fewer" : `Show all ${d.concerns.length}`}</button>}
                  </>
                ) : <div className="concern"><Icon name="checkCircle" size={13} style={{ color: "var(--good-text)" }} />Nothing flagged.</div>}
              </div>
            </div>
          </>
        )}
    </Panel>
  );
}
