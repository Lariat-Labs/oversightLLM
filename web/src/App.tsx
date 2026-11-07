import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Api, useEvents, useLoad, type Alert, type Health, type Overview as OverviewData, type ServerEvent, type Settings } from "./api";
import { Icon, type IconName } from "./components/icons";
import { ErrorBoundary, Switch } from "./components/ui";
import { CommandPalette } from "./components/CommandPalette";
import { Overview } from "./views/Overview";
import { Search } from "./views/Search";
import { Agents } from "./views/Agents";
import { AgentDetail } from "./views/AgentDetail";
import { ConversationView } from "./views/Conversation";
import { Evals } from "./views/Evals";

export type Route =
  | { view: "overview"; agent?: string }
  | { view: "search"; q?: string }
  | { view: "agents" }
  | { view: "agent"; id: string }
  | { view: "conversation"; id: string; from?: Route }
  | { view: "evals"; agent?: string };

export type Toast = { id: number; kind: "good" | "error" | "info" | "alert"; title: string; body?: ReactNode; action?: { label: string; run: () => void } };

export type TimeRange = { key: string; label: string; window: string; ms: number };
export const RANGES: TimeRange[] = [
  { key: "15m", label: "Last 15 minutes", window: "15m", ms: 15 * 60e3 },
  { key: "1h", label: "Last 60 minutes", window: "1h", ms: 3600e3 },
  { key: "4h", label: "Last 4 hours", window: "4h", ms: 4 * 3600e3 },
  { key: "24h", label: "Last 24 hours", window: "24h", ms: 24 * 3600e3 },
  { key: "3d", label: "Last 3 days", window: "72h", ms: 72 * 3600e3 },
  { key: "7d", label: "Last 7 days", window: "168h", ms: 168 * 3600e3 },
];
export const DEFAULT_RANGE = RANGES[RANGES.length - 1];

type Ctx = {
  api: Api;
  route: Route;
  go: (r: Route) => void;
  overview?: OverviewData;
  lastEvent?: ServerEvent;
  agentIds: string[];
  freshFeed: Set<string>;
  alerts: Alert[];
  dismissAlerts: () => void;
  settings?: Settings;
  setAutoJudge: (on: boolean) => void;
  toast: (t: Omit<Toast, "id">) => void;
  health?: Health;
  range: TimeRange;
  setRange: (r: TimeRange) => void;
};
const AppCtx = createContext<Ctx>(null as unknown as Ctx);
export const useApp = () => useContext(AppCtx);

// ---------- hash routing (opt-in via routing="hash") ----------
function toHash(r: Route): string {
  switch (r.view) {
    case "conversation": return `#/run/${encodeURIComponent(r.id)}`;
    case "agent": return `#/agents/${encodeURIComponent(r.id)}`;
    case "agents": return "#/agents";
    case "search": return `#/search${r.q ? `?q=${encodeURIComponent(r.q)}` : ""}`;
    default: return `#/${r.view}${r.agent ? `?agent=${encodeURIComponent(r.agent)}` : ""}`;
  }
}
function fromHash(h: string): Route | null {
  const m = h.replace(/^#\/?/, "");
  if (!m) return null;
  const [path, qs] = m.split("?");
  const p = new URLSearchParams(qs || "");
  const [view, id] = path.split("/");
  if ((view === "run" || view === "conversation") && id) return { view: "conversation", id: decodeURIComponent(id) };
  if (view === "agents" && id) return { view: "agent", id: decodeURIComponent(id) };
  if (view === "agents") return { view: "agents" };
  if (view === "search" || view === "runs") return { view: "search", q: p.get("q") || undefined };
  if (view === "overview" || view === "evals") return { view, agent: p.get("agent") || undefined };
  return null;
}

const NAV: { view: "overview" | "search" | "agents" | "evals"; label: string; icon: IconName }[] = [
  { view: "overview", label: "Overview", icon: "grid" },
  { view: "search", label: "Search", icon: "search" },
  { view: "agents", label: "Agents", icon: "bot" },
  { view: "evals", label: "Evals", icon: "flask" },
];

export function App({ apiBase, initialAgent, host, hashRouting }: { apiBase: string; initialAgent?: string; host: HTMLElement; hashRouting: boolean }) {
  const api = useMemo(() => new Api(apiBase), [apiBase]);
  const [route, setRoute] = useState<Route>(() => (hashRouting && fromHash(location.hash)) || { view: "overview", agent: initialAgent });
  const [range, setRange] = useState<TimeRange>(DEFAULT_RANGE);
  const [overview, setOverview] = useState<OverviewData>();
  const [lastEvent, setLastEvent] = useState<ServerEvent>();
  const [freshFeed, setFreshFeed] = useState<Set<string>>(new Set());
  const [toasts, setToasts] = useState<Toast[]>([]);
  const [palette, setPalette] = useState(false);
  const health = useLoad<Health>(() => api.get("/api/health"), [api]);
  const settings = useLoad<Settings>(() => api.get("/api/settings"), [api]);
  const alertsLoad = useLoad<Alert[]>(() => api.get("/api/alerts"), [api]);
  const [dismissedAt, setDismissedAt] = useState(0);
  const alerts = (alertsLoad.data || []).filter((a) => new Date(a.at).getTime() > dismissedAt);

  const toast = useCallback((t: Omit<Toast, "id">) => {
    const id = Date.now() + Math.random();
    setToasts((ts) => [...ts.slice(-3), { ...t, id }]);
    setTimeout(() => setToasts((ts) => ts.filter((x) => x.id !== id)), t.kind === "error" ? 8000 : 5000);
  }, []);

  const go = useCallback((r: Route) => {
    setRoute(r);
    if (hashRouting && location.hash !== toHash(r)) history.pushState(null, "", toHash(r));
    host.dispatchEvent(new CustomEvent("oversight:navigate", { detail: r, bubbles: true, composed: true }));
    window.scrollTo?.({ top: 0 });
  }, [hashRouting, host]);

  useEffect(() => {
    if (!hashRouting) return;
    const onPop = () => { const r = fromHash(location.hash); if (r) setRoute(r); };
    window.addEventListener("popstate", onPop);
    return () => window.removeEventListener("popstate", onPop);
  }, [hashRouting]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") { e.preventDefault(); setPalette((p) => !p); }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  const setAutoJudge = async (on: boolean) => {
    try {
      settings.setData(await api.put<Settings>("/api/settings", { autoJudge: on }));
      toast({ kind: "info", title: on ? "Auto-judge on" : "Auto-judge off", body: on ? "Each finished run is judged; drift and failures raise alerts." : "The judge runs only when you ask." });
    } catch (e: any) { toast({ kind: "error", title: "Couldn't change auto-judge", body: e.message }); }
  };

  const connected = useEvents(api, (e) => {
    if (e.type === "heartbeat") return;
    if (e.type === "overview") setOverview(e.data);
    if (e.type === "activity") setFreshFeed((s) => new Set(s).add(e.data.time + e.data.text));
    if (e.type === "settings") settings.setData(e.data);
    if (e.type === "alert") {
      alertsLoad.setData((list) => [e.data, ...(list || [])].slice(0, 50));
      toast({ kind: "alert", title: `${e.data.agent}: run ${e.data.status}`, body: e.data.summary, action: { label: "Open run", run: () => go({ view: "conversation", id: e.data.conversationId }) } });
    }
    setLastEvent(e);
  });

  const agentIds = useMemo(() => (overview?.agents || []).map((a) => a.id), [overview]);
  const h = health.data;
  const user = h?.azure?.account?.user || "";
  const navActive = route.view === "conversation" ? (route.from?.view === "agent" ? "agents" : route.from?.view || "search")
    : route.view === "agent" ? "agents" : route.view;

  return (
    <AppCtx.Provider value={{ api, route, go, overview, lastEvent, agentIds, freshFeed, alerts, dismissAlerts: () => setDismissedAt(Date.now()), settings: settings.data, setAutoJudge, toast, health: h, range, setRange }}>
      <header className="appbar">
        <div className="brand">
          <span className="brand-mark"><Icon name="eye" size={15} strokeWidth={2} /></span>
          Oversight {h && <small>{h.projectName}</small>}
        </div>
        <nav className="appnav" aria-label="Main">
          {NAV.map((n) => (
            <button key={n.view} className={navActive === n.view ? "on" : ""} onClick={() => go({ view: n.view } as Route)}>
              <Icon name={n.icon} size={15} /> {n.label}
              {n.view === "overview" && alerts.length > 0 && <span className="badge">{alerts.length}</span>}
            </button>
          ))}
        </nav>
        <div className="appbar-right">
          <button className="bar-search" onClick={() => setPalette(true)}><Icon name="search" size={13} /> Jump to run, agent, page<span className="kbd">⌘K</span></button>
          <span className="bar-sep" />
          <span className="bar-item hide-sm bar-switch" title="Judge every finished run and alert on drift or failure">
            {settings.data && <Switch checked={settings.data.autoJudge} onChange={setAutoJudge} label={<span>Auto-judge</span>} />}
          </span>
          <span className="bar-item hide-sm" title={`Judge model deployment (not a Foundry agent)`}><Icon name="cpu" size={13} /> <b>{h?.judgeDeployment || "—"}</b></span>
          <span className="bar-item" title={h?.live?.error || (h?.live ? `Polling App Insights every ${h.live.interval}` : "")}>
            <span className={`dot ${connected ? "on" : "off"}`} /> {connected ? "Live" : "Offline"}
          </span>
          <span className="bar-sep" />
          <span className="bar-item" title={`${user} · ${h?.azure?.account?.subscription || ""}`}><span className="avatar-user">{user.slice(0, 2).toUpperCase() || "?"}</span></span>
        </div>
      </header>

      <main className="page">
        {health.error && <ErrorBanner>Can't reach the Oversight API at <b>{apiBase}</b>. Is it running? ({health.error})</ErrorBanner>}
        {h && !h.azure.ok && <ErrorBanner>Azure CLI: {h.azure.error}. Run <code>make login</code>.</ErrorBanner>}
        {h && !h.foundry.ok && <ErrorBanner>Foundry: {h.foundry.error}</ErrorBanner>}
        {h?.live?.error && <ErrorBanner>Telemetry: {h.live.error}</ErrorBanner>}

        <ErrorBoundary key={JSON.stringify(route)} label={route.view[0].toUpperCase() + route.view.slice(1)}>
          {route.view === "overview" && <Overview key={"o" + (route.agent || "")} agent={route.agent} />}
          {route.view === "search" && <Search key={"s" + (route.q || "")} initialQuery={route.q} />}
          {route.view === "agents" && <Agents />}
          {route.view === "agent" && <AgentDetail key={route.id} id={route.id} />}
          {route.view === "conversation" && <ConversationView key={route.id} id={route.id} from={route.from} />}
          {route.view === "evals" && <Evals key={"e" + (route.agent || "")} initialAgent={route.agent} />}
        </ErrorBoundary>
      </main>

      {palette && <CommandPalette onClose={() => setPalette(false)} />}

      <div className="toasts" aria-live="polite">
        {toasts.map((t) => (
          <div key={t.id} className={`toast ${t.kind}`}>
            <span className="ic"><Icon name={t.kind === "good" ? "checkCircle" : t.kind === "error" ? "xCircle" : t.kind === "alert" ? "alert" : "info"} /></span>
            <div className="grow">
              <b>{t.title}</b>
              {t.body && <p>{t.body}</p>}
              {t.action && <button className="btn sm" style={{ marginTop: 6 }} onClick={() => { t.action!.run(); setToasts((ts) => ts.filter((x) => x.id !== t.id)); }}>{t.action.label}</button>}
            </div>
            <button className="x" onClick={() => setToasts((ts) => ts.filter((x) => x.id !== t.id))} aria-label="Dismiss"><Icon name="x" size={14} /></button>
          </div>
        ))}
      </div>
    </AppCtx.Provider>
  );
}

function ErrorBanner({ children }: { children: ReactNode }) {
  return <div className="banner error"><Icon name="alert" /><div>{children}</div></div>;
}

/** Global time range picker; every view reads the same range. */
export function TimePicker() {
  const { range, setRange } = useApp();
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const close = (e: MouseEvent) => { if (!e.composedPath().includes(ref.current!)) setOpen(false); };
    window.addEventListener("mousedown", close);
    return () => window.removeEventListener("mousedown", close);
  }, [open]);
  return (
    <div className="dropdown" ref={ref}>
      <button className="btn" onClick={() => setOpen(!open)} aria-haspopup="menu" aria-expanded={open}>
        <Icon name="clock" size={13} /> {range.label} <Icon name="chevronDown" size={13} />
      </button>
      {open && (
        <div className="menu" role="menu">
          <div className="menu-h">Relative</div>
          {RANGES.map((r) => (
            <button key={r.key} role="menuitemradio" aria-checked={r.key === range.key} className={r.key === range.key ? "on" : ""} onClick={() => { setRange(r); setOpen(false); }}>
              <span style={{ width: 14 }}>{r.key === range.key && <Icon name="check" size={13} />}</span>{r.label}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
