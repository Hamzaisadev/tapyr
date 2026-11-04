import { act, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { LiveProvider, pollInterval, useJSON, useLive } from "./live";

// FakeEventSource stands for the event stream of tapyr run.
class FakeEventSource {
  static last: FakeEventSource | null = null;
  static readonly OPEN = 1;
  readyState = 0;
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  private listeners = new Map<string, ((e: MessageEvent<string>) => void)[]>();
  constructor(readonly url: string) {
    FakeEventSource.last = this;
  }
  addEventListener(type: string, fn: (e: MessageEvent<string>) => void) {
    this.listeners.set(type, [...(this.listeners.get(type) ?? []), fn]);
  }
  removeEventListener() {}
  close() {}
  open() {
    this.readyState = 1;
    this.onopen?.();
  }
  emit(type: string, data: object) {
    for (const fn of this.listeners.get(type) ?? []) fn(new MessageEvent(type, { data: JSON.stringify(data) }));
  }
}

function Probe() {
  const { connected } = useLive();
  const status = useJSON<{ instances: number }>("/status");
  return (
    <div>
      <span data-testid="instances">{status.data?.instances ?? "-"}</span>
      <span data-testid="live">{connected ? "live" : "polling"}</span>
    </div>
  );
}

describe("live updates", () => {
  let calls = 0;
  beforeEach(() => {
    vi.useFakeTimers();
    calls = 0;
    vi.stubGlobal("EventSource", FakeEventSource);
    vi.stubGlobal("fetch", vi.fn(async () => {
      calls++;
      return new Response(JSON.stringify({ instances: calls }), { status: 200 });
    }));
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it("refetches after events, coalesced, and polls without the stream", async () => {
    render(<LiveProvider><Probe /></LiveProvider>);
    await act(async () => {});
    expect(screen.getByTestId("instances").textContent).toBe("1");
    expect(FakeEventSource.last?.url).toBe("/api/v1/events");

    // Without the stream, the data is polled.
    await act(async () => { await vi.advanceTimersByTimeAsync(pollInterval); });
    expect(screen.getByTestId("instances").textContent).toBe("2");

    // With it, events trigger one refetch per second at most.
    await act(async () => { FakeEventSource.last!.open(); });
    expect(screen.getByTestId("live").textContent).toBe("live");
    await act(async () => {
      FakeEventSource.last!.emit("run_finished", { job: "devices" });
      FakeEventSource.last!.emit("run_finished", { job: "sites" });
      await vi.advanceTimersByTimeAsync(1_000);
    });
    expect(screen.getByTestId("instances").textContent).toBe("3");
    await act(async () => { await vi.advanceTimersByTimeAsync(pollInterval); });
    expect(calls).toBe(3); // no polling while the stream is up
  });
});
