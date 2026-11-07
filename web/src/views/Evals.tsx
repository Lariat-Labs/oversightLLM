import { useEffect, useMemo, useState } from "react";
import { useApp } from "../App";
import { fmtAgo, useLoad, type Agent, type EvalCase, type FoundryEval, type PublishResult } from "../api";
import { PassBar } from "../components/charts";
import { TraceEvalButton } from "../components/TraceEvalButton";
import { Icon } from "../components/icons";
import { AgentAvatar, Drawer, Empty, Modal, PageHead, Panel, Segmented, Skeleton, Status } from "../components/ui";
import { Single } from "./Overview";

type Filter = "" | "suggested" | "approved" | "uploaded" | "rejected";
type Draft = { id?: string; query: string; expectedBehavior: string; expectedTools: string; response: string };
const blank: Draft = { query: "", expectedBehavior: "", expectedTools: "", response: "" };

/** Curate eval cases (judge-suggested or hand-written) and publish them to Foundry. */
export function Evals({ initialAgent }: { initialAgent?: string }) {
  const { api, go, lastEvent, toast, agentIds } = useApp();
  const agents = useLoad<Agent[]>(() => api.get("/api/agents"), []);
  const [agent, setAgent] = useState(initialAgent || "");
  useEffect(() => {
    if (!agent && agents.data?.length) setAgent(agents.data.find((a) => a.inFoundry)?.name || agents.data[0].name);
  }, [agents.data]); // eslint-disable-line react-hooks/exhaustive-deps

  const cases = useLoad<EvalCase[]>(() => (agent ? api.get(`/api/evals?agent=${encodeURIComponent(agent)}`) : Promise.resolve([])), [agent]);
  const foundry = useLoad<FoundryEval[]>(() => (agent ? api.get(`/api/evals/foundry?agent=${encodeURIComponent(agent)}`) : Promise.resolve([])), [agent]);
  useEffect(() => {
    if (lastEvent?.type === "evals" && (!lastEvent.agent || lastEvent.agent === agent)) cases.reload();
  }, [lastEvent]); // eslint-disable-line react-hooks/exhaustive-deps

  const [filter, setFilter] = useState<Filter>("");
  const [query, setQuery] = useState("");
  const [count, setCount] = useState(5);
  const [suggesting, setSuggesting] = useState(false);
  const [uploading, setUploading] = useState(false);
  const [confirm, setConfirm] = useState(false);
  const [draft, setDraft] = useState<Draft | null>(null);
  const [busyId, setBusyId] = useState<string>();
  const [open, setOpen] = useState<Set<string>>(new Set());

  const list = cases.data || [];
  const counts = useMemo(() => {
    const c: Record<string, number> = { "": list.length, suggested: 0, approved: 0, uploaded: 0, rejected: 0 };
    list.forEach((x) => c[x.status]++);
    return c;
  }, [list]);
  const ready = counts.approved + counts.uploaded;
  const shown = list.filter((c) => (!filter || c.status === filter) && (!query || (c.query + c.expectedBehavior).toLowerCase().includes(query.toLowerCase())));
  const current = agents.data?.find((a) => a.name === agent);
  const runs = (foundry.data || []).flatMap((e) => (e.runs || []).map((r) => ({ ...r, evalName: e.name, mode: e.metadata?.mode || "" })))
    .sort((a, b) => b.created_at - a.created_at);
  const lastRun = runs.find((r) => r.mode !== "traces");

  const suggest = async () => {
    setSuggesting(true);
    try {
      const made = await api.post<EvalCase[]>("/api/evals/suggest", { agent, count });
      await cases.reload(); setFilter("suggested");
      toast({ kind: "good", title: `${made.length} cases suggested`, body: "Review them, approve the keepers, then upload to Foundry." });
    } catch (e: any) { toast({ kind: "error", title: "Suggestion failed", body: e.message }); }
    finally { setSuggesting(false); }
  };
  const setStatus = async (c: EvalCase, status: EvalCase["status"]) => {
    setBusyId(c.id);
    try { await api.put(`/api/evals/${c.id}`, { status }); await cases.reload(); }
    catch (e: any) { toast({ kind: "error", title: "Update failed", body: e.message }); }
    finally { setBusyId(undefined); }
  };
  const remove = async (c: EvalCase) => {
    setBusyId(c.id);
    try { await api.del(`/api/evals/${c.id}`); await cases.reload(); toast({ kind: "info", title: "Case deleted" }); }
    catch (e: any) { toast({ kind: "error", title: "Delete failed", body: e.message }); }
    finally { setBusyId(undefined); }
  };
  const save = async () => {
    if (!draft) return;
    const body = {
      agent, query: draft.query, expectedBehavior: draft.expectedBehavior, response: draft.response,
      expectedTools: draft.expectedTools.split(",").map((s) => s.trim()).filter(Boolean),
    };
    try {
      if (draft.id) await api.put(`/api/evals/${draft.id}`, body);
      else await api.post("/api/evals", { ...body, status: "approved", origin: "manual" });
      setDraft(null); await cases.reload();
      toast({ kind: "good", title: draft.id ? "Case updated" : "Case added and approved" });
    } catch (e: any) { toast({ kind: "error", title: "Save failed", body: e.message }); }
  };
  const upload = async () => {
    setUploading(true);
    try {
      const r = await api.post<PublishResult>("/api/evals/upload", { agent });
      setConfirm(false);
      await Promise.all([cases.reload(), foundry.reload()]);
      toast({ kind: "good", title: `Uploaded ${r.cases} cases to Foundry`, body: `Dataset ${r.dataset} v${r.datasetVersion} · run ${r.runStatus}`, action: r.reportUrl ? { label: "Open in Foundry", run: () => window.open(r.reportUrl, "_blank") } : undefined });
      setTimeout(() => foundry.reload(), 15000);
    } catch (e: any) { toast({ kind: "error", title: "Upload failed", body: e.message }); }
    finally { setUploading(false); }
  };

  return (
    <>
      <PageHead title="Evals" sub="Turn real agent runs into eval cases, then run them in Foundry"
        actions={
          <>
            <button className="btn" onClick={() => setDraft({ ...blank })} disabled={!agent}><Icon name="plus" size={14} /> New case</button>
            <div className="row" style={{ gap: 0 }}>
              <button className="btn" onClick={suggest} disabled={suggesting || !agent} style={{ borderTopRightRadius: 0, borderBottomRightRadius: 0 }}>
                <Icon name={suggesting ? "loader" : "sparkles"} size={14} className={suggesting ? "spin" : undefined} /> {suggesting ? "Reading activity…" : "Suggest from activity"}
              </button>
              <select className="input" value={count} onChange={(e) => setCount(+e.target.value)} aria-label="Number of cases"
                style={{ borderTopLeftRadius: 0, borderBottomLeftRadius: 0, borderLeft: 0, background: "var(--surface-2)" }}>
                {[3, 5, 8, 12].map((n) => <option key={n} value={n}>{n}</option>)}
              </select>
            </div>
            {current?.inFoundry && <TraceEvalButton agent={current.name} onDone={foundry.reload} />}
            <button className="btn primary" onClick={() => setConfirm(true)} disabled={!ready || !current?.inFoundry || uploading}>
              <Icon name="upload" size={14} /> Upload {ready} to Foundry
            </button>
          </>
        }
      />

      <div className="row wrap" style={{ gap: 10 }}>
        <label className="row" style={{ gap: 8 }}>
          <span className="muted">Agent</span>
          {!agents.data ? <Skeleton h={30} w={200} /> : (
            <select className="input" value={agent} onChange={(e) => { setAgent(e.target.value); setFilter(""); }} aria-label="Agent">
              {agents.data.map((a) => <option key={a.id} value={a.name}>{a.name} ({a.evalCases} cases)</option>)}
            </select>
          )}
        </label>
        {current && <AgentAvatar id={current.traceId} all={agentIds.length ? agentIds : [current.traceId]} />}
        {current && <button className="link" onClick={() => go({ view: "agent", id: current.name })}>View agent</button>}
      </div>

      <div className="singles">
        <Single label="To review" value={counts.suggested} sub="suggested, awaiting approval" />
        <Single label="Approved" value={counts.approved} sub="ready to upload" />
        <Single label="In Foundry" value={counts.uploaded} sub={`dataset oversight-${agent || "…"}-evals`} />
        <div className="panel single">
          <div className="single-label">Latest curated-case run</div>
          {lastRun ? <><div style={{ marginTop: 10 }}><PassBar counts={lastRun.result_counts} /></div><div className="single-sub">{lastRun.name} · {fmtAgo(new Date(lastRun.created_at * 1000).toISOString())}</div></>
            : <div className="single-sub" style={{ marginTop: 8 }}>No runs yet</div>}
        </div>
      </div>

      {current && (
        <div className="banner info">
          <Icon name="info" />
          <div>
            <b>Curated cases</b> (below) are graded on the response logged in each case's source run: intent resolution, meets expected behavior, used expected tools. Hand-written cases need a reference response.
            {" "}<b>Evaluate recent runs</b> grades {current.name}'s real runs straight from its traces. Those runs show under the agent's Evaluations tab in Foundry.
          </div>
        </div>
      )}

      <Panel bodyClass="flush">
        <div className="filters" style={{ paddingBottom: 12, borderBottom: "1px solid var(--border)" }}>
          <Segmented<Filter> value={filter} onChange={setFilter} options={[
            { value: "", label: "All", n: counts[""] },
            { value: "suggested", label: "To review", n: counts.suggested },
            { value: "approved", label: "Approved", n: counts.approved },
            { value: "uploaded", label: "In Foundry", n: counts.uploaded },
            { value: "rejected", label: "Rejected", n: counts.rejected },
          ]} />
          <span className="grow" />
          <div className="input-icon" style={{ width: 260 }}>
            <Icon name="search" size={14} />
            <input className="input" placeholder="Filter cases…" value={query} onChange={(e) => setQuery(e.target.value)} />
          </div>
        </div>
        {suggesting && (
          <div className="case" style={{ background: "var(--sel)" }}>
            <div style={{ display: "grid", gap: 8 }}>
              <div className="row" style={{ gap: 8, fontWeight: 500 }}><Icon name="sparkles" size={14} className="spin" style={{ color: "var(--link)" }} /> The judge is reading {agent}'s recent runs…</div>
              <Skeleton h={12} w="80%" /><Skeleton h={12} w="60%" />
            </div>
          </div>
        )}
        {cases.loading && !cases.data ? <div style={{ padding: 18, display: "grid", gap: 14 }}>{[0, 1, 2].map((i) => <Skeleton key={i} h={64} />)}</div>
          : shown.length === 0 && !suggesting ? (
            <Empty icon="flask" title={list.length ? "No cases match" : "No eval cases yet"}
              action={!list.length && <button className="btn" onClick={suggest} disabled={!agent}><Icon name="sparkles" size={14} /> Suggest from activity</button>}>
              {list.length ? "Try another filter." : `Let the judge read ${agent || "this agent"}'s real runs and propose regression, failure and edge cases, or write one yourself.`}
            </Empty>
          ) : shown.map((c) => {
            const isOpen = open.has(c.id);
            return (
              <div key={c.id} className={`case ${isOpen ? "open" : ""}`}>
                <div style={{ minWidth: 0 }}>
                  <div className="row" style={{ gap: 8, marginBottom: 6 }}>
                    <Status status={c.status} />
                    {c.category && <span className="tag">{c.category}</span>}
                    <span className="muted" style={{ fontSize: 12 }}>{c.origin === "judge" ? "suggested by judge" : c.origin === "conversation" ? "from a run" : "hand-written"} · {fmtAgo(c.createdAt)}</span>
                  </div>
                  <div className="case-q">{c.query}</div>
                  {c.expectedBehavior && (
                    <>
                      <div className="case-exp"><b style={{ color: "var(--ink)", fontWeight: 500 }}>Expect:</b> {c.expectedBehavior}</div>
                      {c.expectedBehavior.length > 220 && <button className="more" onClick={() => setOpen((s) => { const n = new Set(s); n.has(c.id) ? n.delete(c.id) : n.add(c.id); return n; })}>{isOpen ? "Show less" : "Show more"}</button>}
                    </>
                  )}
                  {c.rationale && isOpen && <div className="case-why"><Icon name="sparkles" size={13} />{c.rationale}</div>}
                  <div className="case-meta">
                    {(c.expectedTools || []).map((t) => <span key={t} className="chip">{t}</span>)}
                    {c.response ? <span className="tag"><Icon name="checkCircle" size={11} style={{ color: "var(--good-text)" }} /> logged response</span>
                      : <span className="tag"><Icon name="alert" size={11} style={{ color: "var(--warning)" }} /> no response to grade</span>}
                    {c.sourceConversationId && (
                      <button className="tag" style={{ border: 0, cursor: "pointer" }} onClick={() => go({ view: "conversation", id: c.sourceConversationId!, from: { view: "evals", agent } })}>
                        <Icon name="link" size={11} /> source run {c.sourceConversationId.slice(0, 8)}
                      </button>
                    )}
                    {c.upload && <span className="tag"><Icon name="database" size={11} /> {c.upload.dataset} v{c.upload.datasetVersion}</span>}
                  </div>
                </div>
                <div className="case-actions">
                  {c.status !== "approved" && c.status !== "uploaded" && <button className="btn sm" disabled={busyId === c.id} onClick={() => setStatus(c, "approved")} title="Approve"><Icon name="thumbUp" size={13} /> Approve</button>}
                  {c.status !== "rejected" && <button className="btn sm ghost" disabled={busyId === c.id} onClick={() => setStatus(c, "rejected")} title="Reject" aria-label="Reject"><Icon name="thumbDown" size={13} /></button>}
                  <button className="btn sm ghost" onClick={() => setDraft({ id: c.id, query: c.query, expectedBehavior: c.expectedBehavior, expectedTools: (c.expectedTools || []).join(", "), response: c.response })} aria-label="Edit" title="Edit"><Icon name="pencil" size={13} /></button>
                  <button className="btn sm ghost danger" disabled={busyId === c.id} onClick={() => remove(c)} aria-label="Delete" title="Delete"><Icon name="trash" size={13} /></button>
                </div>
              </div>
            );
          })}
      </Panel>

      <Panel title="Foundry evaluation runs" icon="target" bodyClass="flush" sub={agent ? `oversight-${agent} (curated cases) · oversight-traces-${agent} (recent runs)` : ""}
        actions={<button className="btn sm ghost" onClick={foundry.reload}><Icon name="refresh" size={13} /> Refresh</button>}>
        {foundry.loading && !foundry.data ? <div style={{ padding: 18 }}><Skeleton h={60} /></div>
          : runs.length === 0 ? <Empty icon="target" title="No runs in Foundry yet">Approve some cases and upload them. Each upload creates a new dataset version and an evaluation run.</Empty>
          : (
            <div className="table-wrap">
              <table className="t">
                <thead><tr><th>Run</th><th>Type</th><th>Status</th><th>Result</th><th className="right">Failed</th><th className="right">Errored</th><th className="right">Started</th><th /></tr></thead>
                <tbody>
                  {runs.map((r) => (
                    <tr key={r.id}>
                      <td><div style={{ fontWeight: 500 }}>{r.name}</div><div className="muted mono" style={{ fontSize: 11 }}>{r.evalName}</div></td>
                      <td>{r.mode === "traces" ? <span className="tag" title="Shows under the agent's Evaluations tab in Foundry"><Icon name="activity" size={11} /> Recent runs (traces)</span> : <span className="tag"><Icon name="flask" size={11} /> Curated cases</span>}</td>
                      <td><Status status={r.status} /></td>
                      <td><PassBar counts={r.result_counts} /></td>
                      <td className="right num">{r.result_counts?.failed ?? "—"}</td>
                      <td className="right num">{r.result_counts?.errored ?? "—"}</td>
                      <td className="right muted nowrap">{fmtAgo(new Date(r.created_at * 1000).toISOString())}</td>
                      <td className="right">{r.report_url && <a className="btn sm" href={r.report_url} target="_blank" rel="noreferrer">Report <Icon name="external" size={12} /></a>}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
      </Panel>

      {draft && (
        <Drawer title={draft.id ? "Edit eval case" : `New eval case · ${agent}`} icon="flask" onClose={() => setDraft(null)}
          footer={<><button className="btn" onClick={() => setDraft(null)}>Cancel</button><button className="btn primary" disabled={!draft.query.trim()} onClick={save}><Icon name="check" size={14} /> {draft.id ? "Save changes" : "Add as approved"}</button></>}>
          <label className="field"><span>User query</span><textarea className="input" value={draft.query} onChange={(e) => setDraft({ ...draft, query: e.target.value })} placeholder="What the user asks the agent" autoFocus /></label>
          <label className="field">
            <span>Expected behavior</span>
            <textarea className="input" value={draft.expectedBehavior} onChange={(e) => setDraft({ ...draft, expectedBehavior: e.target.value })} placeholder="e.g. Reports CVE-2021-44228 as P1 and cites intel_cve_priority" />
            <small>Make it checkable from the response and the tools called.</small>
          </label>
          <label className="field"><span>Expected tools</span><input className="input" value={draft.expectedTools} onChange={(e) => setDraft({ ...draft, expectedTools: e.target.value })} placeholder="intel_cve_priority, kev_lookup" /><small>Comma separated. Extra tool calls are fine.</small></label>
          <label className="field">
            <span>Reference response</span>
            <textarea className="input" style={{ minHeight: 140, fontFamily: "var(--mono)", fontSize: 12 }} value={draft.response} onChange={(e) => setDraft({ ...draft, response: e.target.value })} placeholder="Paste the agent's answer to grade" />
            <small>Foundry can't call this agent, so the graders score this text.</small>
          </label>
        </Drawer>
      )}

      {confirm && (
        <Modal onClose={() => !uploading && setConfirm(false)}
          footer={<><button className="btn" disabled={uploading} onClick={() => setConfirm(false)}>Cancel</button><button className="btn primary" disabled={uploading} onClick={upload}><Icon name={uploading ? "loader" : "upload"} size={14} className={uploading ? "spin" : undefined} /> {uploading ? "Uploading…" : `Upload ${ready} cases`}</button></>}>
          <div className="modal-ic"><Icon name="upload" size={20} /></div>
          <h3>Upload {ready} cases to Foundry?</h3>
          <p>This writes to your Foundry project for <b>{agent}</b>:</p>
          <ol className="steps">
            <li><span>New version of dataset <span className="mono">oversight-{agent}-evals</span></span></li>
            <li><span>Evaluation <span className="mono">oversight-{agent}</span> (created once, reused after)</span></li>
            <li><span>An eval run with three graders, scored by the grader model</span></li>
          </ol>
        </Modal>
      )}
    </>
  );
}
