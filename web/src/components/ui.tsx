import { Component, useEffect, type ErrorInfo, type ReactNode } from "react";
import { agentColorIndex } from "../api";
import { Icon, type IconName } from "./icons";

// ---------- Status pill: icon + label, never color alone ----------
const STATUS: Record<string, { cls: string; label: string; icon: IconName }> = {
  completed: { cls: "good", label: "Completed", icon: "checkCircle" },
  running: { cls: "running", label: "Running", icon: "loader" },
  failed: { cls: "critical", label: "Failed", icon: "xCircle" },
  incomplete: { cls: "warning", label: "Incomplete", icon: "alert" },
  on_track: { cls: "good", label: "On track", icon: "checkCircle" },
  drifting: { cls: "warning", label: "Drifting", icon: "compass" },
  stuck: { cls: "serious", label: "Stuck", icon: "alert" },
  good: { cls: "good", label: "Healthy", icon: "checkCircle" },
  watch: { cls: "warning", label: "Watch", icon: "eye" },
  bad: { cls: "critical", label: "At risk", icon: "alert" },
  suggested: { cls: "info", label: "Suggested", icon: "sparkles" },
  approved: { cls: "good", label: "Approved", icon: "check" },
  rejected: { cls: "neutral", label: "Rejected", icon: "x" },
  uploaded: { cls: "info", label: "In Foundry", icon: "upload" },
  queued: { cls: "neutral", label: "Queued", icon: "clock" },
  in_progress: { cls: "running", label: "Running", icon: "loader" },
  canceled: { cls: "neutral", label: "Canceled", icon: "x" },
};

export function Status({ status, label }: { status: string; label?: string }) {
  const s = STATUS[status] || { cls: "neutral", label: status || "Unknown", icon: "info" as IconName };
  return (
    <span className={`pill ${s.cls}`}>
      <span className="ic"><Icon name={s.icon} size={12} strokeWidth={2.2} className={s.icon === "loader" ? "spin" : undefined} /></span>
      {label ?? s.label}
    </span>
  );
}

// ---------- Agent identity: fixed categorical slot by sorted name ----------
export function AgentAvatar({ id, all, size }: { id: string; all: string[]; size?: "lg" }) {
  const i = agentColorIndex(id, all);
  const initials = (id || "?").split(/[-_\s]/).filter(Boolean).slice(0, 2).map((p) => p[0]).join("");
  return <span className={`agent-av ${size || ""} ${i > 0 ? "s" + i : ""}`} aria-hidden>{initials}</span>;
}

export function AgentChip({ id, all }: { id: string; all: string[] }) {
  return <span className="agent-chip"><AgentAvatar id={id} all={all} /><span className="trunc">{id || "(unknown)"}</span></span>;
}

export function ToolChain({ tools, max = 6, errors = 0 }: { tools: string[]; max?: number; errors?: number }) {
  if (!tools?.length) return <span className="muted" style={{ fontSize: 12 }}>No tools</span>;
  const shown = tools.slice(0, max);
  return (
    <span className="chain-chips">
      {shown.map((t, i) => (
        <span key={i} style={{ display: "contents" }}>
          {i > 0 && <span className="sep"><Icon name="chevronRight" size={11} /></span>}
          <span className="chip">{t}</span>
        </span>
      ))}
      {tools.length > max && <span className="muted" style={{ fontSize: 12, marginLeft: 4 }}>+{tools.length - max}</span>}
      {errors > 0 && <span className="chip err" style={{ marginLeft: 6 }}>{errors} failed</span>}
    </span>
  );
}

// ---------- Layout ----------
export function Panel({ title, icon, sub, actions, children, className = "", bodyClass = "" }: {
  title?: ReactNode; icon?: IconName; sub?: ReactNode; actions?: ReactNode; children: ReactNode; className?: string; bodyClass?: string;
}) {
  return (
    <section className={`panel ${className}`}>
      {(title || actions) && (
        <div className="panel-head">
          <div className="panel-title">{icon && <Icon name={icon} />}{title}</div>
          {sub && <span className="panel-sub">{sub}</span>}
          <span className="grow" />
          {actions}
        </div>
      )}
      <div className={`panel-body ${bodyClass}`}>{children}</div>
    </section>
  );
}

export function PageHead({ title, sub, actions }: { title: ReactNode; sub?: ReactNode; actions?: ReactNode }) {
  return (
    <div className="page-head fade-in">
      <div className="grow">
        <h1 className="page-title">{title}</h1>
        {sub && <p className="page-sub">{sub}</p>}
      </div>
      {actions && <div className="row wrap">{actions}</div>}
    </div>
  );
}

export function Segmented<T extends string>({ value, options, onChange }: {
  value: T; options: { value: T; label: ReactNode; n?: number }[]; onChange: (v: T) => void;
}) {
  return (
    <div className="seg" role="tablist">
      {options.map((o) => (
        <button key={o.value} role="tab" aria-selected={value === o.value} className={value === o.value ? "on" : ""} onClick={() => onChange(o.value)}>
          {o.label}{o.n !== undefined && <span className="n">{o.n}</span>}
        </button>
      ))}
    </div>
  );
}

export function Switch({ checked, onChange, label }: { checked: boolean; onChange: (v: boolean) => void; label?: ReactNode }) {
  return (
    <label className="switch">
      <input type="checkbox" role="switch" checked={checked} onChange={(e) => onChange(e.target.checked)} />
      <span className="track" aria-hidden><span className="thumb" /></span>
      {label}
    </label>
  );
}

export function Empty({ icon = "info", title, children, action }: { icon?: IconName; title: string; children?: ReactNode; action?: ReactNode }) {
  return (
    <div className="empty">
      <div className="empty-ic"><Icon name={icon} size={20} /></div>
      <h4>{title}</h4>
      {children && <p>{children}</p>}
      {action}
    </div>
  );
}

export function Skeleton({ h = 16, w = "100%", r }: { h?: number; w?: number | string; r?: number }) {
  return <div className="skel" style={{ height: h, width: w, borderRadius: r }} />;
}

export function Banner({ kind = "info", children }: { kind?: "info" | "error" | "good"; children: ReactNode }) {
  return <div className={`banner ${kind}`}><Icon name={kind === "error" ? "alert" : kind === "good" ? "checkCircle" : "info"} />{<div>{children}</div>}</div>;
}

// ---------- Overlays ----------
function useEscape(onClose: () => void) {
  useEffect(() => {
    const h = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", h);
    return () => window.removeEventListener("keydown", h);
  }, [onClose]);
}

export function Drawer({ title, icon, onClose, children, footer }: { title: ReactNode; icon?: IconName; onClose: () => void; children: ReactNode; footer?: ReactNode }) {
  useEscape(onClose);
  return (
    <>
      <div className="overlay" onClick={onClose} />
      <aside className="drawer" role="dialog" aria-modal="true">
        <div className="drawer-head">
          {icon && <div className="modal-ic" style={{ width: 34, height: 34 }}><Icon name={icon} /></div>}
          <h3 style={{ fontSize: 16, fontWeight: 650 }} className="grow">{title}</h3>
          <button className="btn ghost icon" onClick={onClose} aria-label="Close"><Icon name="x" /></button>
        </div>
        <div className="drawer-body">{children}</div>
        {footer && <div className="drawer-foot">{footer}</div>}
      </aside>
    </>
  );
}

export function Modal({ onClose, children, footer }: { onClose: () => void; children: ReactNode; footer?: ReactNode }) {
  useEscape(onClose);
  return (
    <>
      <div className="overlay" onClick={onClose} />
      <div className="modal" role="dialog" aria-modal="true">
        <div className="modal-body">{children}</div>
        {footer && <div className="modal-foot">{footer}</div>}
      </div>
    </>
  );
}

/** Keeps one broken view from blanking the whole app; shows the error and a retry. */
export class ErrorBoundary extends Component<{ children: ReactNode; label?: string }, { error?: Error }> {
  state: { error?: Error } = {};
  static getDerivedStateFromError(error: Error) { return { error }; }
  componentDidCatch(error: Error, info: ErrorInfo) { console.error("Oversight view crashed:", error, info.componentStack); }
  render() {
    if (!this.state.error) return this.props.children;
    return (
      <div className="banner error">
        <Icon name="alert" />
        <div className="grow">
          <b>{this.props.label || "This view"} hit an error.</b> {this.state.error.message}
          <div className="note" style={{ marginTop: 4 }}>If you just updated Oversight, restart the API (<span className="mono">make dev</span>) so the backend matches the frontend.</div>
        </div>
        <button className="btn sm" onClick={() => this.setState({ error: undefined })}>Retry</button>
      </div>
    );
  }
}
