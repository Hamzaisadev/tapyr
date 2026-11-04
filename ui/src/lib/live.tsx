import { createContext, useCallback, useContext, useEffect, useEffectEvent, useRef, useState, type ReactNode } from "react";
import { apiBase, fetchAll, fetchJSON } from "./api";

// Live updates: every event of the stream bumps a version that the data
// hooks refetch on (at most every second); without the stream, they poll
// every 15 seconds.
export const pollInterval = 15_000;
const coalesce = 1_000;

export interface LiveEvent {
  type: string;
  data: Record<string, unknown>;
}

interface Live {
  connected: boolean;
  version: number;
  subscribe: (fn: (e: LiveEvent) => void) => () => void;
}

const LiveContext = createContext<Live>({ connected: false, version: 0, subscribe: () => () => {} });

const eventTypes = ["run_finished", "plan_changed", "circuit_changed", "config_reloaded"];

export function LiveProvider({ children }: { children: ReactNode }) {
  const [connected, setConnected] = useState(false);
  const [version, setVersion] = useState(0);
  const listeners = useRef(new Set<(e: LiveEvent) => void>());
  const pending = useRef<ReturnType<typeof setTimeout> | null>(null);

  const bump = useCallback(() => {
    if (pending.current) return;
    pending.current = setTimeout(() => {
      pending.current = null;
      setVersion((v) => v + 1);
    }, coalesce);
  }, []);

  useEffect(() => {
    if (typeof EventSource === "undefined") return;
    const es = new EventSource(apiBase + "/events");
    es.onopen = () => setConnected(true);
    es.onerror = () => setConnected(es.readyState === EventSource.OPEN);
    const handlers = eventTypes.map((type) => {
      const h = (msg: MessageEvent<string>) => {
        let data: Record<string, unknown> = {};
        try {
          data = JSON.parse(msg.data) as Record<string, unknown>;
        } catch {
          // keep an empty payload
        }
        for (const fn of listeners.current) fn({ type, data });
        bump();
      };
      es.addEventListener(type, h as EventListener);
      return [type, h] as const;
    });
    return () => {
      for (const [type, h] of handlers) es.removeEventListener(type, h as EventListener);
      es.close();
    };
  }, [bump]);

  useEffect(() => {
    if (connected) return;
    const id = setInterval(() => setVersion((v) => v + 1), pollInterval);
    return () => clearInterval(id);
  }, [connected]);

  const subscribe = useCallback((fn: (e: LiveEvent) => void) => {
    listeners.current.add(fn);
    return () => {
      listeners.current.delete(fn);
    };
  }, []);

  return <LiveContext.Provider value={{ connected, version, subscribe }}>{children}</LiveContext.Provider>;
}

export function useLive() {
  return useContext(LiveContext);
}

export interface Remote<T> {
  data?: T;
  error?: Error;
  loading: boolean;
}

// useRemote loads a value and reloads it on every live update, keeping the
// previous value on screen while it reloads.
export function useRemote<T>(load: ((signal: AbortSignal) => Promise<T>) | null, key: string): Remote<T> {
  const { version } = useLive();
  const [state, setState] = useState<{ key?: string; version?: number; data?: T; error?: Error }>({});
  const start = useEffectEvent((signal: AbortSignal) => (load ? load(signal) : null));
  useEffect(() => {
    const ctrl = new AbortController();
    start(ctrl.signal)?.then(
      (data) => setState({ key, version, data }),
      (error: unknown) => {
        if (ctrl.signal.aborted) return;
        setState((s) => ({ key, version, data: s.key === key ? s.data : undefined, error: error as Error }));
      },
    );
    return () => ctrl.abort();
  }, [key, version]);
  const same = state.key === key;
  return {
    data: same ? state.data : undefined,
    error: same ? state.error : undefined,
    loading: load !== null && (!same || state.version !== version),
  };
}

export function useJSON<T>(path: string | null): Remote<T> {
  return useRemote<T>(path ? (signal) => fetchJSON<T>(path, signal) : null, path ?? "");
}

export function useList<T>(path: string | null): Remote<T[]> {
  return useRemote<T[]>(path ? (signal) => fetchAll<T>(path, signal) : null, "list:" + (path ?? ""));
}

// useNow returns the current time, refreshed every interval, for the views
// that compare times with now.
export function useNow(interval = 5_000): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), interval);
    return () => clearInterval(id);
  }, [interval]);
  return now;
}
