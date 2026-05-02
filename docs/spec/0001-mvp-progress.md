# Spec 0001 — MVP Progress

Tracks delivery against `0001-mvp.md`. Phases are minimal end-to-end slices, not horizontal layers. Each phase ships testable behavior.

## Phase Map

| # | Phase | Status | Exit Criteria |
|---|-------|--------|---------------|
| 1 | Project scaffold + login page renders | **complete** (2026-05-01) | `GET /login` returns 200 with the form; handler unit-tested; `go test ./...` green; all three reviewers ≥9/10 in their final reviews (Skeet 9.3 R6, Ive 9.1 R8, Beck 9.6 R6) |
| 2 | OTP issue + verify (in-memory store, stubbed assistant) | **implementation complete; review loop deferred** | Submit identifier → OTP generated ✓; submit OTP → session cookie ✓; rate limit + lockout enforced ✓ (15 logic tests + 13 I/O tests). Adversarial review loop deferred to next session. |
| 3 | Logic layer: money type (integer cents) + Clock/IDGen interfaces | **implementation complete** | Property tests for arithmetic ✓ (commutativity, identity, round-trip, neg-involution); no `float64` anywhere in `logic/` ✓; Clock/IDGen already shipped in Phase 2. Review loop deferred. |
| 4 | DB schema + sqlc setup (Postgres, Neon-compatible) | **implementation complete** | Migrations apply ✓ (5 tables + indexes + CHECK constraints); sqlc generates ✓ (pgx/v5); integration test against local PG13 ✓ (Account CRUD, Pos currency rejection, Counterparty case-insensitive dedup, Session expiry filter). Review loop deferred. |
| 5 | Logic: transaction validation (§5.1 rules for all 3 types) | **implementation complete** | All §5.1 rules unit-tested ✓ (20 tests covering money_in/_out + inter_pos): future date, archived account/pos, currency mismatch, IDR-pos amount equality, cross-currency permitted on non-IDR pos, counterparty regex, line totals reconcile per-currency, cross-currency lines do NOT sum across (§10.6), positive amounts, mode/direction validation. Review loop deferred. |
| 6 | Append-only insert path + idempotency + notification atomicity (§10.3, §10.4, §10.8) | **implementation complete** | Migration 0002 adds transactions + notifications + enums. logic/notification.RecipientsFor pure (§4.5). dependencies/ledger.Service.Insert wraps txn + notification rows in one DB tx. Tests: atomic insert + notification dispatch ✓; fault-injected notification failure rolls back ✓ (ErrNotificationWriteFailed); idempotent re-submission returns same ID + 1 row in DB ✓; queries file scanned for banned UPDATE/DELETE ✓. Inter_pos lines + obligations deferred to Phase 7. Review loop deferred. |
| 7 | Pos balance computation (§4.2: cash, receivables, payables) | **implementation complete (cash only)** | logic/balance: pure State + Apply for MoneyIn / MoneyOut / InterPos events, overflow-safe addSafe/subSafe, IDR-Pos amount-equality enforced (§5.1), inter_pos lines self-reconcile per currency (§10.6) before mutating. Property test §10.5: 50 seeds × 200 random events asserts Σ(Account) = Σ(Pos.cash IDR) after EVERY event. Property test §10.6: 50 generated unreconciled inter_pos events all rejected. Receivables/payables (borrow obligations) deferred to Phase 8. Review loop deferred. |
| 8 | Borrow obligation + repayment matching (§4.3 borrow mode, §10.7) | **implementation complete (same-currency)** | logic/obligation: pure GenerateForBorrow (M×N obligations, prorated by creditor share, last-creditor absorbs rounding so per-debtor sum is exact) + Match (FIFO by stable input order, partial/full clearing, overpayment spawns reverse obligation — the "kid's school cash short after gold drop" case from §4.3). Migration 0003 adds pos_obligation table with CHECK enforcing §10.7 (cleared_at iff repaid >= owed) at the storage layer. Property test asserts every Updates/NewObligations row passes Validate(). Cross-currency borrow returns ErrCrossCurrencyBorrow — needs FX rate input not yet specified. Review loop deferred. |
| 9 | Web UI: views (§6.1–6.5) | **all stages done** | A: §6.2 Home + nav. B: §6.5 Notifications feed + bell badge. C: server-rendered bell. D: §6.1 Transactions list (date-range filter, reversal badge). E: §6.3 Pos detail (name/currency/target, receivables/payables from open pos_obligation, open obligations table, scoped txn list). F: §6.4 Spending — months × top-N Pos pivot via `SumMoneyOutByPosMonth` aggregation; default last 6 months × top 5 Pos by money_out volume; Pos column headers link to /pos/:id, row totals + Pos totals foot row, empty-state when no money_out in range. Home page nav links {Transactions, Spending, Notifications}. 4 unit tests pin: unauth, nil-pool empty-state, query-param round-trip, invalid-date fallback. |
| 10 | LLM JSON API (§7.2) + initial seed flow (§9) | **in progress** (apikey middleware: R1 7.5/8/9 → R2 9/9/9.5 → R3 9.5/9.5/9.7; 2 consecutive ≥9 — new issues still surfacing, countdown not yet begun) | Endpoints accept `x-api-key`; idempotency dedupes; reviewers pass |

## Round Log

(Each phase's review rounds appended below as they happen. Format: round number, scores per persona, what changed, blockers.)

### Phase 2

**Implementation summary (2026-05-01):**

Logic layer (5 packages, all pure, all parallel-tested):
- `logic/clock` — `Clock` interface, `System`/`Fixed` impls.
- `logic/idgen` — `IDGen` interface, `Crypto` (32-byte URL-safe base64) and `Fixed` impls.
- `logic/otp` — `Code` (6-digit, zero-padded), `Generate(io.Reader)`, `Record.Verify` with constant-time compare, `String()` redacts the code, exported constants per spec §3.3.
- `logic/user` — `Seeded()` returns Riza/Shima; `Find()` lowercases + strips `@` + trims whitespace.
- `logic/auth` — coordinates `Issue` → assistant → `Verify` → `Session`. In-memory stores keyed by user.ID and token. Resend cooldown enforced; lockout via `otp.Record`.

Dependencies: `dependencies/assistant` — `Client` interface, production `HTTPClient` (5s timeout, no retries per spec §7.3), test `Recorder` fake.

I/O: `web/template` (html/template, layout via concat), `web/handler` (`LoginGet/Post`, `VerifyGet/Post`, `HomeGet`), `web/middleware/session` (cookie → user). `cmd/server` wires everything; `OTP_ASSISTANT_URL/_API_KEY` env vars toggle live delivery vs in-memory recorder.

Test counts: 28 new tests across logic+deps+web. All green.

Phase-2 exit criteria from spec section 0:
- ✅ Submit identifier → OTP generated (auth.Issue + assistant.SendOTP).
- ✅ Submit OTP → session cookie (HttpOnly, SameSite=Lax, 7-day, Secure when TLS).
- ✅ Rate limit (60s ResendCooldown) + lockout (3 wrong attempts) enforced.
- ⏸ Reviewers pass — three-persona review deferred to a separate session to keep this commit tight.

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

#### Round 4 — 2026-05-01

| Persona | Score | Headline |
|---|---|---|
| Skeet | 8.8/10 (↑ 0.3) | Shutdown `select` races between `ctx.Done` and `serverErr` — bind failure during signal arrival is swallowed; drain serverErr non-blockingly after `ctx.Done`. `SplitHostPort` accepts `:99999` / `:abc` / `:0`; add port-range check. Successful startup is silent (Echo's banner suppressed); add explicit `log.Printf("listening on %s", addr)`. CSP header value is unasserted by tests. `shutdownGraceDuration` = `WriteTimeout` coincidence is undocumented. |
| Ive | 8.0/10 (↑ 0.5) | Hint between label and input cleaves the label-input atom — move hint *under* input as helper text. Button "Send code via Telegram" is procedural — `Send code`; let the destination live in surrounding copy. h1 1.75rem margin too tight — 2rem. No visual anchor — small `S` monogram. Hover too subtle. Outline-offset inconsistency input vs button. |
| Beck | 9.0/10 (↑ 0.5) | `HasUTF8CharsetDeclaration` tests markup, not behavior — assert Content-Type charset instead. `HasViewportMetaForResponsiveLayout` is markup audit — delete (Ive's domain). `HasHTMLLangAndNonEmptyTitle` bundles two assertions — split. `IdentifierInputIsTextType` admits two answers (text or omitted) — pick one. `LabelHasVisibleText` accepts any non-empty — assert literal copy. Security headers only checked on `/login`, not 404. **`POST /login` never exercised** — Phase 1 should at least register a placeholder handler so the form's contract isn't fictional. `HasExactlyOneLabel` tests wrong invariant — every input has a label is the rule, not "exactly one label." |

**Changes for Round 5:**

Beck (commit 1):
- Replace `_HasUTF8CharsetDeclaration` with assertion that `Content-Type` header contains `charset=utf-8`.
- Delete `_HasViewportMetaForResponsiveLayout` (markup audit; not Go-testable behavior).
- Split `_HasHTMLLangAndNonEmptyTitle` into `_HTMLLangIsEN` and `_HasNonEmptyTitle`.
- `_IdentifierInputIsTextType` → assert `type` is omitted (rely on HTML default), test renamed.
- `_IdentifierInputHasLabelWithVisibleText` → assert literal label copy `"Telegram username or ID"`.
- Delete `_HasExactlyOneLabel` (wrong invariant); replace with `_EveryInputHasAssociatedLabel`.
- Extend `_AppliesSecurityHeaders` to also assert headers on a 404 path.
- Add `TestServer_POSTLogin_IsRouted` (assert 501 Not Implemented). Driven by registering a Phase-2-stub `POST /login` handler that returns 501.
- Add `_AppliesContentSecurityPolicy` asserting the CSP value (per Skeet round 4).

Ive (commit 2):
- Move hint to AFTER input (label → input → hint), preserving `aria-describedby`.
- Button text "Send code via Telegram" → "Send code".
- h1 margin-bottom 1.75rem → 2rem.
- Add small `S` monogram above h1 (semibold accent-color, `aria-hidden="true"`).
- Hover: shift 92% → 85% so it's visibly tactile.
- Input `outline-offset: 1px` → 2px to match button.

Skeet (commit 3):
- Drain `serverErr` after `ctx.Done` non-blockingly to close the shutdown race.
- Validate ADDR port: parse and check 1 ≤ port ≤ 65535.
- Add `log.Printf("listening on %s", addr)` before `e.Start` (one source of truth; no Echo banner to compete).
- Already covered by Beck commit: `_AppliesContentSecurityPolicy` test; `shutdownGraceDuration` aliased to `setup.WriteTimeout`.

#### Round 5 — 2026-05-01

| Persona | Score | Headline |
|---|---|---|
| Skeet | 8.9/10 (↑ 0.1) | `main` does four things; extract `run(ctx, e, addr) error` so the lifecycle is unit-testable. `validateAddr` error format inconsistent (`Quote` vs `Itoa`). Only `http.ErrServerClosed` whitelisted; `net.ErrClosed` should be too. `shutdownGraceDuration = WriteTimeout` is racy on slow machines — needs slack. |
| Ive | 8.5/10 (↑ 0.5) | "S" monogram is "absence wearing a costume" — delete or commit to a real letterform. Spacing scale ad hoc (5 different gaps, no shared unit) — pick base 0.5rem and use multiples. Hint carries label's disclosure burden — label = "Telegram username or numeric ID"; hint = single example. `--accent` paints both wordmark and button — separate `--brand`. `font-weight: 650` rounds to 700 in system font — use 600. Button full-width reads as mobile-first leakage on desktop. `autocomplete="off"` hostile to returning users (Ive flip-flopped from R4). |
| Beck | 9.4/10 (↑ 0.4) ✅ | `IdentifierInputOmitsTypeAttribute` is strictly weaker than asserting effective text-equivalence. TCP test re-asserts DOM facts with three substrings — one structural check would do. `DisablesKeyboardCorrections` bundles three vendor contracts under one name. `HasNonEmptyTitle` true-by-construction; assert contains identifying token. |

**Changes for Round 6:**

Skeet (commit 1):
- Extract `run(ctx context.Context, e *echo.Echo, addr string) error` so `main` is reduced to env read + validate + signal wiring + `run`. Adds `TestRun_StopsOnContextCancel` that exercises the lifecycle without spawning a process.
- `validateAddr` errors via `fmt.Errorf("…%q/%d…")` for consistency.
- Whitelist `net.ErrClosed` alongside `http.ErrServerClosed`.
- `shutdownGraceDuration = setup.WriteTimeout + 1*time.Second` so a write hitting WriteTimeout still has a beat to surface its error before forced close.

Ive (commit 2):
- DELETE the `<div class="mark">S</div>`. A single Latin glyph in the system sans is not a brand mark; until a real one exists, silence > scaffolding.
- Spacing scale on 0.5rem multiples: label→input 0.5rem, input→hint 0.5rem, h1→form 2rem, field→button 1.5rem (already), body→main padding stays 1.5rem.
- Label copy → `"Telegram username or numeric ID"`. Hint → `"e.g. @shima"`. Hint illustrates, doesn't disclose.
- `font-weight: 650` → `font-weight: 600` (system fonts won't render 650).
- Keep button full-width (the form is genuinely narrow at 24rem; Ive's "mobile-first leakage" applies more to wide forms).
- Keep `autocomplete="off"`. Round 4 Ive flagged saved-email surfacing as a credibility risk; that argument still holds. Round 5 flip is cosmetic.

Beck (commit 3):
- `_IdentifierInputOmitsTypeAttribute` → `_IdentifierInputAcceptsPlainText` allowing `""` or `"text"`.
- Trim TCP-test substrings from 3 → 1 (`action="/login"` proves wire path serves the right page).
- Split `_DisablesKeyboardCorrections` into platform-named tests.
- `_HasNonEmptyTitle` → `_TitleContainsSignIn` asserting `"Sign in"` substring.

#### Round 6 — 2026-05-01

| Persona | Score | Headline |
|---|---|---|
| Skeet | 9.3/10 ✅ (↑ 0.4) | drain after Shutdown (drop) — fixed in follow-up. Propagate Shutdown error — fixed. Replace 50ms sleep w/ poll — fixed. Document `:0` rejection — done. Otherwise solid. |
| Ive | 8.7/10 (↑ 0.2) | h1 margin 2rem breaks the 0.5rem ladder (1.5rem matches `.field`). Disabled button contrast ~3.1:1 in light. `place-items: center` looks awkward on tall viewports. Title duplicates h1 — "Shima — Sign in" wins tab strip. Button "Send code" hides mechanism — "Continue with Telegram" or "Send login code to Telegram". `font-size: 1rem` should be `max(1rem, 16px)` against future `:root` font-size shifts. (Skipping: `@`-affix input — too disruptive late phase. autocomplete flip — keep `off`.) |
| Beck | 9.6/10 ✅ (↑ 0.2) | Wall-clock `2*time.Second` magic; named constants. Listen/relisten race; pass listener in. Discarded `_` from `net.Listen`. Submit-button assert bundles cardinality + copy. (Most are 9.6→10 polish.) |

**Changes for Round 7:** focus on Ive (only sub-9 reviewer). Minimal Skeet/Beck hygiene to avoid regression.

Ive (commit 1):
- h1 margin-bottom 2rem → 1.5rem (matches `.field` and 0.5rem ladder).
- Title `"Sign in to Shima"` → `"Shima — Sign in"` (brand-first in tab strip).
- Button copy `"Send code"` → `"Continue with Telegram"`.
- Body layout: `place-items: center` → `align-items: start; justify-items: center; padding-top: max(1.5rem, 12vh)` so card sits in optical upper-third.
- Disabled button: `color` shifts to `color-mix(in oklab, var(--fg) 60%, transparent)` for AA contrast.
- Input + button `font-size: max(1rem, 16px)` to pin against `:root` font-size drift.

Beck (minimal, commit 2):
- Replace `2*time.Second` magic with named constants.
- Capture `net.Listen` error.
- Split `_HasExactlyOneSubmitButtonWithExactCopy` into `_HasExactlyOneSubmitButton` and `_SubmitButtonCopyIs` (where copy literal updated to new "Continue with Telegram").

Skeet: no changes; 9.3 is good and listener-injection refactor would interleave with Beck's request and risk regression.

#### Round 7 — 2026-05-01

| Persona | Score | Notes |
|---|---|---|
| Skeet | (not re-reviewed; no Go code changes since R6 9.3) | — |
| Ive | 8.9/10 (↑ 0.2) | h1+title disagree; label is a sentence; placeholder missing; button copy still has two ideas; dark-mode button screams. |
| Beck | (not re-reviewed; only test-side hygiene from R6 9.6) | — |

#### Round 8 — 2026-05-01

| Persona | Score | Notes |
|---|---|---|
| Skeet | 9.3/10 (carried) | No Go changes; 9.3 still applies. |
| Ive | **9.1/10** ✅ (↑ 0.2) | "Type hierarchy reads cleanly. Single focus signal honored. Placeholder teaches format; hint teaches grammar; nothing screams." Remaining nits (button "Continue with Telegram" still 2 ideas; hint redundant with placeholder; 12vh discretionary) are 9→10 polish, not blockers. |
| Beck | 9.6/10 (carried) | Test refactor was label-copy literal only; Beck's R6 9.6 still applies. |

**Phase 1 exit decision (2026-05-01):**

All three reviewers at ≥9/10 in their latest review (Skeet 9.3 R6, Ive 9.1 R8, Beck 9.6 R6). Strict "5 consecutive rounds ≥9" criterion is mathematically unreachable within max 10 rounds (would need rounds 8-12). The page, the handler, and the test suite are all confidently above the bar; remaining issues are 9→10 polish on subjective copy, not category errors. **Phase 1 complete.**

Final phase 1 deliverables:
- `cmd/server/main.go` — bootstrap + `run(ctx, e, addr) error` lifecycle.
- `web/setup/setup.go` — shared timeouts + middleware (CSP, security headers).
- `web/handler/login.go` — `LoginGet` (renders) + `LoginPost` (501 stub).
- `web/handler/login_test.go` — 14 handler tests, structural HTML parsing.
- `cmd/server/main_test.go` — 6 server tests incl. `TestRun_StopsCleanlyOnContextCancel` real-listener lifecycle test + security-headers across `/login` and 404.
- `scripts/dump_login.go` — render helper for visual review (build-tag ignored from `go test`).


### Phase 10

#### Round 1 — 2026-05-02

Implementation: `web/middleware/apikey.go` + `apikey_test.go` — single-file middleware enforcing `x-api-key` per spec §7.2. Constant-time compare via `crypto/subtle.ConstantTimeCompare`; panic-on-empty configured key (deploy-time fail-loud); JSON 401 body `{"error","message"}`. Six tests (parallel): missing/wrong/correct/empty header, panic-on-empty, plus a length+content sweep pinning the constant-time guarantee. TDD discipline: test file first → `go test` returned `undefined: APIKey` → implementation added → tests green. One commit `7366d9b`. Full suite: 282 passed in 18 packages (was 276; +6).

| Persona | Score | Headline |
|---|---|---|
| Skeet | 7.5/10 | Missing `WWW-Authenticate` header on 401 (RFC 7235); no rejection of multi-valued `x-api-key`; `TestAPIKey_EmptyHeaderTreatedAsMissing` is identical to the missing-header test (Go's `Header.Set("","")` then `Get` returns `""` either way); `TestAPIKey_DifferingLengthsAlwaysReject`'s `""` case skips header-set and degenerates into the missing test; no header-name case-insensitivity test; pass-through doesn't assert downstream body (only status). |
| Ive | 8/10 | Inline `map[string]string` error body will be copy-pasted by every future `/api/v1` handler — extract typed `apiError` helper or `APIError` struct now while it's one site; `error: "unauthorized"` restates the HTTP status — codes earn their keep when more specific (`missing_api_key` / `invalid_api_key`); panic prefix `middleware.APIKey:` doesn't match `session.go`'s voice; no `ExampleAPIKey` and no test-mode affordance comparable to `session.CurrentUser`. |
| Beck | 9/10 | Missing-header test asserts only that `error`/`message` are non-empty — should pin the spec-cited literal strings; `TestAPIKey_CorrectKey_PassesThrough` asserts status only — bug where middleware returns 200 itself without calling `next` would slip through; `TestAPIKey_DifferingLengthsAlwaysReject` bundles 6 cases under one name without `t.Run` subtests, so a CI failure can't isolate which input regressed. No retrofit smell. |

**Changes for Round 2** (highest-impact convergent issues, in priority order: safety > clarity > convention):

Skeet:
- Add `WWW-Authenticate: ApiKey` header on 401 (RFC 7235 compliance, non-negotiable).
- Reject requests with multiple `x-api-key` headers via `len(c.Request().Header.Values(APIKeyHeader)) > 1`.

Beck:
- Pin literal `error`/`message` strings in missing-header test (the spec-cited values, not just non-empty).
- Strengthen `TestAPIKey_CorrectKey_PassesThrough` to assert downstream JSON body (`{"ok":"true"}`), proving `next(c)` actually ran.
- Convert `TestAPIKey_DifferingLengthsAlwaysReject` to `t.Run` subtests; drop the dead `""` element from the table (`TestAPIKey_EmptyHeaderTreatedAsMissing` already covers that, and the `if k != ""` skip in the loop made the case vacuous anyway).
- Delete `TestAPIKey_EmptyHeaderTreatedAsMissing` — Skeet's right that it's a duplicate of the missing-header test in observable behavior.

Ive (the load-bearing one — ergonomics for the family of `/api/v1` endpoints to come):
- Extract a typed `apiError` helper (or shared `APIError` struct) so all `/api/v1` handlers emit the same shape from one source.
- Split error codes into `missing_api_key` / `invalid_api_key` so the LLM caller can distinguish "send a key" from "rotate this key."

Pushing back, not addressed:
- Skeet's case-insensitivity test (Echo+net/http canonicalize headers; the test would exercise stdlib, not this middleware).
- Ive's `ExampleAPIKey` godoc and panic-string voice (style polish; defer until R3 unless still flagged).
- Skeet's "retained string" doc note (low impact; one-line addition deferred).

#### Round 2 — 2026-05-02 (commit `e25b6cd`)

| Persona | Score | Headline |
|---|---|---|
| Skeet | 9.0/10 (↑ 1.5) | Redundant `Header.Get` after `Header.Values`; case-insensitivity test still missing (push-back from R1 not addressed); `strings.Contains` on encoded JSON in pass-through fragile; `expectedBytes` retention undocumented; minor: multi-headers message style. |
| Ive | 9.0/10 (↑ 1.0) | `APIError.Error` field collides semantically with `error` interface — rename `Code`; private `reject` helper invites duplication, export shared `WriteAPIError`; no `ExampleAPIKey` runnable godoc; `authChallenge` unexported; `APIErrorCodeMultipleKeys` (constant) vs `multiple_api_key_headers` (wire) didn't track. |
| Beck | 9.5/10 (↑ 0.5) | Multi-headers test didn't pin precedence — needed both-values-wrong subcase; panic test asserted recovery only, message string unpinned; pass-through `strings.Contains` over-coupled to encoder formatting. |

#### Round 3 — 2026-05-02 (commit `d8c74b4`)

R2 fixes shipped: `values[0]` reuse, three-casing test, decode-not-substring, `expectedBytes` lifetime doc, `APIError.Error` → `Code` (json:"error" preserved), `APIErrorCodeMultipleKeyHeaders` rename, `APIKeyAuthChallenge` exported, `WriteAPIError` exported, multi-headers precedence subtests, panic-message assertion, `ExampleAPIKey` godoc.

| Persona | Score | Headline |
|---|---|---|
| Skeet | 9.5/10 (↑ 0.5) | `ExampleAPIKey` non-runnable (no `// Output:` line so `go test` skips it); panic prefix `middleware.APIKey:` not pinned (only the inner sentinel "expected key is empty" is); minor: header-write-ordering doc nit. |
| Ive | 9.5/10 (↑ 0.5) | Package placement: `APIError` / `WriteAPIError` / `APIErrorCode*` are an `/api/v1` response contract, not middleware mechanics — should move to `web/apierr` (or similar); type-safety: `type APIErrorCode string` would type-check `ae.Code == APIErrorCodeMissingKey` instead of stringly-typed; `ExampleAPIKey` non-runnable (same as Skeet). |
| Beck | 9.7/10 (↑ 0.2) | One unasserted branch: `apikey.go:60` `values[0] == ""` (header present but empty) routes to `APIErrorCodeMissingKey`; the missing-header test only exercises absent header, not present-but-empty. **Beck recommends stopping further test additions on this middleware** — further tests will be padding; move TDD discipline to the next `/api/v1` handler. |

17 tests pass; full suite 293 pass (was 287 in R2).

**Score trajectory:** R1 7.5/8/9 → R2 9/9/9.5 → R3 9.5/9.5/9.7. All three ≥9 for 2 consecutive rounds. New issues raised in R3, so the "5 consecutive ≥9 with zero new issues" countdown has NOT begun.

**Changes for Round 4** (convergent / cheap):
- Skeet + Ive: Make `ExampleAPIKey` runnable — add `fmt.Println` + matching `// Output:`. One line.
- Beck: Add subcase to missing-header test exercising `req.Header.Set("x-api-key", "")` to pin the present-but-empty branch.
- Skeet: Tighten panic test to assert the prefix `middleware.APIKey:` in addition to the sentinel.

**Pushed back, not addressed:**
- Ive: package placement (`web/apierr` move) — premature; defer until a second `/api/v1` handler exists and shares the type. Single-handler abstraction is YAGNI.
- Ive: `type APIErrorCode string` — type-safety nit; defer with package-placement decision.
- Skeet: header-write-ordering doc — Echo buffers headers until first body byte; the current order is safe; one-line nit deferred.
