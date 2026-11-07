import { useEffect, useMemo, useState } from "react";
import { useApp, type Route } from "../App";
import { fmtDur, fmtNum, fmtTime, useEvents, useLoad, type Conversation, type Intent, type Step } from "../api";
import { Waterfall } from "../components/charts";
import { Icon } from "../components/icons";
import { AgentAvatar, Empty, Panel, Skeleton, Status } from "../components/ui";

type Block =
  | { kind: "user"; step: Step }
  | { kind: "turn"; step: Step; tools: { step: Step; children: Step[] }[]; logs: Step[] }
  | { kind: "answer"; step: Step }
  | { kind: "log"; step: Step };

/** Group a flat, time-ordered step list into reasoning turns with their tool calls. */
function toBlocks(steps: Step[]): Block[] {
  const byId = new Map(steps.filter((s) => s.id).map((s) => [s.id, s]));
  const ancestorIn = (s: Step, ids: Set<string>) => {
    let cur: Step | undefined = s;
    for (let i = 0; i < 30 && cur; i++) {
      if (cur.parentId && ids.has(cur.parentId)) return cur.parentId;
      cur = cur.parentId ? byId.get(cur.parentId) : undefined;
    }
    return undefined;
  };
  const toolIds = new Set(steps.filter((s) => s.kind === "tool").map((s) => s.id));
  const llmIds = new Set(steps.filter((s) => s.kind === "llm").map((s) => s.id));
  const blocks: Block[] = [];
  let turn: Extract<Block, { kind: "turn" }> | null = null;
  const toolChildren = new Map<string, Step[]>();
  for (const s of steps) {
    if (s.kind === "user") { blocks.push({ kind: "user", step: s }); turn = null; continue; }
    if (s.kind === "answer") { blocks.push({ kind: "answer", step: s }); turn = null; continue; }
    if (s.kind === "llm") { turn = { kind: "turn", step: s, tools: [], logs: [] }; blocks.push(turn); continue; }
    if (s.kind === "tool") {
      const entry = { step: s, children: [] as Step[] };
      toolChildren.set(s.id, entry.children);
      if (turn) turn.tools.push(entry);
      else blocks.push({ kind: "turn", step: { ...s, kind: "llm", title: "Tool calls", detail: "" }, tools: [entry], logs: [] });
      continue;
    }
    const tool = ancestorIn(s, toolIds);
    if (tool) { toolChildren.get(tool)?.push(s); continue; }
    if (ancestorIn(s, llmIds)) continue; // model HTTP calls under a reasoning step
    if (s.kind === "log") { if (turn) turn.logs.push(s); else blocks.push({ kind: "log", step: s }); }
    // ingress spans above the agent (UI, gateway) are shown in the waterfall only
  }
  return blocks;
}

/** Follow one conversation: input → reasoning turns → tool calls → answer, plus the judge's read. */
export function ConversationView({ id, from }: { id: string; from?: Route }) {
  const { api, go, agentIds, toast } = useApp();
  const data = useLoad<{ conversation: Conversation; intent: Intent | null }>(() => api.get(`/api/conversations/${encodeURIComponent(id)}`), [id]);
  const [tab, setTab] = useState<"timeline" | "trace">("timeline");
  const [judging, setJudging] = useState(false);
  const [madeEval, setMadeEval] = useState(false);

  useEvents(api, (e) => {
    if (e.type === "activity") data.reload();
    if (e.type === "intent" && data.data) data.setData({ ...data.data, intent: e.data });
  }, { conversation: id });

  const c = data.data?.conversation;
  const intent = data.data?.intent;
  useEffect(() => { if (c && !intent && c.steps.length) judge(false); }, [c?.id]); // eslint-disable-line react-hooks/exhaustive-deps

  async function judge(force: boolean) {
    setJudging(true);
    try {
      const i = await api.post<Intent>(`/api/conversations/${encodeURIComponent(id)}/intent${force ? "?force=1" : ""}`);
      data.setData((d) => d && { ...d, intent: i });
    } catch (e: any) { toast({ kind: "error", title: "The judge couldn't read this run", body: e.message }); }
    finally { setJudging(false); }
  }
  async function makeEval() {
    try {
      await api.post(`/api/evals/from-conversation/${encodeURIComponent(id)}`, {});
      setMadeEval(true);
      toast({ kind: "good", title: "Draft eval case created", body: "It replays this run's input and grades against its logged answer.", action: { label: "Open evals", run: () => go({ view: "evals", agent: c?.agent }) } });
    } catch (e: any) { toast({ kind: "error", title: "Couldn't create the case", body: e.message }); }
  }

  const blocks = useMemo(() => (c ? toBlocks(c.steps) : []), [c]);
  const back = from || { view: "search" as const };

  if (data.error) {
    return (
      <div className="panel">
        <Empty icon="search" title="No telemetry for this run" action={<button className="btn" onClick={() => go(back)}><Icon name="arrowLeft" size={14} /> Back</button>}>{data.error}</Empty>
      </div>
    );
  }
  if (!c) {
    return (
      <div className="grid">
        <Skeleton h={44} w={360} />
        <Skeleton h={64} />
        <div className="grid g-main"><Skeleton h={420} /><Skeleton h={320} /></div>
      </div>
    );
  }

  return (
    <>
      <div className="page-head fade-in">
        <button className="btn icon" onClick={() => go(back)} aria-label="Back"><Icon name="arrowLeft" /></button>
        <AgentAvatar id={c.agent} all={agentIds} size="lg" />
        <div className="grow">
          <div className="row" style={{ gap: 10 }}>
            <h1 className="page-title trunc">{c.input ? c.input.split("\n")[0] : `${c.agent} run`}</h1>
          </div>
          <div className="row" style={{ gap: 8, marginTop: 4 }}>
            <Status status={c.status} />
            <span className="muted" style={{ fontSize: 13 }}>{c.agent}</span>
            <span className="muted">·</span>
            <span className="mono muted">{c.id}</span>
          </div>
        </div>
        <button className="btn" onClick={() => go({ view: "evals", agent: c.agent })}><Icon name="flask" size={14} /> Agent evals</button>
        <button className={`btn ${madeEval ? "" : "primary"}`} disabled={madeEval} onClick={makeEval}>
          <Icon name={madeEval ? "check" : "plus"} size={14} /> {madeEval ? "Eval case drafted" : "Turn into eval case"}
        </button>
      </div>

      <div className="meta-strip fade-in">
        <div><span>Started</span><b>{fmtTime(c.started)}</b></div>
        <div><span>Duration</span><b>{fmtDur(c.durationMs)}</b></div>
        <div><span>Tool calls</span><b>{c.toolCalls}{c.toolErrors > 0 && <span style={{ color: "var(--critical-text)", fontSize: 13 }}> · {c.toolErrors} failed</span>}</b></div>
        <div><span>Tokens</span><b>{fmtNum(c.inputTokens + c.outputTokens)}</b> <span style={{ display: "inline" }} className="muted">{fmtNum(c.inputTokens)} in / {fmtNum(c.outputTokens)} out</span></div>
        <div><span>Model</span><b>{c.model || "—"}</b></div>
        <div><span>User</span><b>{c.user || "—"}</b></div>
      </div>

      <div className="grid g-main">
        <section className="panel">
          <div className="tabs">
            <button className={tab === "timeline" ? "on" : ""} onClick={() => setTab("timeline")}><Icon name="list" size={14} /> Run timeline</button>
            <button className={tab === "trace" ? "on" : ""} onClick={() => setTab("trace")}><Icon name="waterfall" size={14} /> Trace waterfall <span className="tag">{c.steps.length}</span></button>
            <span className="spacer" />
            {c.stopReason && <span className="tag">stop: {c.stopReason}</span>}
          </div>
          {tab === "timeline" ? (
            <div className="panel-body" style={{ paddingTop: 14 }}>
              <p className="note" style={{ marginBottom: 14 }}>Reconstructed from OpenTelemetry spans: the user input, each model call and the tools it requested, tool results, and the final answer. The model's private reasoning isn't recorded in telemetry.</p>
              {blocks.length === 0 ? <Empty icon="list" title="No steps recorded" /> : blocks.map((b, i) => <BlockView key={i} block={b} />)}
            </div>
          ) : (
            <div style={{ paddingBottom: 8 }}><Waterfall steps={c.steps} /></div>
          )}
        </section>

        <div className="stack sticky">
          <JudgePanel intent={intent} judging={judging} onJudge={() => judge(true)} />
          {c.systemPrompt && <Panel title="System instructions" icon="terminal"><pre className="pre">{c.systemPrompt}</pre></Panel>}
          {c.toolDefs?.length ? (
            <Panel title="Tools available" icon="wrench" sub={`${c.toolDefs.length} tools`}>
              <div className="chain-chips" style={{ gap: 6 }}>
                {c.toolDefs.map((t) => {
                  const used = c.steps.some((s) => s.kind === "tool" && s.title === "Tool: " + t);
                  return <span key={t} className="chip" style={used ? { borderColor: "var(--ink-2)", color: "var(--ink)" } : undefined}>{used && "✓ "}{t}</span>;
                })}
              </div>
            </Panel>
          ) : null}
        </div>
      </div>
    </>
  );
}

function BlockView({ block }: { block: Block }) {
  const s = block.step;
  const meta = (
    <span className="turn-meta">
      {new Date(s.time).toLocaleTimeString()}{s.durationMs > 0 && ` · ${fmtDur(s.durationMs)}`}{s.tokens ? ` · ${fmtNum(s.tokens)} tokens` : ""}
    </span>
  );
  if (block.kind === "user") {
    return (
      <div className="turn">
        <span className="turn-node user"><Icon name="user" size={15} /></span>
        <div className="turn-head"><span className="turn-title">User asked</span>{meta}</div>
        <div className="turn-body"><div className="bubble user">{s.detail || <span className="muted">Input not captured in telemetry</span>}</div></div>
      </div>
    );
  }
  if (block.kind === "answer") {
    return (
      <div className="turn">
        <span className={`turn-node ${s.ok ? "answer" : "bad"}`}><Icon name={s.ok ? "checkCircle" : "xCircle"} size={15} /></span>
        <div className="turn-head"><span className="turn-title">{s.ok ? "Agent answered" : "Run failed"}</span>{meta}</div>
        <div className="turn-body">
          <div className="bubble answer">{s.detail || <span className="muted">Output not captured in telemetry</span>}</div>
          {s.attributes && <div className="attrs">{Object.entries(s.attributes).map(([k, v]) => <span key={k} className="chip">{k.split(".").pop()}: {v}</span>)}</div>}
        </div>
      </div>
    );
  }
  if (block.kind === "log") {
    return <div className="turn"><span className="turn-node ctx"><Icon name="terminal" size={14} /></span><div className="turn-body" style={{ paddingTop: 4 }}><div className="log-line">{s.detail}</div></div></div>;
  }
  const [first, ...rest] = (s.detail || "").split("\n");
  const wants = first.startsWith("Decided to call: ") ? first.slice(17).split(", ") : [];
  const thought = rest.join("\n").trim();
  return (
    <div className="turn">
      <span className={`turn-node ${s.ok ? "" : "bad"}`}><Icon name="cpu" size={15} /></span>
      <div className="turn-head"><span className="turn-title">{s.title.replace(/^Reasoning \(turn (\d+)\)$/, "Model call · turn $1")}</span>{meta}</div>
      <div className="turn-body">
        {wants.length > 0 ? (
          <div className="reason">Requested tools {wants.map((w) => <span key={w} className="chip">{w}</span>)}</div>
        ) : first && <div className="reason">{first === "Composed the answer" ? "Returned the final answer (no tool requests)" : first}</div>}
        {thought && <div className="bubble" style={{ fontSize: 13 }}>{thought}</div>}
        {block.tools.map((t) => <ToolCard key={t.step.id} step={t.step} children={t.children} />)}
        {block.logs.map((l) => <div key={l.time + l.detail} className="log-line">{l.detail}</div>)}
      </div>
    </div>
  );
}

function ToolCard({ step, children }: { step: Step; children: Step[] }) {
  const [open, setOpen] = useState(false);
  const name = step.title.replace(/^Tool: /, "");
  const outcome = (step.detail || "").replace(/^Outcome: /, "");
  const visible = children.filter((c) => c.kind !== "http" || !/^GET \/$/.test(c.title));
  return (
    <div className={`tool-card ${step.ok ? "" : "bad"}`}>
      <button className="tool-top" onClick={() => setOpen(!open)} aria-expanded={open}>
        <span className="tool-ic"><Icon name="wrench" size={13} /></span>
        <span className="tool-name">{name}</span>
        {step.ok ? <span className="pill good"><span className="ic"><Icon name="check" size={11} strokeWidth={2.4} /></span>{outcome || "ok"}</span>
          : <span className="pill critical"><span className="ic"><Icon name="xCircle" size={11} strokeWidth={2.4} /></span>{outcome || "error"}</span>}
        <span className="grow" />
        <span className="turn-meta">{fmtDur(step.durationMs)}</span>
        {(visible.length > 0 || step.attributes) && <Icon name="chevronRight" size={14} className={`chev ${open ? "open" : ""}`} />}
      </button>
      {open && (
        <div className="tool-sub">
          {step.attributes && <div className="attrs">{Object.entries(step.attributes).map(([k, v]) => <span key={k} className="chip">{k.split(".").pop()}: {v}</span>)}</div>}
          {visible.map((c, i) => (
            <div key={c.id + i} className="sub-row" style={{ paddingLeft: Math.max(0, c.depth - step.depth - 1) * 14 }}>
              <span className="nm">{c.kind === "log" ? "log: " + c.detail : c.title}</span>
              <span className="turn-meta">{c.ok ? "" : "failed · "}{c.durationMs ? fmtDur(c.durationMs) : ""}</span>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

function JudgePanel({ intent, judging, onJudge }: { intent?: Intent | null; judging: boolean; onJudge: () => void }) {
  return (
    <section className="panel insight">
      <div className="panel-head" style={{ position: "relative", zIndex: 1 }}>
        <span className="ai-badge"><Icon name="sparkles" size={12} strokeWidth={2.2} /> Judge</span>
        <span className="panel-title">Intent & direction</span>
        <span className="grow" />
        <button className="btn sm" disabled={judging} onClick={onJudge}>
          <Icon name={judging ? "loader" : "refresh"} size={13} className={judging ? "spin" : undefined} />{judging ? "Judging" : intent ? "Re-judge" : "Judge"}
        </button>
      </div>
      <div className="panel-body" style={{ position: "relative", zIndex: 1 }}>
        {!intent ? (
          judging ? <div style={{ display: "grid", gap: 10 }}><Skeleton h={18} w="60%" /><Skeleton h={14} /><Skeleton h={14} w="85%" /><Skeleton h={14} w="70%" /></div>
            : <p className="ink2">Not judged yet.</p>
        ) : (
          <>
            <div className="judge-sec">
              <div className="row" style={{ justifyContent: "space-between", marginBottom: 10 }}>
                <Status status={intent.status} />
                <span className="tag">{intent.category}</span>
              </div>
              <p className="lead">{intent.intent}</p>
              <div className="row" style={{ marginTop: 12, gap: 10 }}>
                <span className="muted" style={{ fontSize: 12 }}>Confidence</span>
                <div className="confidence grow"><i style={{ width: `${Math.round((intent.confidence || 0) * 100)}%` }} /></div>
                <span className="num" style={{ fontSize: 12, fontWeight: 600 }}>{Math.round((intent.confidence || 0) * 100)}%</span>
              </div>
            </div>
            <div className="judge-sec"><div className="sub-h">How it went</div><p>{intent.trajectory}</p></div>
            <div className="judge-sec"><div className="sub-h">Where it's heading</div><p>{intent.direction}</p></div>
            {intent.riskFlags?.length > 0 && (
              <div className="judge-sec">
                <div className="sub-h">Risk flags · {intent.riskFlags.length}</div>
                {intent.riskFlags.map((r, i) => <div key={i} className="flag"><Icon name="flag" size={14} />{r}</div>)}
              </div>
            )}
            <div className="judge-sec muted" style={{ fontSize: 12 }}>{intent.model} · judged {fmtTime(intent.judgedAt)}</div>
          </>
        )}
      </div>
    </section>
  );
}
