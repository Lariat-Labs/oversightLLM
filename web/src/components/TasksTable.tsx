import { useMemo, useState, type ReactNode } from "react";
import { useApp, type Route } from "../App";
import { fmtAgo, fmtDur, fmtNum, type Task } from "../api";
import { Icon } from "./icons";
import { AgentChip, Skeleton, Status, ToolChain } from "./ui";

type Col = { key: string; label: string; right?: boolean; sort?: (t: Task) => number | string; render: (t: Task) => ReactNode; hide?: boolean };

/** Sortable, striped table of runs. */
export function TasksTable({ tasks, loading, from, compact, limit }: { tasks?: Task[]; loading?: boolean; from: Route; compact?: boolean; limit?: number }) {
  const { go, agentIds } = useApp();
  const [sort, setSort] = useState<{ key: string; dir: 1 | -1 }>({ key: "started", dir: -1 });

  const allCols: Col[] = [
    { key: "status", label: "Status", sort: (t) => t.status, render: (t) => <Status status={t.status} /> },
    {
      key: "input", label: "Run", sort: (t) => t.input || "~",
      render: (t) => (
        <div style={{ minWidth: 0 }}>
          <div className="task trunc" title={t.input}>{t.input || <span className="muted" style={{ fontWeight: 400 }}>Input not captured</span>}</div>
          {!compact && <div className="task-sub trunc">{t.outputPreview || <span className="mono">{t.conversationId}</span>}</div>}
        </div>
      ),
    },
    { key: "agent", label: "Agent", sort: (t) => t.agent, render: (t) => <AgentChip id={t.agent} all={agentIds} /> },
    {
      key: "intent", label: "Judged", hide: compact, sort: (t) => t.intent?.status || "~",
      render: (t) => t.intent ? <span title={t.intent.intent}><Status status={t.intent.status} /> <span className="muted" style={{ fontSize: 12 }}>{t.intent.category}</span></span> : <span className="muted">—</span>,
    },
    { key: "tools", label: "Tool chain", sort: (t) => t.toolSequence.length, render: (t) => <ToolChain tools={t.toolSequence} max={3} errors={t.toolErrors} /> },
    { key: "turns", label: "Turns", right: true, sort: (t) => t.turns, render: (t) => t.turns },
    { key: "duration", label: "Duration", right: true, sort: (t) => t.durationMs, render: (t) => fmtDur(t.durationMs) },
    { key: "tokens", label: "Tokens", right: true, sort: (t) => t.tokens, render: (t) => fmtNum(t.tokens) },
    { key: "started", label: "Started", right: true, sort: (t) => +new Date(t.started), render: (t) => <span className="muted nowrap" title={new Date(t.started).toLocaleString()}>{fmtAgo(t.started)}</span> },
  ];
  const cols = allCols.filter((c) => !c.hide);

  const rows = useMemo(() => {
    const col = cols.find((c) => c.key === sort.key);
    const list = (tasks || []).map((t) => ({ ...t, toolSequence: t.toolSequence || [] }));
    if (col?.sort) list.sort((a, b) => { const x = col.sort!(a), y = col.sort!(b); return (x < y ? -1 : x > y ? 1 : 0) * sort.dir; });
    return limit ? list.slice(0, limit) : list;
  }, [tasks, sort, limit]); // eslint-disable-line react-hooks/exhaustive-deps

  if (loading && !tasks) return <div style={{ padding: 12, display: "grid", gap: 8 }}>{Array.from({ length: 5 }).map((_, i) => <Skeleton key={i} h={26} />)}</div>;
  return (
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
          {rows.map((t) => (
            <tr key={t.conversationId + t.started} className="click" onClick={() => go({ view: "conversation", id: t.conversationId, from })}>
              {cols.map((c) => <td key={c.key} className={c.right ? "right num" : ""}>{c.render(t)}</td>)}
            </tr>
          ))}
        </tbody>
      </table>
      {tasks && tasks.length === 0 && (
        <div className="empty"><span className="empty-ic"><Icon name="search" size={20} /></span><h4>No runs in this time range</h4><p>Pick a wider range in the time picker.</p></div>
      )}
    </div>
  );
}
