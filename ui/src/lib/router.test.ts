import { describe, expect, it } from "vitest";
import { href, parseHash } from "./router";

describe("router", () => {
  it("parses and builds hashes", () => {
    const r = parseHash("#/jobs/rutos%20system?instance=a%3Db");
    expect(r.path).toEqual(["jobs", "rutos system"]);
    expect(r.query.get("instance")).toBe("a=b");
    expect(parseHash("").path).toEqual([]);
    expect(href(["debug"], { job: "x y" })).toBe("#/debug?job=x+y");
  });
  it("round-trips paths with reserved characters", () => {
    const path = ["jobs", "a/b c?d"];
    expect(parseHash(href(path)).path).toEqual(path);
  });
});
