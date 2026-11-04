import { cleanup, render } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { highlight } from "./Code";

// tokens renders the highlighted code and lists its colored spans as
// "class:text", so that a test reads like the code it checks.
function tokens(code: string, lang: "yaml" | "json" | "text"): { text: string; spans: string[] } {
  const { container } = render(<code>{highlight(code, lang)}</code>);
  return {
    text: container.textContent ?? "",
    spans: [...container.querySelectorAll("span")].map((s) => `${s.className}:${s.textContent}`),
  };
}

describe("highlight", () => {
  afterEach(cleanup);

  it("leaves plain text alone", () => {
    expect(highlight("a: 1", "text")).toEqual(["a: 1"]);
  });

  it("colors YAML keys, strings, numbers, booleans and comments", () => {
    const code = 'job:\n  interval: "5m0s" # every five minutes\n  - limit: 10\n  enabled: true';
    const t = tokens(code, "yaml");
    expect(t.text).toBe(code);
    expect(t.spans).toEqual(["k:job", "k:interval", 's:"5m0s"', "c:# every five minutes", "k:limit", "n:10", "k:enabled", "v:true"]);
  });

  it("colors JSON keys apart from string values", () => {
    const code = '{"name": "devices", "count": -1.5e3, "ok": false, "none": null}';
    const t = tokens(code, "json");
    expect(t.text).toBe(code);
    expect(t.spans).toEqual(['k:"name"', 's:"devices"', 'k:"count"', "n:-1.5e3", 'k:"ok"', "v:false", 'k:"none"', "v:null"]);
  });

  it("keeps escaped quotes inside JSON strings", () => {
    const code = '{"q": "say \\"hi\\""}';
    expect(tokens(code, "json").spans).toEqual(['k:"q"', 's:"say \\"hi\\""']);
  });
});
