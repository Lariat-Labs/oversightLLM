import { useEffect, useMemo, useRef, useState } from "react";
import { useApp, type Route } from "../App";
import { fmtAgo, type Task } from "../api";
import { Icon, type IconName } from "./icons";
import { AgentAvatar } from "./ui";

type Item = { group: string; label: string; hint?: string; icon?: IconName; agent?: string; run: () => void };

/** ⌘K palette: jump to pages, agents and recent runs, or flip switches. */
export function CommandPalette({ onClose }: { onClose: () => void }) {
  const { api, go, agentIds, settings, setAutoJudge } = useApp();
  const [q, setQ] = useState("");
  const [sel, setSel] = useState(0);
  const [tasks, setTasks] = useState<Task[]>([]);
  const input = useRef<HTMLInputElement>(null);
  const list = useRef<HTMLDivElement>(null);

  useEffect(() => { input.current?.focus(); api.get<Task[]>("/api/tasks?limit=150").then(setTasks).catch(() => {}); }, [api]);

  const nav = (r: Route) => () => { go(r); onClose(); };
  const items = useMemo<Item[]>(() => {
    const all: Item[] = [
      { group: "Pages", label: "Overview", icon: "grid", run: nav({ view: "overview" }) },
      { group: "Pages", label: "Search runs", icon: "search", run: nav({ view: "search" }) },
      { group: "Pages", label: "Agents", icon: "bot", run: nav({ view: "agents" }) },
      { group: "Pages", label: "Evals", icon: "flask", run: nav({ view: "evals" }) },
      { group: "Actions", label: settings?.autoJudge ? "Turn auto-judge off" : "Turn auto-judge on", icon: "sparkles", run: () => { setAutoJudge(!settings?.autoJudge); onClose(); } },
      ...agentIds.flatMap((a) => [
        { group: "Agents", label: a, agent: a, hint: "Agent details", run: nav({ view: "agent", id: a }) },
        { group: "Agents", label: `${a}: runs`, agent: a, hint: "Search", run: nav({ view: "search", q: `agent=${a}` }) },
        { group: "Agents", label: `${a}: evals`, agent: a, hint: "Evals", run: nav({ view: "evals", agent: a }) },
      ]),
      ...tasks.map((t) => ({
        group: "Runs", icon: "message" as IconName, label: t.input || `(input not captured) · ${t.conversationId.slice(0, 8)}`,
        hint: `${t.agent} · ${fmtAgo(t.started)}`, run: nav({ view: "conversation", id: t.conversationId, from: { view: "search" } }),
      })),
    ];
    const needle = q.trim().toLowerCase();
    const filtered = needle ? all.filter((i) => (i.label + " " + (i.hint || "")).toLowerCase().includes(needle)) : all;
    // keep the list snappy: cap runs
    let runs = 0;
    return filtered.filter((i) => i.group !== "Runs" || ++runs <= (needle ? 30 : 8));
  }, [q, tasks, agentIds, settings?.autoJudge]); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => setSel(0), [q]);
  useEffect(() => { list.current?.querySelector(".sel")?.scrollIntoView({ block: "nearest" }); }, [sel]);

  const onKey = (e: React.KeyboardEvent) => {
    if (e.key === "ArrowDown") { e.preventDefault(); setSel((s) => Math.min(items.length - 1, s + 1)); }
    if (e.key === "ArrowUp") { e.preventDefault(); setSel((s) => Math.max(0, s - 1)); }
    if (e.key === "Enter") items[sel]?.run();
    if (e.key === "Escape") onClose();
  };

  let lastGroup = "";
  return (
    <>
      <div className="overlay" onClick={onClose} />
      <div className="palette" role="dialog" aria-modal="true" onKeyDown={onKey}>
        <div className="palette-input">
          <Icon name="search" size={18} />
          <input ref={input} value={q} onChange={(e) => setQ(e.target.value)} placeholder="Search runs, agents, pages…" aria-label="Search" />
          <span className="kbd">esc</span>
        </div>
        <div className="palette-list" ref={list}>
          {items.length === 0 && <div className="empty" style={{ padding: 28 }}>No matches for “{q}”</div>}
          {items.map((it, i) => {
            const head = it.group !== lastGroup ? (lastGroup = it.group) : null;
            return (
              <div key={i}>
                {head && <div className="palette-group">{head}</div>}
                <button className={`palette-item ${i === sel ? "sel" : ""}`} onMouseEnter={() => setSel(i)} onClick={it.run}>
                  {it.agent ? <AgentAvatar id={it.agent} all={agentIds} /> : <Icon name={it.icon || "arrowRight"} />}
                  <span className="trunc">{it.label}</span>
                  {it.hint && <span className="hint">{it.hint}</span>}
                </button>
              </div>
            );
          })}
        </div>
        <div className="palette-foot"><span><span className="kbd">↑↓</span> navigate</span><span><span className="kbd">↵</span> open</span><span><span className="kbd">⌘K</span> toggle</span></div>
      </div>
    </>
  );
}
