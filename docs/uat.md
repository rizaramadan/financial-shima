---
layout: default
title: financial-shima — UAT walkthrough
---

# UAT walkthrough

End-to-end acceptance tests captured by **Playwright driving real
Chromium against the real local app** (`scripts/dev_server.go` —
production handler tree + an in-process OTP shim that lets the
script complete the auth flow without Telegram). Every screenshot
below is a real browser screenshot at the moment the test asserted.

→ [UI showcase](./) for the design tour. ← [Repo](https://github.com/rizaramadan/financial-shima).

The driver lives at `scripts/playwright/uat.js`. Re-run any time:

```bash
go run ./scripts/dev_server.go &
node scripts/playwright/uat.js
```

---

## 1. Login fails — unknown identifier (S2)

Submitting an unknown Telegram handle re-renders `/login` with an
inline alert. **No OTP is sent.** Form is safe to retry within rate
limits.

| Empty form | After submit |
|---|---|
| ![empty login](./screenshots/uat/01a_login_empty.png) | ![user not found](./screenshots/uat/01b_login_user_not_found.png) |

Server says: *"User not found."*

---

## 2. Sign in with OTP — happy path (S1)

Type identifier → server issues 6-digit code → enter it → session
cookie issued, redirected to `/`.

| 1. Login filled | 2. /verify empty | 3. /verify filled |
|---|---|---|
| ![login filled](./screenshots/uat/02a_login_filled.png) | ![verify empty](./screenshots/uat/02b_verify_empty.png) | ![verify filled](./screenshots/uat/02c_verify_filled.png) |

After verify: 303 → `/`.

---

## 3. Authenticated home dashboard (S17)

Three accounts with derived IDR balances; Pos rows grouped by
currency (IDR · gold-g · USD); progress rails on rows that have a
target; negative-cash marker (red `▾`) on **Petty Cash**.

![home dashboard](./screenshots/uat/03_home_dashboard.png)

---

## 4. Create Pos — empty name rejected

Server-side validation rejects whitespace-only `name` (`logic/pos.Validate`).

| Empty form | After submit |
|---|---|
| ![pos/new empty](./screenshots/uat/04a_pos_new_empty.png) | ![empty name error](./screenshots/uat/04b_pos_new_empty_name_error.png) |

Server says: *"Name is required."*

---

## 5. Create Pos — invalid currency rejected

`currency` must match `^[a-z0-9-]+$`. Server normalizes (lowercase +
trim) before validating, so `"BAD CURRENCY"` (with space) still
fails the regex.

![bad currency](./screenshots/uat/05_pos_new_bad_currency_error.png)

Server says: *"Currency must be lowercase letters, digits, or hyphens (e.g. idr, usd, gold-g)."*

---

## 6. Create Pos — duplicate name caught at the DB

Submitting `name=Mortgage`, `currency=idr` (already in seed) triggers
the schema's `UNIQUE (name, currency)` constraint. The handler
catches Postgres error code `23505` and surfaces it as a form error
— the user never sees a 500.

![duplicate](./screenshots/uat/06_pos_new_duplicate_error.png)

Server says: *"A Pos with that name and currency already exists."*

---

## 7. Create Pos — happy path (S5 prep)

Fill the form → submit → 303 → `/pos/<uuid>` → balance + formatted
target render via `money()`.

| 1. Filled | 2. Created |
|---|---|
| ![filled](./screenshots/uat/07a_pos_new_filled.png) | ![created](./screenshots/uat/07b_pos_detail_after_create.png) |

Subtitle: **`idr · target Rp 15.000.000`** (formatter ran).

---

## 8. Pos detail with open obligation (S18)

Belanja Bulanan owes Mortgage Rp 1.500.000 (seed scenario). The
obligations table renders the `payable` chip + counterparty Pos
**resolved by name** (not raw UUID, because the handler JOINs).

![pos with obligation](./screenshots/uat/08_pos_detail_with_obligation.png)

---

## 9. Transactions list (S16)

Type chips (Income / Expense / Transfer) + sign-prefixed colored
amounts + the wrong-charge / reversal pair rendered with line-through
+ `reverses →` link.

![transactions](./screenshots/uat/09_transactions_list.png)

---

## 10. Spending months × Pos pivot (S19)

Six months × top-N Pos by spending volume. Per-cell currency
formatting; row totals on the right edge; Pos totals row on the
bottom.

![spending](./screenshots/uat/10_spending_pivot.png)

---

## 11. Notifications feed (S22)

Unread items in bold; read items faded. `Mark all read (3)` CTA at
top; per-row Mark-read affordance. Times rendered via the `relTime`
template func.

![notifications](./screenshots/uat/11_notifications_feed.png)

---

## 12. Income templates — list

A salary-allocation template names an income type and the fixed
allocation across Pos when it lands. Templates are the **only** way
for a single incoming event to credit multiple Pos; without one,
`money_in` stays single-Pos.

![income templates list](./screenshots/uat/12_income_templates_list.png)

---

## 13. Income template — create end-to-end

Form lets the operator name the template, optionally pick a
**leftover Pos** (absorbs any amount above Σ(lines) on apply), and
add up to 8 line rows (Pos + amount). Submit → redirected to the
detail view.

| 1. Empty form | 2. Filled | 3. After create |
|---|---|---|
| ![empty](./screenshots/uat/13a_income_template_new_empty.png) | ![filled](./screenshots/uat/13b_income_template_new_filled.png) | ![created](./screenshots/uat/13c_income_template_detail_after_create.png) |

Detail subtitle: `Income template · allocation total Rp 20.000.000 · leftover → Mortgage`.

---

## 14. Income template — preview the suggested allocation

When the operator submits the **incoming form** (amount, date,
account, counterparty), the server runs the template's allocation
logic to produce a **suggested split** and renders an editable
preview page — the human sees the proposed breakdown BEFORE
anything is written. No transactions exist yet; this step has zero
side effects.

![preview suggested](./screenshots/uat/14_income_template_preview_suggested.png)

The Incoming summary card pins what was entered (Rp 25.000.000
salary). The Allocation table is pre-filled from the template:
3 lines (Anak Sekolah 12M, Belanja Bulanan 5M, Liburan 3M) plus a
leftover row (Mortgage 5M, since 25M − 20M = 5M overflow). Six
empty rows below are slots for adding new allocations.

---

## 15. Income template — adjust and approve

The human can edit any row before approving — bump amounts, swap
the Pos, or zero out the leftover and add a new row. On submit,
the server validates **Σ(rows) = entered amount** (else
re-renders with the mismatch error) and creates one money_in
transaction per non-empty row, atomically per row, with
idempotency keys derived from the preview-stamped key + the row's
pos_id. Re-approving the same preview yields identical txn ids
(retry is safe).

| 1. After adjust (Liburan 3M → 5M, Mortgage leftover 5M → 3M) | 2. After approve |
|---|---|
| ![preview adjusted](./screenshots/uat/15a_income_template_preview_adjusted.png) | ![apply success](./screenshots/uat/15b_income_template_apply_after_adjust.png) |

Flash: *"Approved & applied 25000000 across 4 Pos."* Σ stays 25M
(12 + 5 + 5 + 3) so validation passes; the human's adjustment
overrode the template's leftover suggestion by changing two row
amounts.

---

## 16. Verify fails — wrong OTP (S2)

Submitting a wrong 6-digit code increments the per-OTP attempt
counter and re-renders `/verify` inline with an error. Run with
`@shima` so it doesn't perturb Riza's earlier successful flow.

| /verify empty | After wrong code |
|---|---|
| ![verify empty](./screenshots/uat/16a_verify_empty_for_negative.png) | ![wrong code](./screenshots/uat/16b_verify_wrong_code.png) |

Server says: *"That code did not match. Try again."* (After 3 wrong
attempts the OTP locks; user must request a new one.)

---

## Summary

| | Scenario | Status |
|---|---|---|
| 1 | Login: unknown identifier → "User not found" | ✓ |
| 2 | Login: full OTP flow → session | ✓ |
| 3 | Home: derived balances + progress + neg-cash marker | ✓ |
| 4 | /pos/new: empty name rejected | ✓ |
| 5 | /pos/new: invalid currency rejected | ✓ |
| 6 | /pos/new: duplicate name → DB UNIQUE caught | ✓ |
| 7 | /pos/new: create + redirect + formatted detail | ✓ |
| 8 | Pos detail: obligation surfaces + counterparty by name | ✓ |
| 9 | Transactions: chips, signs, colors, reversals | ✓ |
| 10 | Spending: months × Pos pivot, formatted cells | ✓ |
| 11 | Notifications: read/unread distinction, mark-read | ✓ |
| 12 | Income templates: list page | ✓ |
| 13 | Income template: create form → detail (with leftover Pos) | ✓ |
| 14 | Income template: preview shows suggested allocation | ✓ |
| 15 | Income template: human adjusts then approves | ✓ |
| 16 | Verify: wrong OTP → inline error | ✓ |

Plus two API harnesses driving the same handlers programmatically:

- `scripts/e2e_api.go` — 20 steps over the 4 base POST endpoints
  (accounts, pos, counterparties, transactions) with DB consistency
  checks, idempotency, validation rejections.
- `scripts/e2e_income_template.go` — 10 steps over the income-template
  surface: create + line persistence, apply rejection (below-sum),
  apply with exact match (3 txns), apply with leftover (4 txns), DB
  sum verification, idempotency (re-apply yields same ids), strict
  template (no leftover) rejecting over-sum, list endpoint.
