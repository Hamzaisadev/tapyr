import { useEffect, useState } from "react";

// A hash router: #/budgets, #/jobs/devices?instance=x. The UI is served
// under /ui/ by the binary, so the paths never reach the server.
export interface Route {
  path: string[];
  query: URLSearchParams;
}

export function parseHash(hash: string): Route {
  const raw = hash.replace(/^#\/?/, "");
  const [p = "", q = ""] = raw.split("?", 2);
  return { path: p.split("/").filter(Boolean).map(decodeURIComponent), query: new URLSearchParams(q) };
}

export function useRoute(): Route {
  const [route, setRoute] = useState(() => parseHash(window.location.hash));
  useEffect(() => {
    const on = () => setRoute(parseHash(window.location.hash));
    window.addEventListener("hashchange", on);
    return () => window.removeEventListener("hashchange", on);
  }, []);
  return route;
}

export function href(path: string[], query?: Record<string, string>): string {
  const q = query ? new URLSearchParams(query).toString() : "";
  return "#/" + path.map(encodeURIComponent).join("/") + (q ? "?" + q : "");
}
