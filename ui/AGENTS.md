# AGENTS.md - web UI

Rules for the web UI in `ui/`, on top of the [root `AGENTS.md`](../AGENTS.md). The specification is the web UI part of [`09-cli-api-ui.md`](../docs/spec/09-cli-api-ui.md) and [ADR 0006](../docs/adr/0006-read-only-embedded-ui.md).

## What it is

A React 19 + TypeScript app built by Vite into `ui/dist/app`, embedded in the binary by [`embed.go`](embed.go) and served under `/ui/` by `internal/ui`. It reads the status API (`/api/v1`) and its event stream; nothing else.

```
ui/src/
├── App.tsx            # layout, navigation, routes (hash router: #/jobs/name)
├── pages/             # one component per page, with its tests (*.test.ts)
├── components/        # shared components (ui.tsx: badges, meters, tables, select; Code, JsonTree...)
├── lib/               # api.ts (fetch), live.tsx (event stream and polling), format.ts, router.ts, types.ts
└── theme.css          # the tokens and styles of both themes
```

## Rules

- **Read-only.** The UI never changes configuration or state, and the API it reads has no write endpoint. A feature that needs to write does not belong here.
- **No external network call.** The page is served with `Content-Security-Policy: default-src 'self'`: fonts, icons and images are bundled, there is no CDN, no analytics, no remote font. `TestNoExternalReferences` (`internal/ui`) fails on any URL in the built bundle.
- **Few dependencies.** Runtime dependencies are React, `@xyflow/react` (the graph) and the bundled fonts. Charts are hand-written SVG. A new dependency needs a reason in the pull request.
- **Types follow the API.** `lib/types.ts` mirrors the status API (`internal/api`, `openapi.yaml`); change both together.
- **Both themes.** Colors come from the tokens of `theme.css` (light and dark, following the system, with a switch). Check every change in both.
- **Never color alone.** Every state carries a word, an icon or a shape next to its color (accessibility, spec 09).
- **English** for every label; the look follows [tapyr.eu](https://tapyr.eu).
- **Live updates** go through `lib/live.tsx` (refetch on events, polling when the stream is down); pages do not open their own `EventSource`.
- **React hooks lint rules** (purity, no state set synchronously in effects) are errors, not warnings: derive values instead of syncing state.

## Checks

```sh
make ui-check      # tsc, ESLint, Vitest
make ui            # build into ui/dist/app (embedded by the next go build)
make ui-dev        # hot reload against a running tapyr run (TAPYR_URL, default http://127.0.0.1:9790)
```

Layout, formatting and component logic gets Vitest tests (`pages/*.test.ts`, `lib/*.test.ts`, `components/*.test.tsx`). For a visual change, run the UI against the simulators (`tapyr sim --all` and a configuration pointing to them, or `examples/compose`) and look at it in both themes, with a few jobs and with many. `ui/dist/app/` is never committed (only `ui/dist/README.md`, the placeholder `go:embed` needs).
