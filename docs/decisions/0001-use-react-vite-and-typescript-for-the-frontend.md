# 1. Use React, Vite and TypeScript for the frontend

Date: 2026-10-28

## Status

Accepted

## Context

The panel is a small authenticated web app for one server, used by a few people. All seven screens (01–07, plus the mobile dashboard) show live data: status, metrics, logs and job progress. There are no public pages and no SEO needs. The screens in `knowledge/design/` are written as React components with inline styles and Lucide-style icons, with no UI library. The owner's top constraint is that the panel stays lightweight on the server it manages. Decided in [#1](https://github.com/daniel-vuky/server-control-panel/issues/1).

### Option A: React SPA built with Vite

- Pro: the design is already React, so screens translate directly.
- Pro: it builds to static files that the backend can serve. No Node process runs in production.
- Con: no server rendering, which nothing here needs.

### Option B: Next.js

- Pro: the owner knows it well, and it has file routing.
- Con: `next start` uses an estimated 150–250 MB RAM at idle, against near zero for static files.
- Con: SSR and SEO, its main strengths, go unused because every page is live data behind auth.
- Con: long-running background work (sampling, schedules) has to be wedged into the framework.

### Option C: SvelteKit

- Con: the design would have to be translated out of React.

## Decision

The frontend is a **React single-page app built with Vite, in TypeScript**.

1. Styling uses Tailwind CSS. The design's colours, fonts and radii are defined as theme tokens (CSS variables).
2. Fonts (IBM Plex Sans, JetBrains Mono) are self-hosted from `@fontsource` packages, with no runtime request to Google Fonts.
3. Icons come from `lucide-react`. Accessible primitives (dialog, menu, toast) come from Radix UI, and time-series charts use uPlot. Each is added by the first story that needs it.
4. Tests use Vitest and Testing Library. Lint and format use Biome.
5. The build output is embedded into the backend binary (see [ADR 2](0002-use-go-for-the-backend-as-a-single-binary.md)).

## Consequences

**Easier:**

- Screens can be built straight from the design files.
- Production runs no frontend server: it is static files served by the backend.

**Harder:**

- API types must be shared with a Go backend through code generation rather than a common language (see ADR 2).
