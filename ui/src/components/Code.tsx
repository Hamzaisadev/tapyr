import { useEffect, useRef, useState, type ReactNode } from "react";
import { Icon } from "./Icon";

// copyText writes to the clipboard, falling back to a selection copy on
// pages served over plain HTTP (no Clipboard API outside secure contexts).
async function copyText(text: string, node: HTMLElement | null): Promise<boolean> {
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await Promise.race([navigator.clipboard.writeText(text), new Promise((_, no) => setTimeout(no, 800))]);
      return true;
    }
  } catch {
    // fall back below
  }
  if (!node) return false;
  const range = document.createRange();
  range.selectNodeContents(node);
  const selection = window.getSelection();
  selection?.removeAllRanges();
  selection?.addRange(range);
  let ok: boolean;
  try {
    ok = document.execCommand("copy");
  } catch {
    ok = false;
  }
  selection?.removeAllRanges();
  return ok;
}

// CopyButton copies text, as on tapyr.eu: a check mark for 1.6 seconds.
export function CopyButton({ text, target }: { text: string; target?: React.RefObject<HTMLElement | null> }) {
  const [state, setState] = useState<"idle" | "done" | "failed">("idle");
  useEffect(() => {
    if (state === "idle") return;
    const id = setTimeout(() => setState("idle"), 1600);
    return () => clearTimeout(id);
  }, [state]);
  const label = state === "done" ? "Copied" : state === "failed" ? "Copy failed" : "Copy";
  return (
    <button
      type="button"
      className={`copy${state === "done" ? " done" : ""}`}
      aria-label={label}
      title={label}
      onClick={async () => setState((await copyText(text, target?.current ?? null)) ? "done" : "failed")}
    >
      <Icon name={state === "done" ? "check" : "copy"} />
    </button>
  );
}

type Lang = "yaml" | "json" | "text";

// highlight colors YAML and JSON with the classes of the site (keys,
// strings, numbers, comments).
export function highlight(code: string, lang: Lang): ReactNode[] {
  if (lang === "text") return [code];
  const out: ReactNode[] = [];
  const re =
    lang === "json"
      ? /("(?:[^"\\]|\\.)*")(\s*:)?|(-?\b\d+(?:\.\d+)?(?:[eE][+-]?\d+)?\b)|\b(true|false|null)\b/g
      : /^(\s*-?\s*)([\w.-]+|"[^"]*")(:)|(#.*$)|("(?:[^"\\]|\\.)*")|(-?\b\d+(?:\.\d+)?\b)|\b(true|false|null)\b/gm;
  let last = 0;
  let i = 0;
  for (const m of code.matchAll(re)) {
    const start = m.index;
    if (start > last) out.push(code.slice(last, start));
    if (lang === "json") {
      if (m[1] !== undefined) out.push(<span key={i++} className={m[2] ? "k" : "s"}>{m[1]}</span>, m[2] ?? "");
      else if (m[3] !== undefined) out.push(<span key={i++} className="n">{m[3]}</span>);
      else out.push(<span key={i++} className="v">{m[4]}</span>);
    } else if (m[2] !== undefined) {
      out.push(m[1] ?? "", <span key={i++} className="k">{m[2]}</span>, m[3] ?? "");
    } else if (m[4] !== undefined) out.push(<span key={i++} className="c">{m[4]}</span>);
    else if (m[5] !== undefined) out.push(<span key={i++} className="s">{m[5]}</span>);
    else if (m[6] !== undefined) out.push(<span key={i++} className="n">{m[6]}</span>);
    else out.push(<span key={i++} className="v">{m[7]}</span>);
    last = start + m[0].length;
  }
  if (last < code.length) out.push(code.slice(last));
  return out;
}

// CodeBlock is a code block of the site, with its copy button.
export function CodeBlock({ code, lang = "text", maxHeight }: { code: string; lang?: Lang; maxHeight?: number }) {
  const ref = useRef<HTMLPreElement>(null);
  return (
    <div className="codewrap">
      <pre className="code" ref={ref} style={maxHeight ? { maxHeight } : undefined}>
        <code>{highlight(code, lang)}</code>
      </pre>
      <CopyButton text={code} target={ref} />
    </div>
  );
}
