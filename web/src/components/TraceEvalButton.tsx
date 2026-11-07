import { useState } from "react";
import { useApp } from "../App";
import { type TraceRunResult } from "../api";
import { Icon } from "./icons";
import { Modal } from "./ui";

/** Starts a Foundry trace eval over the agent's recent runs (it shows under the agent in Foundry). */
export function TraceEvalButton({ agent, onDone, small }: { agent: string; onDone?: () => void; small?: boolean }) {
  const { api, range, toast } = useApp();
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [max, setMax] = useState(50);
  const hours = Math.max(1, Math.round(range.ms / 3600e3));

  const run = async () => {
    setBusy(true);
    try {
      const r = await api.post<TraceRunResult>("/api/evals/traces", { agent, lookbackHours: hours, maxTraces: max });
      setOpen(false);
      toast({ kind: "good", title: `Evaluating ${agent}'s recent runs in Foundry`, body: `Run started in ${r.evalName}. Results appear under the agent's Evaluations tab in a few minutes.`,
        action: r.reportUrl ? { label: "Open in Foundry", run: () => window.open(r.reportUrl, "_blank") } : undefined });
      onDone?.();
      setTimeout(() => onDone?.(), 20000);
    } catch (e: any) { toast({ kind: "error", title: "Couldn't start the trace eval", body: e.message }); }
    finally { setBusy(false); }
  };

  return (
    <>
      <button className={`btn ${small ? "sm" : ""}`} onClick={() => setOpen(true)}><Icon name="target" size={small ? 12 : 13} /> Evaluate recent runs</button>
      {open && (
        <Modal onClose={() => !busy && setOpen(false)}
          footer={<><button className="btn" disabled={busy} onClick={() => setOpen(false)}>Cancel</button>
            <button className="btn primary" disabled={busy} onClick={run}><Icon name={busy ? "loader" : "target"} size={13} className={busy ? "spin" : undefined} /> {busy ? "Starting…" : "Start evaluation"}</button></>}>
          <h3>Evaluate {agent}'s recent runs in Foundry</h3>
          <p>Foundry reads this agent's traces from Application Insights and grades each run. The results show up under <b>{agent} → Evaluations</b> in the Foundry portal.</p>
          <ol className="steps">
            <li>Time range: <b>{range.label.toLowerCase()}</b> (from the time picker)</li>
            <li>Graders: intent resolution, task adherence, tool call accuracy</li>
            <li>Eval <span className="mono">oversight-traces-{agent}</span> (created once, reused after)</li>
          </ol>
          <label className="field"><span>Max runs to grade</span>
            <select className="input" value={max} onChange={(e) => setMax(+e.target.value)} style={{ width: 120 }}>
              {[10, 25, 50, 100, 200].map((n) => <option key={n} value={n}>{n}</option>)}
            </select>
          </label>
          <p className="note">Only runs whose spans record the input and output messages can be graded. Runs shown as "input not captured" are skipped.</p>
        </Modal>
      )}
    </>
  );
}
