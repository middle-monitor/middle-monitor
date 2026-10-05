# Frontend

React + Vite application: the dashboard, the public site and the documentation
(`/docs`), plus a read-only demo (`/demo`) that runs entirely in the browser
against mock data in `src/demo/`.

```bash
npm ci
npm run dev     # http://localhost:3000, proxies /api to http://localhost:8080
npm run lint
npm test        # Vitest unit tests
npm run e2e     # Playwright, runs against the demo, no backend needed
npm run build   # type-check, bundle, prerender public pages, generate llms.txt
```

`npm run build` also writes `dist/llms.txt`, `dist/llms-full.txt` and
`dist/docs/*.md`, parsed out of `src/views/DocumentationView.tsx`; keep its JSX
structure when editing the docs.

Build-time settings:

| Variable | Effect |
|----------|--------|
| `VITE_MIDDLE_MONITOR_TOKEN` | Service token the dashboard reports its own errors with (optional). |
| `VITE_PLAUSIBLE_SRC`, `VITE_PLAUSIBLE_DOMAIN` | Load a Plausible analytics script. Unset: no tracker. |

At runtime the nginx image reads `ANALYTICS_ORIGIN` to allow that analytics
host in its Content-Security-Policy.
