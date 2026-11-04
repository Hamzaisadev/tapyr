import type { ReactNode } from "react";

// JsonTree renders a JSON value with collapsible objects and lists; the
// first open levels are expanded.
export function JsonTree({ value, depth = 0, name, open = 2 }: { value: unknown; depth?: number; name?: string; open?: number }): ReactNode {
  const key = name !== undefined ? <span className="k">{JSON.stringify(name)}</span> : null;
  const prefix = key ? <>{key}: </> : null;
  if (value === null) return <div>{prefix}<span className="b">null</span></div>;
  if (Array.isArray(value) || typeof value === "object") {
    const entries = Array.isArray(value) ? value.map((v, i) => [String(i), v] as const) : Object.entries(value as Record<string, unknown>);
    const [opening, closing] = Array.isArray(value) ? ["[", "]"] : ["{", "}"];
    if (entries.length === 0) return <div>{prefix}{opening}{closing}</div>;
    return (
      <details open={depth < open}>
        <summary>
          {prefix}
          {opening} <span className="muted">{entries.length} {Array.isArray(value) ? "items" : "keys"}</span>
        </summary>
        {entries.map(([k, v]) => (
          <JsonTree key={k} value={v} depth={depth + 1} open={open} name={Array.isArray(value) ? undefined : k} />
        ))}
        <div>{closing}</div>
      </details>
    );
  }
  const cls = typeof value === "string" ? "s" : typeof value === "number" ? "n" : "b";
  return (
    <div>
      {prefix}
      <span className={cls}>{JSON.stringify(value)}</span>
    </div>
  );
}
