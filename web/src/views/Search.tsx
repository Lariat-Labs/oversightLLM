import { useEffect, useMemo, useState } from "react";
import { TimePicker, useApp } from "../App";
import { fmtDur, fmtNum, useLoad, type Task } from "../api";
import { Histogram } from "../components/charts";
import { Icon } from "../components/icons";
import { TasksTable } from "../components/TasksTable";
import { Empty, PageHead, Segmented, Skeleton, Status } from "../components/ui";

// ---------- query language ----------
// field=value  field!=value  field>n  field<=n  "free text"  word   (values accept * wildcards)
type Clause = { field?: string; op: "=" | "!=" | ">" | "<" | ">=" | "<=" | "text"; value: string };

const FIELDS: Record<string, { get: (t: Task) => string | string[] | number; numeric?: boolean; label: string }> = {
  agent: { label: "agent", get: (t) => t.agent },
  status: { label: "status", get: (t) => t.status },
  tool: { label: "tool", get: (t) => t.toolSequence },
  user: { label: "user", get: (t) => t.user || "" },
  model: { label: "model", get: (t) => t.model || "" },
  stop: { label: "stop", get: (t) => t.stopReason || "" },
  judged: { label: "judged", get: (t) => t.intent?.status || "not_judged" },
  category: { label: "category", get: (t) => t.intent?.category || "" },
  id: { label: "id", get: (t) => t.conversationId },
  turns: { label: "turns", numeric: true, get: (t) => t.turns },
  duration: { label: "duration", numeric: true, get: (t) => t.durationMs },
  tokens: { label: "tokens", numeric: true, get: (t) => t.tokens },
  errors: { label: "errors", numeric: true, get: (t) => t.toolErrors },
};
const SIDEBAR_FIELDS = ["agent", "status", "tool", "judged", "user", "model", "stop", "category"];

function tokenize(q: string): string[] {
  const out: string[] = [];
  const re = /(\w+(?:!=|>=|<=|=|>|<)"[^"]*"|"[^"]*"|\S+)/g;
  let m;
  while ((m = re.exec(q))) out.push(m[1]);
  return out;
}

function parse(q: string): Clause[] {
  return tokenize(q).map((tok) => {
    const m = tok.match(/^(\w+)(!=|>=|<=|=|>|<)(.*)$/);
    if (m && FIELDS[m[1].toLowerCase()]) return { field: m[1].toLowerCase(), op: m[2] as Clause["op"], value: m[3].replace(/^"|"$/g, "") };
    return { op: "text", value: tok.replace(/^"|"$/g, "") };
  });
}

function toNumber(field: string, v: string): number {
  if (field === "duration") {
    const m = v.match(/^([\d.]+)\s*(ms|s|m)?$/);
    if (m) return parseFloat(m[1]) * (m[2] === "m" ? 60000 : m[2] === "s" || !m[2] ? 1000 : 1);
  }
  if (/k$/i.test(v)) return parseFloat(v) * 1000;
  return parseFloat(v);
}

function glob(pattern: string, s: string) {
  const re = new RegExp("^" + pattern.split("*").map((p) => p.replace(/[.+?^${}()|[\]\\]/g, "\\$&")).join(".*") + "$", "i");
  return re.test(s);
}

function matches(t: Task, clauses: Clause[]) {
  const blob = [t.input, t.outputPreview, t.agent, t.conversationId, t.toolSequence.join(" "), t.intent?.intent || "", t.intent?.category || ""].join(" ").toLowerCase();
  return clauses.every((c) => {
    if (c.op === "text") return blob.includes(c.value.toLowerCase());
    const f = FIELDS[c.field!];
    const v = f.get(t);
    if (f.numeric) {
      const n = v as number, x = toNumber(c.field!, c.value);
      switch (c.op) {
        case "=": return n === x; case "!=": return n !== x;
        case ">": return n > x; case "<": return n < x; case ">=": return n >= x; case "<=": return n <= x;
      }
    }
    const vals = Array.isArray(v) ? v : [String(v)];
    const hit = vals.some((x) => glob(c.value, x));
    return c.op === "!=" ? !hit : hit;
  });
}

const quote = (v: string) => (/\s/.test(v) ? `"${v}"` : v);

/** Search every run like an event log: field=value queries, histogram, fields sidebar, event list. */
export function Search({ initialQuery }: { initialQuery?: string }) {
  const { api, range, lastEvent, go } = useApp();
  const [draft, setDraft] = useState(initialQuery || "");
  const [query, setQuery] = useState(initialQuery || "");
  const [mode, setMode] = useState<"events" | "table">("events");
  const [zoom, setZoom] = useState<[number, number] | null>(null);
  const [openField, setOpenField] = useState<string | null>("agent");
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [shown, setShown] = useState(50);
  const tasks = useLoad<Task[]>(() => api.get(`/api/tasks?limit=1000&window=${range.window}`), [range.window]);
  useEffect(() => { if (lastEvent?.type === "overview" || lastEvent?.type === "intent") tasks.reload(); }, [lastEvent]); // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => { setZoom(null); }, [range.key]);

  const clauses = useMemo(() => parse(query), [query]);
  const results = useMemo(() => (tasks.data || []).filter((t) => {
    if (zoom) { const s = +new Date(t.started); if (s < zoom[0] || s >= zoom[1]) return false; }
    return matches(t, clauses);
  }), [tasks.data, clauses, zoom]);

  // Histogram over the selected range (or zoom window).
  const hist = useMemo(() => {
    const end = zoom ? zoom[1] : Date.now();
    const start = zoom ? zoom[0] : end - range.ms;
    const n = 60, size = (end - start) / n;
    const counts = new Array(n).fill(0);
    for (const t of results) {
      const i = Math.floor((+new Date(t.started) - start) / size);
      if (i >= 0 && i < n) counts[i]++;
    }
    return { counts, buckets: counts.map((_, i) => new Date(start + i * size).toISOString()), start, size };
  }, [results, range.ms, zoom]);

  const fieldStats = useMemo(() => {
    const out: Record<string, { value: string; n: number }[]> = {};
    for (const f of SIDEBAR_FIELDS) {
      const counts = new Map<string, number>();
      for (const t of results) {
        const v = FIELDS[f].get(t);
        const vals = Array.isArray(v) ? [...new Set(v)] : [String(v)];
        for (const x of vals) if (x !== "") counts.set(x, (counts.get(x) || 0) + 1);
      }
      out[f] = [...counts.entries()].map(([value, n]) => ({ value, n })).sort((a, b) => b.n - a.n);
    }
    return out;
  }, [results]);

  const run = (q: string) => { setDraft(q); setQuery(q); setShown(50); };
  const addFilter = (field: string, value: string, op = "=") => {
    const clause = `${field}${op}${quote(value)}`;
    if (!tokenize(query).includes(clause)) run((query + " " + clause).trim());
  };
  const toggle = (id: string) => setExpanded((s) => { const n = new Set(s); n.has(id) ? n.delete(id) : n.add(id); return n; });

  return (
    <>
      <PageHead title="Search" sub="Every agent run as a searchable event. Click any field value to filter by it." actions={<TimePicker />} />

      <form className="searchbar" onSubmit={(e) => { e.preventDefault(); run(draft); }}>
        <div className="q">
          <Icon name="search" size={15} />
          <input value={draft} onChange={(e) => setDraft(e.target.value)} placeholder='agent=cve-triage status=failed tool=kev_lookup duration>10s "CVE-2024"' aria-label="Search query" spellCheck={false} />
        </div>
        <button type="submit" className="btn primary"><Icon name="search" size={14} /> Search</button>
      </form>
      <div className="search-hint">
        Try{" "}
        {["status=failed", "tool=kev_lookup", "duration>20s", "judged=not_judged", "turns>=4", "errors>0"].map((ex) => (
          <span key={ex}><code onClick={() => run(ex)}>{ex}</code>{" "}</span>
        ))}
        · fields: {Object.keys(FIELDS).join(", ")} · <code style={{ cursor: "default" }}>*</code> wildcards, <code style={{ cursor: "default" }}>!=</code>, <code style={{ cursor: "default" }}>&gt;</code>, <code style={{ cursor: "default" }}>&lt;</code>
      </div>

      <section className="panel">
        <div className="summary-bar">
          {tasks.loading && !tasks.data ? <Skeleton h={14} w={200} /> : <span><b>{fmtNum(results.length)}</b> runs <span className="muted">({range.label.toLowerCase()}{tasks.data && results.length !== tasks.data.length ? ` · ${tasks.data.length} before filters` : ""})</span></span>}
          {zoom && (
            <span className="tag">
              <Icon name="clock" size={11} /> {new Date(zoom[0]).toLocaleString(undefined, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" })} – {new Date(zoom[1]).toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" })}
              <button className="link" style={{ marginLeft: 4, color: "inherit" }} onClick={() => setZoom(null)} aria-label="Clear zoom"><Icon name="x" size={11} /></button>
            </span>
          )}
          {query && <button className="btn sm ghost" onClick={() => run("")}><Icon name="x" size={12} /> Clear search</button>}
          <span className="grow" />
          <Segmented value={mode} onChange={setMode} options={[{ value: "events", label: <><Icon name="list" size={12} /> Events</> }, { value: "table", label: <><Icon name="grid" size={12} /> Table</> }]} />
        </div>
        <div style={{ padding: "10px 12px 18px" }}>
          {tasks.data ? <Histogram buckets={hist.buckets} values={hist.counts} label="Runs" height={80} onPick={(i) => setZoom([hist.start + i * hist.size, hist.start + (i + 1) * hist.size])} /> : <Skeleton h={80} />}
        </div>
      </section>

      <div className="grid g-side">
        <aside className="panel fields">
          <div className="fields-h">Fields</div>
          {SIDEBAR_FIELDS.map((f) => {
            const vals = fieldStats[f] || [];
            const open = openField === f;
            return (
              <div key={f}>
                <button className="field-item" onClick={() => setOpenField(open ? null : f)} aria-expanded={open}>
                  <span className="ty">a</span>{f}<span className="cnt">{vals.length}</span>
                </button>
                {open && (
                  <div className="field-values">
                    {vals.length === 0 && <span className="muted" style={{ fontSize: 12 }}>No values</span>}
                    {vals.slice(0, 10).map((v) => (
                      <button key={v.value} className="fv" onClick={() => addFilter(f, v.value)} title={`Add ${f}=${v.value}`}>
                        <span className="fv-name">{v.value}</span>
                        <span className="fv-n">{v.n} · {Math.round((v.n / Math.max(1, results.length)) * 100)}%</span>
                        <span className="fv-bar"><i style={{ width: `${(v.n / Math.max(1, results.length)) * 100}%` }} /></span>
                      </button>
                    ))}
                  </div>
                )}
              </div>
            );
          })}
          <div className="fields-h">Numeric</div>
          {["turns", "duration", "tokens", "errors"].map((f) => (
            <button key={f} className="field-item" onClick={() => setDraft((d) => (d + ` ${f}>`).trim())}><span className="ty">#</span>{f}</button>
          ))}
          <div style={{ height: 8 }} />
        </aside>

        <section className="panel" style={{ minWidth: 0 }}>
          {tasks.loading && !tasks.data ? <div style={{ padding: 12, display: "grid", gap: 8 }}>{[0, 1, 2, 3].map((i) => <Skeleton key={i} h={44} />)}</div>
            : results.length === 0 ? <Empty icon="search" title="No runs match">Change the query, clear the zoom, or widen the time range.</Empty>
            : mode === "table" ? <TasksTable tasks={results} from={{ view: "search", q: query || undefined }} />
            : (
              <div className="table-wrap">
                <table className="events">
                  <thead><tr><th className="x" /><th className="time">Time</th><th>Event</th></tr></thead>
                  <tbody>
                    {results.slice(0, shown).map((t) => {
                      const key = t.conversationId + t.started;
                      const open = expanded.has(key);
                      const d = new Date(t.started);
                      return [
                        <tr key={key}>
                          <td className="x"><button onClick={() => toggle(key)} aria-label={open ? "Collapse" : "Expand"}><Icon name="chevronRight" size={14} className={`chev ${open ? "open" : ""}`} /></button></td>
                          <td className="time">{d.toLocaleDateString(undefined, { month: "2-digit", day: "2-digit", year: "2-digit" })}<br />{d.toLocaleTimeString(undefined, { hour12: false })}.{String(d.getMilliseconds()).padStart(3, "0")}</td>
                          <td>
                            <div className="ev-raw">
                              <Status status={t.status} />{" "}
                              <button className="link" style={{ fontFamily: "var(--mono)", fontSize: 12, color: "var(--ink)", fontWeight: 600 }} onClick={() => go({ view: "conversation", id: t.conversationId, from: { view: "search", q: query || undefined } })}>
                                {t.input || "(input not captured)"}
                              </button>
                            </div>
                            <div className="ev-fields">
                              <KV k="agent" v={t.agent} onPick={addFilter} />
                              <KV k="status" v={t.status} onPick={addFilter} err={t.status === "failed"} />
                              {t.toolSequence.length > 0 && <span className="kvp">tools = {[...new Set(t.toolSequence)].map((x, i) => <span key={x}>{i > 0 && ", "}<button className="kvp" onClick={() => addFilter("tool", x)}><span className="v">{x}</span></button></span>)}</span>}
                              <KV k="turns" v={String(t.turns)} onPick={addFilter} />
                              <span className="kvp">duration = <span className="v" style={{ color: "var(--ink)" }}>{fmtDur(t.durationMs)}</span></span>
                              <span className="kvp">tokens = <span className="v" style={{ color: "var(--ink)" }}>{fmtNum(t.tokens)}</span></span>
                              {t.toolErrors > 0 && <KV k="errors" v={String(t.toolErrors)} onPick={addFilter} err />}
                              {t.user && <KV k="user" v={t.user} onPick={addFilter} />}
                              {t.intent && <KV k="judged" v={t.intent.status} onPick={addFilter} err={["drifting", "stuck", "failed"].includes(t.intent.status)} />}
                            </div>
                          </td>
                        </tr>,
                        open && (
                          <tr key={key + ":d"} className="ev-detail">
                            <td colSpan={3}>
                              <div className="ev-detail-box">
                                <table>
                                  <tbody>
                                    <tr><td>conversation</td><td className="mono">{t.conversationId}</td></tr>
                                    <tr><td>output</td><td>{t.outputPreview || <span className="muted">not captured</span>}</td></tr>
                                    <tr><td>tool chain</td><td className="mono">{t.toolSequence.join(" → ") || "none"}</td></tr>
                                    <tr><td>model</td><td>{t.model || "—"}</td></tr>
                                    <tr><td>stop reason</td><td>{t.stopReason || "—"}</td></tr>
                                    {t.intent && <tr><td>judged intent</td><td>{t.intent.intent}</td></tr>}
                                  </tbody>
                                </table>
                                <div><button className="btn sm" onClick={() => go({ view: "conversation", id: t.conversationId, from: { view: "search", q: query || undefined } })}>Open run timeline <Icon name="arrowRight" size={12} /></button></div>
                              </div>
                            </td>
                          </tr>
                        ),
                      ];
                    })}
                  </tbody>
                </table>
                {results.length > shown && <div style={{ padding: 12, textAlign: "center" }}><button className="btn" onClick={() => setShown((n) => n + 50)}>Show 50 more ({results.length - shown} left)</button></div>}
              </div>
            )}
        </section>
      </div>
    </>
  );
}

function KV({ k, v, onPick, err }: { k: string; v: string; onPick: (f: string, v: string) => void; err?: boolean }) {
  return <button className={`kvp ${err ? "err" : ""}`} onClick={() => onPick(k, v)} title={`Add ${k}=${v} to the search`}>{k} = <span className="v">{v}</span></button>;
}
