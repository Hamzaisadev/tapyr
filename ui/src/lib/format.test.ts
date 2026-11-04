import { describe, expect, it } from "vitest";
import { count, duration, healthClass, outputHealth, plural, since, toYAML } from "./format";
import type { Output } from "./types";

describe("format", () => {
  it("formats durations compactly", () => {
    expect(duration(45)).toBe("45s");
    expect(duration(300)).toBe("5m");
    expect(duration(330)).toBe("5m30s");
    expect(duration(5400)).toBe("1h30m");
    expect(duration(90061)).toBe("1d1h");
    expect(duration(undefined)).toBe("-");
  });
  it("formats counts and plurals", () => {
    expect(count(4986)).toBe("4,986");
    expect(count(100000)).toBe("100.0k");
    expect(count(2_500_000)).toBe("2.5M");
    expect(plural(1, "instance")).toBe("1 instance");
    expect(plural(12, "instance")).toBe("12 instances");
  });
  it("shows times relative to now", () => {
    const now = Date.parse("2026-11-03T12:00:00Z");
    expect(since("2026-11-03T11:55:00Z", now)).toBe("5m ago");
    expect(since("2026-11-03T12:01:00Z", now)).toBe("in 1m");
    expect(since(undefined, now)).toBe("never");
  });
  it("renders YAML", () => {
    expect(toYAML({ job: { interval: "5m0s", path: "/a/{b}", outputs: [], n: 3, list: ["x", { k: true }] } }).trimStart()).toBe(
      'job:\n  interval: "5m0s"\n  path: "/a/{b}"\n  outputs: []\n  n: 3\n  list:\n    - x\n    - k: true',
    );
  });
  it("maps states to classes", () => {
    expect(healthClass("success")).toBe("ok");
    expect(healthClass("skipped_budget")).toBe("warn");
    expect(healthClass("open")).toBe("bad");
    expect(healthClass("whatever")).toBe("idle");
  });
});

describe("output health", () => {
  const push: Output = { name: "vm", type: "prometheus_remote_write", queue: 0, capacity: 100, sent: 0, dropped: 0 };
  it.each([
    { name: "a scraped output is always ok", o: { ...push, type: "prometheus", last_error: "x", last_error_at: "2026-11-03T12:00:00Z" }, want: "ok" },
    { name: "nothing sent yet is unknown", o: push, want: "unknown" },
    { name: "a success is ok", o: { ...push, last_success: "2026-11-03T12:00:00Z" }, want: "ok" },
    { name: "an error without a success is failing", o: { ...push, last_error: "refused", last_error_at: "2026-11-03T12:00:00Z" }, want: "failing" },
    { name: "an error after the last success is failing", o: { ...push, last_success: "2026-11-03T11:00:00Z", last_error: "refused", last_error_at: "2026-11-03T12:00:00Z" }, want: "failing" },
    { name: "a success after the last error is ok", o: { ...push, last_success: "2026-11-03T12:00:00Z", last_error: "refused", last_error_at: "2026-11-03T11:00:00Z" }, want: "ok" },
  ])("$name", ({ o, want }) => {
    expect(outputHealth(o)).toBe(want);
  });
});
