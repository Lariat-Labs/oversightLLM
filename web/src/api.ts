// Typed client for the oversight API and its SSE stream.
import { useEffect, useRef, useState } from "react";

export type Totals = {
  runs: number; running: number; failedRuns: number; conversations: number;
  llmCalls: number; toolCalls: number; toolErrors: number;
  inputTokens: number; outputTokens: number; activeAgents: number;
};
export type NameCount = { name: string; count: number };
export type AgentStats = {
  id: string; runs: number; running: number; failedRuns: number; toolCalls: number; toolErrors: number;
  llmCalls: number; inputTokens: number; outputTokens: number; avgDurationMs: number; p95DurationMs: number; lastSeen: string;
  topTools: NameCount[]; users: string[]; timeline: Bucket[];
};
export type ToolStats = { name: string; calls: number; errors: number; avgMs: number; agents: string[]; outcomes: NameCount[] };
export type Intent = {
  conversationId: string; agent: string; intent: string; category: string; trajectory: string; direction: string;
  status: string; riskFlags: string[]; confidence: number; summary: string; model: string; judgedAt: string;
};
export type Task = {
  conversationId: string; agent: string; started: string; lastActivity: string; durationMs: number;
  status: "running" | "completed" | "failed" | "incomplete"; input: string; outputPreview: string;
  toolSequence: string[]; toolErrors: number; turns: number; tokens: number; user?: string; stopReason?: string;
  model?: string; intent?: Intent;
};
export type Bucket = { start: string; runs: number; toolCalls: number; errors: number; tokens: number };
export type FeedItem = { time: string; agent: string; conversationId: string; kind: string; text: string; ok: boolean };
export type Overview = {
  generatedAt: string; window: string; totals: Totals; agents: AgentStats[]; tools: ToolStats[];
  tasks: Task[]; timeline: Bucket[]; feed: FeedItem[];
};
export type Insight = {
  headline: string;
  themes: { theme: string; agents: string[]; count: number }[];
  agents: { agent: string; doing: string; health: "good" | "watch" | "bad" }[];
  concerns: string[]; generatedAt: string; model: string;
};
export type Agent = {
  id: string; name: string; traceId: string; kind: string; version: string; description: string; state: string;
  metadata?: Record<string, string>; createdAt: string; inFoundry: boolean; stats?: AgentStats; evalCases: number;
};
export type Step = {
  id: string; parentId?: string; time: string; kind: "user" | "llm" | "tool" | "subcall" | "http" | "answer" | "log";
  title: string; detail?: string; durationMs: number; ok: boolean; turn: number; depth: number; tokens?: number;
  attributes?: Record<string, string>;
};
export type Conversation = {
  id: string; agent: string; user?: string; model?: string; started: string; ended: string; durationMs: number;
  status: string; stopReason?: string; input: string; output: string; systemPrompt?: string; toolDefs?: string[];
  inputTokens: number; outputTokens: number; toolCalls: number; toolErrors: number; runs: number; steps: Step[];
};
export type EvalCase = {
  id: string; agent: string; query: string; expectedBehavior: string; expectedTools: string[] | null;
  response: string; actualTools: string[] | null; sourceConversationId?: string; category?: string; rationale?: string;
  origin: string; status: "suggested" | "approved" | "rejected" | "uploaded"; createdAt: string; updatedAt: string;
  upload?: { evalId: string; runId: string; dataset: string; datasetVersion: string; reportUrl: string; at: string };
};
export type FoundryEval = {
  id: string; name: string; created_at: number; metadata: Record<string, string>;
  runs: { id: string; name: string; status: string; created_at: number; report_url: string; result_counts?: Record<string, number> }[] | null;
};
export type PublishResult = {
  evalId: string; evalName: string; runId: string; runStatus: string; reportUrl: string;
  dataset: string; datasetVersion: string; mode: string; cases: number;
};
export type Settings = { autoJudge: boolean };
export type TraceRunResult = { evalId: string; evalName: string; runId: string; runStatus: string; reportUrl: string; agentId: string; lookbackHours: number; maxTraces: number };
export type JudgeInfo = {
  agentId: string; deployment: string; graderModel: string; endpoint: string; foundryAgent: boolean; traced: boolean; autoJudge: boolean; cachedIntents: number;
  stats: { calls: number; errors: number; inputTokens: number; outputTokens: number; byKind: Record<string, number>; lastCall: string; lastError?: string; since: string };
};
export type Alert = { conversationId: string; agent: string; status: string; summary: string; riskFlags: string[] | null; at: string };
export type Health = {
  ok: boolean; projectName: string; project: string; judgeDeployment: string;
  azure: { ok: boolean; account?: { user: string; subscription: string }; error?: string };
  foundry: { ok: boolean; error?: string };
  live: { ready: boolean; spans: number; lastPoll: string; error?: string; interval: string; lookback: string };
};

/** Fill in missing lists so views never read .length/.map on null (older or partial API responses). */
export function normalizeOverview(o: Overview): Overview {
  return {
    ...o,
    agents: (o.agents || []).map((a) => ({ ...a, timeline: a.timeline || [], topTools: a.topTools || [], users: a.users || [] })),
    tools: (o.tools || []).map((t) => ({ ...t, agents: t.agents || [], outcomes: t.outcomes || [] })),
    tasks: (o.tasks || []).map((t) => ({ ...t, toolSequence: t.toolSequence || [] })),
    timeline: o.timeline || [],
    feed: o.feed || [],
  };
}

export class Api {
  constructor(public base: string) {
    this.base = base.replace(/\/$/, "");
  }
  async req<T>(path: string, init?: RequestInit): Promise<T> {
    const res = await fetch(this.base + path, {
      ...init,
      headers: { "Content-Type": "application/json", ...(init?.headers || {}) },
    });
    if (res.status === 204) return undefined as T;
    const body = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(body?.error || `${res.status} ${res.statusText}`);
    return body as T;
  }
  get<T>(p: string) { return this.req<T>(p); }
  post<T>(p: string, body?: unknown) { return this.req<T>(p, { method: "POST", body: body ? JSON.stringify(body) : undefined }); }
  put<T>(p: string, body: unknown) { return this.req<T>(p, { method: "PUT", body: JSON.stringify(body) }); }
  del(p: string) { return this.req<void>(p, { method: "DELETE" }); }
}

export type ServerEvent = { type: string; agent?: string; conversationId?: string; data: any };

/** Subscribe to /api/events. Reconnects automatically (EventSource retry). */
export function useEvents(api: Api, onEvent: (e: ServerEvent) => void, filter: { agent?: string; conversation?: string } = {}) {
  const [connected, setConnected] = useState(false);
  const handler = useRef(onEvent);
  handler.current = onEvent;
  useEffect(() => {
    const q = new URLSearchParams();
    if (filter.agent) q.set("agent", filter.agent);
    if (filter.conversation) q.set("conversation", filter.conversation);
    const es = new EventSource(`${api.base}/api/events?${q}`);
    const types = ["status", "overview", "activity", "insight", "intent", "alert", "settings", "evals", "evals.uploaded", "heartbeat"];
    const listener = (ev: MessageEvent) => {
      try { handler.current(JSON.parse(ev.data)); } catch { /* ignore malformed */ }
    };
    types.forEach((t) => es.addEventListener(t, listener as EventListener));
    es.onopen = () => setConnected(true);
    es.onerror = () => setConnected(false);
    return () => es.close();
  }, [api.base, filter.agent, filter.conversation]);
  return connected;
}

/** Fetch-on-mount helper with manual reload. */
export function useLoad<T>(fn: () => Promise<T>, deps: unknown[]) {
  const [data, setData] = useState<T | undefined>();
  const [error, setError] = useState<string>();
  const [loading, setLoading] = useState(true);
  const reload = () => {
    setLoading(true);
    return fn().then((d) => { setData(d); setError(undefined); }).catch((e) => setError(String(e.message || e))).finally(() => setLoading(false));
  };
  useEffect(() => { reload(); }, deps); // eslint-disable-line react-hooks/exhaustive-deps
  return { data, setData, error, loading, reload };
}

// ---------- formatting ----------
export const fmtNum = (n: number) => (n >= 1e6 ? (n / 1e6).toFixed(1) + "M" : n >= 1e4 ? (n / 1e3).toFixed(0) + "k" : n >= 1e3 ? (n / 1e3).toFixed(1) + "k" : String(n));
export const fmtDur = (ms: number) => (ms >= 60000 ? `${(ms / 60000).toFixed(1)}m` : ms >= 1000 ? `${(ms / 1000).toFixed(1)}s` : `${Math.round(ms)}ms`);
export function fmtAgo(iso: string) {
  const t = new Date(iso).getTime();
  if (!t || t < 0) return "never";
  const s = Math.max(0, (Date.now() - t) / 1000);
  if (s < 60) return `${Math.round(s)}s ago`;
  if (s < 3600) return `${Math.round(s / 60)}m ago`;
  if (s < 86400) return `${Math.round(s / 3600)}h ago`;
  return `${Math.round(s / 86400)}d ago`;
}
export const fmtTime = (iso: string) => new Date(iso).toLocaleString(undefined, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit", second: "2-digit" });

/** Stable categorical color per agent: fixed slot order, assigned by sorted name, never cycled past 8. */
const SLOTS = 8;
export function agentColorIndex(agent: string, all: string[]) {
  const sorted = [...new Set(all)].sort();
  const i = sorted.indexOf(agent);
  return i < 0 || i >= SLOTS ? -1 : i + 1;
}
