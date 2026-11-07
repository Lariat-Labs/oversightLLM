// Charts: one series per chart (no dual axes), 2px lines, recessive grid, hover on every plot.
import { useId, useMemo, useRef, useState, type ReactNode } from "react";
import { fmtDur, fmtNum, type Step } from "../api";

const fmtBucket = (iso: string) => new Date(iso).toLocaleString(undefined, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" });

function niceMax(v: number) {
  if (v <= 0) return 1;
  const p = Math.pow(10, Math.floor(Math.log10(v)));
  for (const m of [1, 2, 2.5, 5, 10]) if (m * p >= v) return m * p;
  return 10 * p;
}

function smoothPath(pts: [number, number][]) {
  if (pts.length < 2) return "";
  let d = `M${pts[0][0]},${pts[0][1]}`;
  for (let i = 1; i < pts.length; i++) {
    const [x0, y0] = pts[i - 1], [x1, y1] = pts[i];
    const cx = (x0 + x1) / 2;
    d += ` C${cx},${y0} ${cx},${y1} ${x1},${y1}`;
  }
  return d;
}

/** Sparkline for a stat tile, with a hover readout. */
export function Spark({ values, labels, format = (v) => fmtNum(v), mini }: { values: number[]; labels: string[]; format?: (v: number) => string; mini?: boolean }) {
  const [hover, setHover] = useState<number | null>(null);
  const gid = useId().replace(/:/g, "");
  const W = 200, H = mini ? 22 : 28;
  const max = Math.max(1, ...values);
  const pts = values.map((v, i) => [(i / Math.max(1, values.length - 1)) * W, H - 3 - (v / max) * (H - 6)] as [number, number]);
  const line = smoothPath(pts);
  const area = line ? `${line} L${W},${H} L0,${H} Z` : "";
  const onMove = (e: React.MouseEvent<SVGSVGElement>) => {
    const r = e.currentTarget.getBoundingClientRect();
    setHover(Math.max(0, Math.min(values.length - 1, Math.round(((e.clientX - r.left) / r.width) * (values.length - 1)))));
  };
  return (
    <div className={`spark ${mini ? "mini" : ""}`}>
      <svg viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none" onMouseMove={onMove} onMouseLeave={() => setHover(null)}>
        <defs>
          <linearGradient id={gid} x1="0" x2="0" y1="0" y2="1">
            <stop offset="0" stopColor="var(--s1)" stopOpacity="0.10" />
            <stop offset="1" stopColor="var(--s1)" stopOpacity="0" />
          </linearGradient>
        </defs>
        {!mini && area && <path d={area} fill={`url(#${gid})`} />}
        {line && <path className="line" d={line} vectorEffect="non-scaling-stroke" />}
        {hover !== null && pts[hover] && <line className="hl" x1={pts[hover][0]} x2={pts[hover][0]} y1={0} y2={H} vectorEffect="non-scaling-stroke" />}
      </svg>
      {hover !== null && labels[hover] && (
        <div className="tip" style={{ left: `clamp(0px, calc(${(hover / Math.max(1, values.length - 1)) * 100}% - 80px), calc(100% - 160px))`, bottom: mini ? 28 : 36 }}>
          <div className="tip-title">{fmtBucket(labels[hover])}</div>
          <div className="tip-row primary"><span>Value</span><b>{format(values[hover])}</b></div>
        </div>
      )}
    </div>
  );
}

export type Metric = { key: string; label: string; values: number[]; format?: (v: number) => string };

/** Single-series area chart with crosshair + tooltip showing every metric for the hovered bucket. */
export function TrendChart({ buckets, metric, all }: { buckets: string[]; metric: Metric; all: Metric[] }) {
  const [hover, setHover] = useState<number | null>(null);
  const ref = useRef<SVGSVGElement>(null);
  const gid = useId().replace(/:/g, "");
  const W = 900, H = 240, padL = 40, padR = 8, padT = 12, padB = 26;
  const vals = metric.values;
  const nice = niceMax(Math.max(...vals, 0));
  const x = (i: number) => padL + (i / Math.max(1, vals.length - 1)) * (W - padL - padR);
  const y = (v: number) => padT + (H - padT - padB) * (1 - v / nice);
  const pts = vals.map((v, i) => [x(i), y(v)] as [number, number]);
  const line = smoothPath(pts);
  const area = line ? `${line} L${x(vals.length - 1)},${y(0)} L${x(0)},${y(0)} Z` : "";
  const ticks = [0, nice / 4, nice / 2, (nice * 3) / 4, nice];
  const xt = useMemo(() => {
    const n = buckets.length;
    return n ? [0, Math.floor(n / 4), Math.floor(n / 2), Math.floor((3 * n) / 4), n - 1] : [];
  }, [buckets.length]);
  const fmt = metric.format || ((v: number) => fmtNum(v));
  const onMove = (e: React.MouseEvent) => {
    const r = ref.current!.getBoundingClientRect();
    const px = ((e.clientX - r.left) / r.width) * W;
    const i = Math.round(((px - padL) / (W - padL - padR)) * (vals.length - 1));
    setHover(i >= 0 && i < vals.length ? i : null);
  };
  const total = vals.reduce((a, b) => a + b, 0);
  return (
    <div className="chart" onMouseLeave={() => setHover(null)}>
      <svg ref={ref} viewBox={`0 0 ${W} ${H}`} role="img" aria-label={`${metric.label} over time`} onMouseMove={onMove}>
        <defs>
          <linearGradient id={gid} x1="0" x2="0" y1="0" y2="1">
            <stop offset="0" stopColor="var(--s1)" stopOpacity="0.10" />
            <stop offset="1" stopColor="var(--s1)" stopOpacity="0" />
          </linearGradient>
        </defs>
        {ticks.map((t, i) => (
          <g key={i}>
            <line className={i === 0 ? "baseline" : "gridline"} x1={padL} x2={W - padR} y1={y(t)} y2={y(t)} />
            <text className="tick" x={padL - 8} y={y(t) + 4} textAnchor="end">{fmt(t)}</text>
          </g>
        ))}
        {xt.map((i, k) => buckets[i] && (
          <text key={k} className="tick" x={x(i)} y={H - 6} textAnchor={k === 0 ? "start" : k === xt.length - 1 ? "end" : "middle"}>
            {new Date(buckets[i]).toLocaleString(undefined, { month: "short", day: "numeric", hour: "2-digit" })}
          </text>
        ))}
        {total > 0 && <path d={area} fill={`url(#${gid})`} />}
        {total > 0 && <path className="series-line" d={line} />}
        {hover !== null && (
          <g>
            <line className="crosshair" x1={x(hover)} x2={x(hover)} y1={padT} y2={y(0)} />
            <circle className="marker" cx={x(hover)} cy={y(vals[hover])} r={5} />
          </g>
        )}
        <rect x={padL} y={padT} width={W - padL - padR} height={H - padT - padB} fill="transparent" />
      </svg>
      {total === 0 && <div style={{ position: "absolute", inset: 0, display: "grid", placeItems: "center" }} className="muted">No {metric.label.toLowerCase()} in this window</div>}
      {hover !== null && buckets[hover] && (
        <div className="tip" style={{ left: `clamp(0px, calc(${(x(hover) / W) * 100}% + 14px), calc(100% - 190px))`, top: 8 }}>
          <div className="tip-title">{fmtBucket(buckets[hover])}</div>
          {all.map((m) => (
            <div key={m.key} className={`tip-row ${m.key === metric.key ? "primary" : ""}`}>
              <span>{m.label}</span><b>{(m.format || fmtNum)(m.values[hover])}</b>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

// Waterfall kinds take categorical slots in fixed order; rows are text-labelled, plus a legend.
const KIND_SLOT: Record<string, { color: string; label: string }> = {
  llm: { color: "var(--s1)", label: "Reasoning" },
  tool: { color: "var(--s2)", label: "Tool call" },
  subcall: { color: "var(--s3)", label: "SDK operation" },
  http: { color: "var(--s4)", label: "HTTP" },
  log: { color: "var(--s5)", label: "Log" },
};

/** APM-style trace waterfall: every span positioned by start time, sized by duration. */
export function Waterfall({ steps }: { steps: Step[] }) {
  const [hover, setHover] = useState<string | null>(null);
  const rows = steps.filter((s) => KIND_SLOT[s.kind]);
  if (!rows.length) return null;
  const t0 = Math.min(...rows.map((s) => +new Date(s.time)));
  const t1 = Math.max(...rows.map((s) => +new Date(s.time) + s.durationMs));
  const span = Math.max(1, t1 - t0);
  const pct = (ms: number) => (ms / span) * 100;
  const marks = [0, 0.25, 0.5, 0.75, 1];
  const used = Object.entries(KIND_SLOT).filter(([k]) => rows.some((r) => r.kind === k));
  return (
    <div>
      <div className="legend" style={{ padding: "12px 18px 4px" }}>
        {used.map(([k, v]) => <span key={k}><i style={{ background: v.color }} />{v.label}</span>)}
      </div>
      <div className="wf">
        <div className="h">Span</div>
        <div className="h" style={{ padding: 0 }}>
          <div className="scale">{marks.map((m) => <span key={m} style={{ left: `${m * 100}%`, top: 10 }}>{fmtDur(span * m)}</span>)}</div>
        </div>
        {rows.map((s) => {
          const left = pct(+new Date(s.time) - t0);
          const width = Math.max(0.3, pct(s.durationMs));
          const slot = KIND_SLOT[s.kind];
          return (
            <Row key={s.id + s.kind} onEnter={() => setHover(s.id)} onLeave={() => setHover(null)}>
              <div className={`nm ${s.ok ? "" : "bad"}`} style={{ paddingLeft: 12 + Math.min(s.depth, 6) * 14 }} title={s.title}>
                <i className="k" style={{ background: slot.color }} /><span>{s.title.replace(/^Tool: /, "")}</span>
              </div>
              <div className="tr">
                {marks.slice(1, -1).map((m) => <i key={m} className="gl" style={{ left: `${m * 100}%` }} />)}
                <i className={`bar ${s.ok ? "" : "bad"}`} style={{ left: `${left}%`, width: `${width}%`, background: slot.color, opacity: hover && hover !== s.id ? 0.4 : 1 }} />
                <span className="dur" style={left + width > 85 ? { right: `${100 - left + 1}%` } : { left: `calc(${left + width}% + 6px)` }}>{fmtDur(s.durationMs)}</span>
              </div>
            </Row>
          );
        })}
      </div>
    </div>
  );
}

function Row({ children, onEnter, onLeave }: { children: ReactNode; onEnter: () => void; onLeave: () => void }) {
  return <div style={{ display: "contents" }} onMouseEnter={onEnter} onMouseLeave={onLeave}>{children}</div>;
}

/** Pass / fail / error proportion bar for an eval run, with labels alongside. */
export function PassBar({ counts }: { counts?: Record<string, number> }) {
  const p = counts?.passed ?? 0, f = counts?.failed ?? 0, e = counts?.errored ?? 0;
  const total = p + f + e;
  if (!total) return <span className="muted">—</span>;
  return (
    <div className="row" style={{ gap: 10 }} title={`${p} passed · ${f} failed · ${e} errored`}>
      <div className="passbar">
        {p > 0 && <i className="p" style={{ width: `${(p / total) * 100}%` }} />}
        {f > 0 && <i className="f" style={{ width: `${(f / total) * 100}%` }} />}
        {e > 0 && <i className="e" style={{ width: `${(e / total) * 100}%` }} />}
      </div>
      <span className="num" style={{ fontSize: 12 }}><b>{Math.round((p / total) * 100)}%</b> <span className="muted">pass · {p}/{total}</span></span>
    </div>
  );
}

/** Single-series column chart (event histogram) with per-column tooltip. */
export function Histogram({ buckets, values, label, height = 90, onPick }: {
  buckets: string[]; values: number[]; label: string; height?: number; onPick?: (i: number) => void;
}) {
  const [hover, setHover] = useState<number | null>(null);
  const W = 1000, H = height, padL = 34, padB = 18, padT = 6;
  const nice = niceMax(Math.max(...values, 0));
  const bw = (W - padL) / Math.max(1, values.length);
  const y = (v: number) => padT + (H - padT - padB) * (1 - v / nice);
  const xt = values.length ? [0, Math.floor(values.length / 2), values.length - 1] : [];
  return (
    <div className="chart" onMouseLeave={() => setHover(null)}>
      <svg viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none" style={{ height: H }} role="img" aria-label={`${label} over time`}>
        {[0, nice].map((t) => (
          <g key={t}>
            <line className={t === 0 ? "baseline" : "gridline"} x1={padL} x2={W} y1={y(t)} y2={y(t)} vectorEffect="non-scaling-stroke" />
          </g>
        ))}
        {values.map((v, i) => {
          const h = y(0) - y(v);
          return (
            <g key={i}>
              {v > 0 && <rect className={`col ${hover !== null && hover !== i ? "dim" : ""}`} x={padL + i * bw + 1} y={y(v)} width={Math.max(1, bw - 2)} height={h} />}
              <rect x={padL + i * bw} y={padT} width={bw} height={H - padT - padB} fill="transparent"
                style={{ cursor: onPick ? "pointer" : "default" }} onMouseEnter={() => setHover(i)} onClick={() => onPick?.(i)} />
            </g>
          );
        })}
      </svg>
      <div style={{ position: "absolute", left: 0, top: 0, width: padL - 6, textAlign: "right" }} className="tick">
        <span style={{ fontSize: 10.5, color: "var(--muted)" }}>{fmtNum(nice)}</span>
      </div>
      <div style={{ position: "absolute", left: 0, top: H - padB - 8, width: padL - 6, textAlign: "right", fontSize: 10.5, color: "var(--muted)" }}>0</div>
      <div style={{ display: "flex", justifyContent: "space-between", paddingLeft: padL, fontSize: 10.5, color: "var(--muted)", marginTop: -14 }}>
        {xt.map((i) => <span key={i}>{buckets[i] && new Date(buckets[i]).toLocaleString(undefined, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" })}</span>)}
      </div>
      {hover !== null && buckets[hover] && (
        <div className="tip" style={{ left: `clamp(0px, calc(${((padL + hover * bw) / W) * 100}% + 10px), calc(100% - 180px))`, top: 0 }}>
          <div className="tip-title">{fmtBucket(buckets[hover])}</div>
          <div className="tip-row primary"><span>{label}</span><b>{values[hover]}</b></div>
          {onPick && <div className="tip-row"><span>Click to zoom to this bucket</span></div>}
        </div>
      )}
    </div>
  );
}
