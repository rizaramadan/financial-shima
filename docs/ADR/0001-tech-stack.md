# ADR 0001: Initial Technology Stack

## Status

Accepted — 2026-04-30

## Context

This is a family financial manager: a server-rendered web app for tracking household money. Scale is small (a single family, low concurrent users), but correctness is non-negotiable (it's their money records) and the project will be touched occasionally over years rather than developed full-time.

Constraints driving the decision:

- **Low infrastructure cost** — must run cheaply when idle, scale-to-zero preferred.
- **Solo developer** — minimal cognitive overhead, conventional tools, no team-coordination tax.
- **Server-rendered** — no SPA build step, no JavaScript framework churn.
- **Long lifespan** — favor "boring tech" with stable APIs over the cutting edge.
- **Correctness invariants** (see CLAUDE.md §7) — money math, determinism, append-only ledger, idempotency.

## Decision

| Layer | Choice |
|---|---|
| Database | PostgreSQL via [Neon](https://neon.tech) (serverless, scale-to-zero) |
| Language | Go |
| Web framework | [Echo](https://echo.labstack.com/) |
| SQL access | [sqlc](https://sqlc.dev/) — type-safe Go from raw SQL |
| Server-side templates | Go `html/template` |
| Client interactivity | [HTMX](https://htmx.org/) for server-driven updates |
| Client-side reactivity | [Alpine.js](https://alpinejs.dev/) for trivial UI state |

No SPA framework, no build step for assets beyond a CDN/static include, no ORM.

## Alternatives Considered

- **SQLite** — simpler (single file, no service). Rejected: preference for Postgres, and Neon's free tier eliminates the "service to run" cost anyway.
- **stdlib `net/http`** (Go 1.22+ pattern matching) — smaller dependency surface. Rejected: Echo's middleware ecosystem (logging, recovery, CORS, rate limiting, validation) is worth the dependency for solo-dev velocity.
- **Phoenix LiveView (Elixir)** — ideologically purer match for HTMX-style reactivity, drops HTMX and Alpine. Rejected: heavier runtime than a Go binary, smaller ecosystem, new language to maintain over years.
- **Rails + Hotwire (Ruby)** — most productive for CRUD. Rejected: heavier deploy than a Go binary, higher idle cost.
- **Blazor Server (.NET)** — single language end-to-end, ASP.NET Identity for free auth. Rejected: ~50–100MB runtime vs Go's ~15MB static binary; hosting cost on tiny tiers is meaningfully higher.

The HTMX/Alpine layer is also worth a deliberate note: HTMX alone covers most server-driven interactivity, but Alpine handles trivial UI state (dropdowns, modals, form toggles) without a server round-trip. Both libraries are <20KB, no build step, no version churn.

## Consequences

**Easier:**

- Deployment is a single static binary (~15MB) plus a connection string. Fits any tiny VPS or container host.
- Neon's free tier and scale-to-zero keep idle infra cost effectively at $0.
- sqlc gives compile-time SQL safety without ORM coupling — schema lives in `.sql` files, queries are reviewed as SQL, generated Go code is mechanical.
- The pure Logic layer (per `principles/code-structure.md`) is naturally deterministic in Go — no implicit `time.Now()` or globals — which directly enables the correctness invariants in CLAUDE.md §7.
- HTMX + Alpine = no JS toolchain to maintain. No webpack, no Vite, no node_modules.

**Harder:**

- More CRUD boilerplate than Rails/Phoenix/Blazor. Forms, validation, auth, sessions are all hand-rolled (Echo middleware helps, but no convention-over-configuration).
- Auth specifically: no ASP.NET-Identity-equivalent. We will pick a small library or build session-cookie auth ourselves — to be revisited in a follow-up ADR.
- HTMX implies more server round-trips than a SPA for interactivity. Acceptable at family scale; would be a problem at higher concurrency.
- Neon's scale-to-zero means a cold-start latency penalty (~hundreds of ms) on the first request after idle. Acceptable for a family app; would matter for user-facing low-latency systems.
- Two frontend libraries (HTMX + Alpine) instead of one. Both are small, but it's a real surface area to learn and reason about.
- We accept Go's verbosity over Elixir/Ruby/C# productivity gains — explicitly chosen for runtime cost and deploy simplicity.
