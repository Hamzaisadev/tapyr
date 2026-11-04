import type { ApiError, List } from "./types";

// The status API is served by the same origin as the UI.
export const apiBase = "/api/v1";

export class HttpError extends Error {
  readonly status: number;
  readonly code: string;
  constructor(status: number, code: string, message: string) {
    super(message);
    this.status = status;
    this.code = code;
  }
}

export async function fetchJSON<T>(path: string, signal?: AbortSignal): Promise<T> {
  const resp = await fetch(apiBase + path, { signal, headers: { Accept: "application/json" } });
  if (!resp.ok) {
    let code = "http_error";
    let message = `${resp.status} ${resp.statusText}`;
    try {
      const body = (await resp.json()) as ApiError;
      code = body.error.code;
      message = body.error.message;
    } catch {
      // not a JSON error body
    }
    throw new HttpError(resp.status, code, message);
  }
  return (await resp.json()) as T;
}

// fetchAll follows the pagination of a list endpoint.
export async function fetchAll<T>(path: string, signal?: AbortSignal): Promise<T[]> {
  const items: T[] = [];
  let cursor = "";
  for (let page = 0; page < 50; page++) {
    const sep = path.includes("?") ? "&" : "?";
    const url = `${path}${sep}limit=1000${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ""}`;
    const list = await fetchJSON<List<T>>(url, signal);
    items.push(...list.items);
    if (!list.next_cursor) break;
    cursor = list.next_cursor;
  }
  return items;
}
