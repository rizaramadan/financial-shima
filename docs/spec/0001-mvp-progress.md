# Spec 0001 — MVP Progress

Tracks delivery against `0001-mvp.md`. Phases are minimal end-to-end slices, not horizontal layers. Each phase ships testable behavior.

## Phase Map

| # | Phase | Status | Exit Criteria |
|---|-------|--------|---------------|
| 1 | Project scaffold + login page renders | in_progress | `GET /login` returns 200 with the form; handler unit-tested; `go test ./...` green; review loop passes |
| 2 | OTP issue + verify (in-memory store, stubbed assistant) | pending | Submit identifier → OTP generated; submit OTP → session cookie; rate limit + lockout enforced; reviewers pass |
| 3 | Logic layer: money type (integer cents) + Clock/IDGen interfaces | pending | Property tests for arithmetic; no `float64` anywhere in `logic/`; reviewers pass |
| 4 | DB schema + sqlc setup (Postgres, Neon-compatible) | pending | Migrations run; `accounts`, `pos`, `counterparties`, `users`, `sessions` tables; sqlc generates; reviewers pass |
| 5 | Logic: transaction validation (§5.1 rules for all 3 types) | pending | All §5.1 rules unit-tested; reviewers pass |
| 6 | Append-only insert path + idempotency + notification atomicity (§10.3, §10.4, §10.8) | pending | Property tests assert invariants; fault-injection on notification write; reviewers pass |
| 7 | Pos balance computation (§4.2: cash, receivables, payables) | pending | Property tests for §10.5, §10.6; reviewers pass |
| 8 | Borrow obligation + repayment matching (§4.3 borrow mode, §10.7) | pending | FIFO matching tested incl. cross-currency; reviewers pass |
| 9 | Web UI: views (§6.1–6.5) | pending | All five views render with seeded data; HTMX bell badge polls; reviewers pass |
| 10 | LLM JSON API (§7.2) + initial seed flow (§9) | pending | Endpoints accept `x-api-key`; idempotency dedupes; reviewers pass |

## Round Log

(Each phase's review rounds appended below as they happen. Format: round number, scores per persona, what changed, blockers.)

### Phase 1

#### Round 1 — 2026-05-01

| Persona | Score | Headline issues |
|---|---|---|
| Skeet (code) | 6.5/10 | No graceful shutdown / timeouts / env addr; `log.Fatal` treats `ErrServerClosed` as crash; missing security headers; `Content-Type` not asserted; server test duplicates handler assertions; `go.mod` lists direct deps as `// indirect`; `dump_login.go` doesn't check status. |
| Ive (UX/design) | 3/10 | No `<meta viewport>`; no CSS at all; default Times New Roman; no product wordmark / orientation copy; no error region; `autofocus` hostile on mobile; input lacks `inputmode`/`autocapitalize`/`autocorrect`; "Send code" ambiguous; no CSRF placeholder. |
| Beck (TDD) | 6/10 | Content-Type / `<label>` / submit / `required` untested though deliberately added (retrofit smell); server test duplicates body assertion; no POST `/login` 405 boundary test; substring assertions are positional, not structural. |

**Changes to make in Round 2 (driven by failing tests where possible):**

- Server: graceful shutdown via `signal.NotifyContext`; `ReadHeaderTimeout`/`ReadTimeout`/`WriteTimeout`/`IdleTimeout`; env `ADDR` with `:8080` default; `middleware.Secure()`; `log.Fatal` ignores `http.ErrServerClosed`.
- Page: viewport meta; inline CSS with system font stack, centered card, vertical rhythm; "Shima" wordmark + Telegram subhead; hint copy with `aria-describedby`; empty `role=alert aria-live=polite` error slot; CSRF placeholder hidden input; remove `autofocus`; add `inputmode=text`, `autocapitalize=off`, `autocorrect=off`, `spellcheck=false`; rename button to "Continue".
- Tests: split focused tests; structural parse via `golang.org/x/net/html`; assert `Content-Type`; assert `<label for="identifier">`; assert submit button; pin Phase 1 boundary with `POST /login → 405`; assert security headers; `t.Parallel()`.
- Hygiene: `go mod tidy`; `dump_login.go` checks status; updated handler doc-comment to describe HTTP contract.

