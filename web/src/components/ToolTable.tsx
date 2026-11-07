import { fmtDur, type ToolStats } from "../api";

/** Tool usage as a table with an inline bar for call volume (one series, so one color). */
export function ToolTable({ tools, onPick }: { tools: ToolStats[]; onPick?: (t: ToolStats) => void }) {
  const max = Math.max(1, ...tools.map((t) => t.calls));
  return (
    <table className="t">
      <thead><tr><th>Tool</th><th>Calls</th><th className="right">Failed</th><th className="right">Avg</th><th>Agents</th></tr></thead>
      <tbody>
        {tools.map((t) => (
          <tr key={t.name} className={onPick ? "click" : ""} onClick={() => onPick?.(t)}>
            <td className="mono">{t.name}</td>
            <td><div className="bar-cell"><span className="track"><span className="fill" style={{ display: "block", width: `${(t.calls / max) * 100}%` }} /></span><span className="v">{t.calls}</span></div></td>
            <td className={`right num ${t.errors ? "bad-text" : ""}`}>{t.errors}</td>
            <td className="right num">{fmtDur(t.avgMs)}</td>
            <td><span className="muted" style={{ fontSize: 12 }}>{t.agents.join(", ")}</span></td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}
