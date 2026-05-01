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

#### Round 2 — 2026-05-01

| Persona | Score | Headline |
|---|---|---|
| Skeet | 8/10 (↑ 1.5) | Shutdown path: `log.Fatalf` in goroutine skips defers + bypasses Shutdown — use channel pattern; `Shutdown` failure should `Printf` not `Fatalf`; no smoke test that server actually binds (`httptest` recorder bypasses real `net/http.Server`); timeout test is tautological; `WriteTimeout 10s` is a Phase-2 Telegram-call footgun without a TODO marker; ADDR unvalidated; empty CSRF placeholder a contract trap; SIGTERM no-op on Windows. |
| Ive | 6/10 (↑ 3) | Button "Continue" too generic; no submission/loading state; `role="alert"` + `aria-live="polite"` conflict (drop `aria-live`); `aria-describedby` should not permanently include empty form-error; hint below input but label says "or" — move hint above; long-input overflow on error; sub-320px viewport too tight; dark-mode disabled button breaks contrast; `opacity:0.92` hover is generic; wordmark + h1 redundant — combine "Sign in to Shima"; subhead front-loads system; no no-JS guarantee; focus blue is only color; no "Don't use Telegram?" escape; no honeypot; tracking inconsistent. |
| Beck | 7.5/10 (↑ 1.5) | CSRF and error-region tests are placeholders (no behavior consumes them — TDD inversion: stub drives test instead of behavior driving stub); label text content untested (empty label would pass); no uniqueness assertions (multiple identifier inputs would pass); `FormPostsToLoginEndpoint` asserts two things; `renderLogin` bypasses middleware — at least one canary should go through `e.ServeHTTP`; round-2 bundling commit hides per-reviewer attribution. (Note: server-test bodies were elided in the prompt — that was the prompt's fault, not the code's; full bodies will be pasted in Round 3.) |

**Changes for Round 3:**

Beck (drives commit 1):
- Delete `TestLoginGet_HasCSRFTokenPlaceholder` + remove the hidden CSRF input (let Phase 2's "POST without valid token → 403" drive its return).
- Delete `TestLoginGet_HasErrorRegionForLiveAlerts` + remove the `role="alert"` element (let Phase 2's error-flow tests drive it).
- Add `TestLoginGet_LabelHasVisibleText` walking the label's children.
- Add `findAll` helper; assert exactly one `<form>`, one identifier input, one submit button.
- Split `TestLoginGet_FormPostsToLoginEndpoint` into `_FormUsesPOSTMethod` and `_FormPostsToLoginPath`.
- Make `renderLogin` go through `e.ServeHTTP` after registering the route, so handler tests exercise the framework dispatch path.
- Assert `<html lang="en">` and non-empty `<title>`.

Ive (drives commit 2):
- Drop wordmark; rename h1 to "Sign in to Shima"; subhead → "Sign in with a 6-digit code from Telegram." (user-as-subject).
- Drop `<form novalidate>` so empty-submit blocked natively.
- Move hint between `<label>` and `<input>`.
- `overflow-wrap: anywhere` on `.error-region` (it'll come back in Phase 2; the CSS rule stays even when the DOM is removed — keep it commented or move into Phase 2).
- `@media (max-width: 360px) { body { padding: 1rem; } }`.
- Disabled button: separate token, not opacity, to keep AA contrast in dark mode.
- Hover: `color-mix(in oklab, var(--accent) 92%, var(--fg))` instead of opacity.
- Button label → "Send code via Telegram" (carries destination so subhead can tighten).
- Add honeypot `<input name="website" tabindex="-1" autocomplete="off">` off-screen.
- Tracking tokens `--tracking-tight/normal/wide`.

Skeet (drives commit 3):
- Channel-based shutdown: server-error chan, `select` on `ctx.Done` vs `serverErr`, then `e.Shutdown` unconditionally; downgrade `Shutdown` failure to `Printf`.
- Smoke test via `httptest.NewServer` that binds and serves real HTTP.
- Replace tautological timeout assertions with exact-value comparisons.
- `net.ResolveTCPAddr` to validate `ADDR` eagerly.
- `WriteTimeout` TODO comment marking Phase-2 review.
- Windows SIGTERM no-op comment.
- Drop `e.HidePort` OR drop the manual `listening on …` log; one source of truth.
- Rename POST 405 test to describe Echo routing rather than implying we wrote the 405 logic.

Conflict resolution (per spec: safety>simplicity, clarity>performance, convention>magic): Beck wins on the CSRF/error-region tests (delete them), even though Ive's "form shape stability" argument is real — clarity wins, and Phase 2 will drive their return naturally. Ive's `aria-live` removal aligns with Beck's deletion of the test, so no cross-cutting conflict remains there.

#### Round 3 — 2026-05-01

| Persona | Score | Headline |
|---|---|---|
| Skeet | 8.5/10 (↑ 0.5) | **Critical**: bind-failure path `return`s with exit 0 — supervisors won't restart. `net.ResolveTCPAddr` does DNS (semantic), should be `SplitHostPort` (syntactic). `BindsAndServesRealHTTP` exercises Echo over TCP but not `e.Start`'s bind path. `TimeoutsMatchDeclaredConstants` is tautology (asserts X==X). |
| Ive | 7.5/10 (↑ 1.5) | h1 + subhead semantic overlap; h1 undertuned at 24px/-1%; `max-width 22rem` cramped; field→button rhythm too tight; button + input visually identical shapes; `autocomplete="username"` is **a correctness bug** (browsers surface saved usernames/emails, not Telegram); no `accent-color` for native chrome; no visual anchor; subhead at 15px on `--muted` borderline. |
| Beck | 8.5/10 (↑ 1) | `_HasNoAutofocus_PerMobileUXReview` pins a removal decision, not a behavior — delete. `TimeoutsMatchDeclaredConstants` tautology — delete or use literals. `PostLogin_RejectedByEchoMethodRouting` tests Echo, not us — delete. Mobile-attrs bundle hides multi-property test — split. Button text `!= ""` is degenerate — pin actual word. `renderLogin` uses bare Echo while `newServer` adds middleware — middleware not exercised by handler tests. Untested: charset, label uniqueness, body content on TCP test. |

**Changes for Round 4** (separated by reviewer per Round 2's lesson):

Beck (commit 1):
- Extract `web/setup` package: `Apply(*echo.Echo)` configures timeouts + middleware. Both `cmd/server` and the handler test's `renderLogin` call it, so handler tests exercise the assembled stack.
- Delete `TestLoginGet_HasNoAutofocus_PerMobileUXReview` (pins absence; not behavior).
- Delete `TestServer_PostLogin_RejectedByEchoMethodRouting` (asserts framework behavior).
- Delete `TestServer_TimeoutsMatchDeclaredConstants` (tautology); the real-listener test is the timeout regression guard now.
- Split `_IdentifierInputUsesMobileFriendlyAttributes` into `_AutocompleteIsOff`, `_DisablesKeyboardCorrections` (autocapitalize/autocorrect/spellcheck), `_IsRequired`.
- Pin button label exactly: assert text is `"Send code via Telegram"`.
- Add `_HasUTF8CharsetDeclaration` and `_HasExactlyOneLabel` (uniqueness).
- `BindsAndServesRealHTTP` now also asserts the response body contains `<form` and `name="identifier"`.

Ive (commit 2):
- `autocomplete="username"` → `autocomplete="off"` (browsers were going to surface saved emails, not Telegram handles).
- h1 punch: `font-size: 1.875rem; font-weight: 650; letter-spacing: -0.02em` — present, not default.
- Drop subhead entirely; h1 carries the orienting voice now that it's specific ("Sign in to Shima").
- `max-width: 22rem → 24rem` so 32-char usernames don't crowd the input.
- `.field { margin-bottom: 1.5rem }` so the button reads as a consequence, not a sibling.
- Button taller than input: `padding: 0.875rem 1rem` — the action is shaped differently from the question.
- `accent-color: var(--focus)` on `:root` and a `::selection` style so native form chrome stays in palette.

Skeet (commit 3):
- Bind failure now exits non-zero via `log.Fatalf` (preserves the supervisor restart contract that Round 2's `Printf` accidentally broke).
- Replace `net.ResolveTCPAddr` with `net.SplitHostPort` (pure syntax, no DNS).
- Defer-close in goroutine for invariant clarity.
- Rename `BindsAndServesRealHTTP` → `HandlerOverRealTCP_ServesLoginForm` (honest about what it tests; the body-content assertion lands here too).

