# Spec 0002: MVP — User Scenarios

## Status

Draft — 2026-05-02

## Purpose

A reader-facing companion to `0001-mvp.md`. Where 0001 documents the system structurally (data model, validation, code organization), this document walks through what a user can actually *do*. Every scenario maps to a section of 0001; nothing here introduces new behavior.

## Conventions

Each scenario uses this shape:

- **Actor** — Riza, Shima, LLM, or Operator (initial setup).
- **Preconditions** — required state before the scenario runs.
- **Trigger** — what kicks it off.
- **Steps** — numbered, from the actor's seat.
- **Outcome** — what's true at the end.
- **Spec refs** — where in 0001 each rule lives.

Scenarios are grouped by area; the index table below maps each to the primary 0001 section.

## Index

| ID | Title | Primary refs |
|---|---|---|
| S1 | Riza logs in with OTP | §3.2, §3.3 |
| S2 | Login fails (unknown user, wrong OTP, expired OTP, lockout) | §3.2, §3.3 |
| S3 | Riza logs out | §3.4 |
| S4 | Session expires after 7 days; re-login required | §3.4 |
| S5 | Record a salary (money_in, IDR) | §4.3 Type A, §5.1 |
| S6 | Record groceries (money_out, IDR) | §4.3 Type B, §5.1 |
| S7 | Counterparty autocomplete and inline create | §4.4 |
| S8 | Reallocate Pos → Pos (no debt) | §4.3 Type C, mode=reallocation |
| S9 | Borrow between Pos (creates obligations) | §4.3 Type C, mode=borrow |
| S10 | Repay an inter-Pos borrow | §4.3 (repayment matching) |
| S11 | Record money_in / money_out involving a non-IDR Pos | §4.2, §4.3 |
| S12 | Cross-currency inter-Pos borrow | §4.3 (cross-currency handling) |
| S13 | Hard-edit a note or counterparty | §5.2 |
| S14 | Reverse-and-re-enter for amount / date / account / pos | §5.2 |
| S15 | Delete a transaction (logical delete via reversal) | §5.3 |
| S16 | Browse the transaction list with filters | §6.1 |
| S17 | View the current balances dashboard | §6.2 |
| S18 | Drill into a Pos breakdown | §6.3 |
| S19 | View spending by Pos over time | §6.4 |
| S20 | Resolve a Pos with negative cash | §5.5 |
| S21 | Shima sees a notification when Riza posts a transaction | §4.5, §5.4 |
| S22 | Open the notifications feed and mark items read | §6.5 |
| S23 | Operator seeds initial state via the LLM and gsheet | §9, §7.2 |
| S24 | LLM logs a transaction on a user's behalf | §7.2 |
| S25 | LLM lists or reverses transactions via API | §7.2 |
| S26 | API request without `x-api-key` is rejected | §7.2 |

---

## Authentication

### S1 — Riza logs in with OTP

- **Actor**: Riza.
- **Preconditions**: Riza is seeded with `display_name="Riza"` and a valid `telegram_identifier`. The OTP assistant API is reachable.
- **Trigger**: Riza opens `/login`.
- **Steps**:
  1. Form has one input: Telegram ID or `@username`. Riza enters his identifier and submits.
  2. App looks up the matching user, generates a 6-digit OTP, stores it server-side with a 5-minute expiry, and `POST`s `{"message": "send OTP <code> to Riza"}` to the OTP assistant.
  3. App renders the OTP-entry form.
  4. Riza receives the OTP via Telegram and enters it.
  5. App verifies; on success, issues a session cookie (`Secure`, `HttpOnly`, `SameSite=Lax`) with a 7-day rolling expiry and clears the stored OTP.
- **Outcome**: Riza is now authenticated; subsequent requests carry the session cookie. (0001 does not specify a post-login destination.)
- **Spec refs**: §3.1, §3.2, §3.3, §3.4, §7.3.

### S2 — Login fails

Variants, all surfaced through the same `/login` flow:

- **Unknown identifier**: lookup misses → page renders `"User not found."` No OTP is sent. (§3.2 step 2.)
- **Wrong OTP**: increment per-OTP attempt counter. After 3 failed attempts the OTP is locked; Riza must request a new one. (§3.2 step 6, §3.3.)
- **Expired OTP**: 5 minutes have passed since OTP was generated; verification fails. Riza requests a new OTP, subject to the 60-second resend cooldown. (§3.3.)

In every variant, **outcome** = no session is issued; the operation is safe to retry within rate limits.

### S3 — Riza logs out

- **Actor**: Riza, authenticated.
- **Trigger**: Riza clicks "Log out."
- **Steps**: app revokes the session row in the DB and clears the cookie.
- **Outcome**: subsequent requests to authenticated routes redirect to `/login`. (§3.4, §7.1.)

### S4 — Session expires after 7 days

- **Actor**: Riza, authenticated.
- **Preconditions**: 7+ days have passed since Riza's last authenticated request.
- **Trigger**: Riza opens any authenticated route.
- **Outcome**: redirect to `/login`. The 7-day expiry is rolling: every authenticated request renews it. (§3.4.)

---

## Recording — basic

### S5 — Record a salary (money_in, IDR)

- **Actor**: Riza, authenticated.
- **Preconditions**: account `BCA Shima` exists and is non-archived; Pos `Bulanan` exists, currency `IDR`, non-archived; counterparty `Salary` exists or will be created inline.
- **Trigger**: Riza opens the new-transaction form on `/transactions`.
- **Steps**:
  1. Selects type `money_in`, account `BCA Shima`, Pos `Bulanan`, counterparty `Salary` (autocomplete; see S7), amount `15,000,000` IDR, optional note, `effective_date = today`.
  2. Submits.
  3. App validates per §5.1: `effective_date ≤ today`, account non-archived, Pos non-archived, `pos.currency = IDR` so `account_amount == pos_amount`, counterparty regex passes, amounts positive, `idempotency_key` unique.
  4. App inserts the transaction with `source=web`, `created_by=riza`, and writes a notification for Shima in the same DB transaction (§5.4).
- **Outcome**: a new row appears at the top of the transaction list via HTMX swap. `BCA Shima` balance and Pos `Bulanan.cash_balance` each rise by 15,000,000 in any view that consults them (§6.2, §6.3). Shima's unread notification count goes up.
- **Spec refs**: §4.3 Type A, §4.4, §5.1, §5.4, §6.1.

### S6 — Record groceries (money_out, IDR)

- **Actor**: Shima, authenticated.
- **Preconditions**: account exists and non-archived; Pos `Bulanan` exists, IDR, non-archived; counterparty `Indomaret` exists or is created inline.
- **Trigger**: Shima opens the new-transaction form.
- **Steps**: identical to S5 but type `money_out`, counterparty `Indomaret`, amount `500,000`.
- **Outcome**: account and Pos balances each fall by 500,000. Riza receives a notification.
- **Spec refs**: §4.3 Type B, §5.1, §5.4.

### S7 — Counterparty autocomplete and inline create

- **Actor**: any.
- **Preconditions**: counterparty input visible (S5 or S6).
- **Steps**:
  1. User types in the counterparty field.
  2. App suggests existing counterparties via case-insensitive match against `name_lower`. (0001 §4.4 specifies "case-insensitive match" without pinning prefix vs substring vs fuzzy; the choice is an implementation detail.)
  3. If the user picks a suggestion, the existing row is reused.
  4. If the typed name is not in the list, submitting the form creates a new counterparty row inline. The first-typed casing is preserved as `name`; future typing of any casing of the same lowercased name matches this row.
  5. Validation: name matches `^[a-zA-Z0-9_\- ]+$`, trimmed, no tabs/newlines.
- **Outcome**: a counterparty row exists for the entry; subsequent transactions can reuse it.
- **Spec refs**: §4.4, §5.1.

---

## Recording — inter-Pos

### S8 — Reallocate Pos → Pos (no debt)

- **Actor**: Riza, authenticated.
- **Preconditions**: Pos `Tabungan Liburan` (IDR) and Pos `Tabungan Mobil` (IDR) exist, non-archived.
- **Trigger**: Riza opens the new-inter-Pos form.
- **Steps**:
  1. Selects type `inter_pos`, mode `reallocation`.
  2. Adds an `out` line: Pos `Tabungan Liburan`, amount `2,000,000`.
  3. Adds an `in` line: Pos `Tabungan Mobil`, amount `2,000,000`.
  4. Submits.
  5. App validates per §5.1: at least one `out` and one `in` line; `Σ(out IDR) = Σ(in IDR) = 2,000,000`; `pos.currency = line.amount.currency` for each line.
- **Outcome**: `Tabungan Liburan.cash_balance` falls by 2M; `Tabungan Mobil.cash_balance` rises by 2M. **No `pos_obligation` row** is created — reallocation is permanent. (§4.3 mode=reallocation.)
- **Spec refs**: §4.3 Type C, §5.1.

### S9 — Borrow between Pos (creates obligations)

- **Actor**: Riza, authenticated.
- **Preconditions**: source Pos `kids school` (IDR) and `self insurance` (IDR) exist; destination Pos `loan to sister` (IDR) exists. Goal: prepare 5M for a `money_out` to sister, drawn from two source Pos.
- **Trigger**: Riza opens the new-inter-Pos form.
- **Steps**:
  1. Type `inter_pos`, mode `borrow`.
  2. Out lines: `kids school` 3,000,000 and `self insurance` 2,000,000.
  3. In line: `loan to sister` 5,000,000.
  4. Submits.
  5. App validates §5.1; for each `out` × `in` pairing it generates a `pos_obligation` row. `amount_owed` is the debtor's `in` amount prorated across creditors. (0001 §4.3 names the proration but doesn't pin the formula. The natural reading — proration by share of out-line contributions — yields `kids school` owed 3M and `self insurance` owed 2M here, both denominated in the **debtor's** currency, IDR — Pattern P.)
- **Outcome**: source Pos balances drop; destination Pos balance rises; obligations are open (`cleared_at IS NULL`). A subsequent `money_out` (S6) can then send the funds out of the destination Pos to the sister via a bank account.
- **Spec refs**: §4.3 Type C, §5.1.

### S10 — Repay an inter-Pos borrow

- **Actor**: Riza, authenticated. Continues from S9.
- **Preconditions**: open obligations exist where `loan to sister` is debtor and `{kids school, self insurance}` are creditors. Sister has repaid the cash, captured by a `money_in` (S5 with counterparty="Sister") that credits Pos `loan to sister`.
- **Trigger**: Riza opens a new inter-Pos transfer to settle.
- **Steps**:
  1. Type `inter_pos`, mode `borrow`. (Repayment uses borrow mode in reverse; the system matches against open obligations.)
  2. Out line: `loan to sister` 5,000,000.
  3. In lines: `kids school` 3,000,000 and `self insurance` 2,000,000.
  4. Submits.
  5. App matches each `(creditor_pos_id, debtor_pos_id)` pair against open obligations FIFO and increments `amount_repaid`. When `amount_repaid >= amount_owed`, `cleared_at` is set.
- **Outcome**: original obligations close (`cleared_at` set). Source / destination balances net back to pre-S9 state in IDR terms.
- **Edge case (per 0001 §4.3)**: if a repayment transaction has *no matching open obligation* — e.g., `loan to sister` sends to `kids school` when no debt currently exists — the repayment still succeeds and creates a *new* obligation in the opposite direction (`loan to sister` becomes the creditor). 0001 does not specify behavior for *partially*-unmatched surplus (over-paying an existing obligation by some amount); this is an open question that should be resolved in 0001 before implementation.
- **Spec refs**: §4.3 (repayment matching, FIFO, inverse-obligation case).

---

## Multi-currency

### S11 — Record money_in / money_out involving a non-IDR Pos

- **Actor**: Riza, authenticated.
- **Preconditions**: Pos `loan to sister` exists with `currency = gold-g` (validated `^[a-z0-9\-]+$`), non-archived, currently holding 10 gold-g cash (populated via any prior credit — opening-balance transaction, money_in, or an inter-Pos transfer such as S12). Account `BCA Shima` (IDR) exists, non-archived. Counterparty `Sister` exists or is created inline.
- **Trigger**: Riza opens the new-`money_out` form to dispatch the gold loan to sister externally.
- **Steps**:
  1. Type `money_out`, account `BCA Shima`, Pos `loan to sister`.
  2. Because `pos.currency != IDR`, the form requires both amounts: `account_amount = 12,000,000` (IDR sent via bank) and `pos_amount = 10` (gold-g leaving the allocation).
  3. Counterparty `Sister`. Submits.
  4. App validates: `pos.currency = line.amount.currency`; both amounts positive; counterparty present and matching `^[a-zA-Z0-9_\- ]+$`.
- **Outcome**: per spec §4.3 Type B (sign reversed), **both sides fall**: `BCA Shima` drops 12M IDR; `loan to sister.cash_balance` drops 10 gold-g. Per-currency reconciliation (§10.5) holds independently for IDR and gold-g. The Pos's outstanding payable to `kids school` (created in S12) is unchanged — that liability clears later via inverse repayment when sister returns the gold.
- **Inverse case**: when sister returns 1g, `money_in` records the symmetric event — `BCA Shima` rises by whatever IDR she sent, and `loan to sister.cash_balance` rises by 1 gold-g (the gold flows back into the local allocation). Both sides rise, per §4.3 Type A.
- **Spec refs**: §4.2, §4.3 Type A and Type B, §5.1.

### S12 — Cross-currency inter-Pos borrow

- **Actor**: Riza, authenticated.
- **Preconditions**: Pos `kids school` (IDR), Pos `loan to sister` (gold-g) — both non-archived.
- **Trigger**: Riza opens new-inter-Pos to lend gold-equivalent value to sister sourced from an IDR Pos.
- **Steps**:
  1. Type `inter_pos`, mode `borrow`.
  2. Out line: `kids school` 12,000,000 IDR.
  3. In line: `loan to sister` 10 gold-g.
  4. Submits.
  5. App validates: lines on opposite directions may carry different currencies (§4.3 cross-currency handling). System does not convert. `Σ(out IDR) = 12M` and `Σ(in gold-g) = 10` are tracked per-currency separately.
  6. Obligation generated in the debtor's currency (gold-g): `kids school` is owed 10 gold-g by `loan to sister` (Pattern P).
- **Outcome**: `kids school` cash falls by 12M IDR; `loan to sister` cash rises by 10 gold-g. Open obligation persists in gold-g until repaid (S10 in reverse). If gold price has moved at repayment time, `kids school` may end up with residual negative IDR cash — see S20.
- **Spec refs**: §4.3 (cross-currency, Pattern P), §5.1, §10.5, §10.6.

---

## Editing and deletion

### S13 — Hard-edit a note or counterparty

- **Actor**: any user, authenticated.
- **Preconditions**: a transaction exists.
- **Trigger**: user opens the row's edit affordance and changes only `note` or `counterparty_id`.
- **Steps**: app `UPDATE`s the row in place. No reversal entry is created.
- **Outcome**: the transaction's metadata changes; balances unchanged. A notification is sent to the *other* user (§5.4: edits trigger notifications on the modification).
- **Spec refs**: §5.2 (hard-edit), §5.4.

### S14 — Reverse-and-re-enter for amount / date / account / pos

- **Actor**: any user, authenticated.
- **Preconditions**: a transaction `T` exists. The user wants to change a balance- or period-affecting field.
- **Trigger**: user opens the row's edit affordance and changes `amount`, `effective_date`, `account_id`, or `pos_id`.
- **Steps**:
  1. App inserts a reversal transaction `T-rev` with `reverses_id = T.id` and sign-flipped amounts.
  2. App inserts a fresh corrected transaction `T'` with the new field values.
  3. Original row `T` is retained.
  4. Notifications fire on the modification, not the original (§5.4).
- **Outcome**: three rows visible in the ledger (`T`, `T-rev`, `T'`); balances reflect only `T'`. Reversal entries display with a strikethrough/badge linking to `T` (§6.1).
- **Spec refs**: §5.2 (reverse-and-re-enter), §5.4, §6.1.

### S15 — Delete a transaction (logical delete via reversal)

- **Actor**: any user, authenticated.
- **Preconditions**: a transaction `T` exists.
- **Trigger**: user clicks "Delete" on a row.
- **Steps**: app inserts a reversal transaction `T-rev` with `reverses_id = T.id` and sign-flipped amounts. No `DELETE` SQL is ever issued on the `transactions` table.
- **Outcome**: balances reflect the cancellation; `T` is retained in history with its reversal. Notifications fire on the reversal.
- **Spec refs**: §5.3, §10.3 (append-only ledger).

**Non-editable**: `type` and `mode` cannot be changed in place. To "edit" type or mode, the user deletes (S15) and recreates the transaction.

---

## Balances and views

### S16 — Browse the transaction list with filters

- **Actor**: any user, authenticated.
- **Trigger**: user opens `/transactions`.
- **Steps**:
  1. Default view: last 30 days, all accounts, all Pos, all counterparties, all types, newest first.
  2. User narrows by any combination of: date range, account (multi-select), Pos (multi-select), counterparty (multi-select), type (`money_in` / `money_out` / `inter_pos`).
  3. List re-renders with matching rows.
- **Outcome**: each row shows date, type, amount (per-currency), account, Pos(es), counterparty, note. Reversal entries render with a strikethrough or "reversed" badge linking to the row they reverse.
- **Spec refs**: §6.1.

### S17 — View the current balances dashboard

- **Actor**: any user, authenticated.
- **Trigger**: user opens `/`.
- **Outcome**: two sections render.
  - **Accounts**: table of non-archived accounts with current IDR balance, sorted by name.
  - **Pos**: table of non-archived Pos grouped by currency. For each Pos: name, cash balance, receivables, payables, target (if set), progress-toward-target percentage. Pos with negative cash carry a marker.
- **Spec refs**: §6.2.

### S18 — Drill into a Pos breakdown

- **Actor**: any user, authenticated.
- **Trigger**: user clicks a Pos name (from S17 or S16).
- **Outcome**: `/pos/:id` renders with name, currency, target, current cash / receivables / payables, list of open obligations (this Pos as creditor or debtor), and a chronological transaction list scoped to this Pos.
- **Spec refs**: §6.3.

### S19 — View spending by Pos over time

- **Actor**: any user, authenticated.
- **Trigger**: user opens `/spending`.
- **Steps**: user optionally selects a date range (default last 6 months).
- **Outcome**: bar or table view; rows are months, columns are top-N Pos by `money_out` volume in the selected range.
- **Spec refs**: §6.4.

### S20 — Resolve a Pos with negative cash

- **Actor**: any user, authenticated.
- **Preconditions**: a Pos's `cash_balance < 0` (e.g., post-S12 after gold price drop).
- **Trigger**: user notices the deficit on the dashboard (S17, marker present) or in the Pos detail view (S18).
- **Steps**: any of three resolutions:
  - **Reallocate** from another Pos with positive cash, same currency (S8).
  - **Allocate future income** by recording a `money_in` to that Pos (S5).
  - **Acknowledge the deficit** — leave the negative balance in place. There is no automatic remediation, no manual-adjustment transaction type, and no write-off.
- **Outcome**: the user's chosen resolution is applied; the system imposes nothing.
- **Spec refs**: §5.5.

---

## Notifications

### S21 — Shima sees a notification when Riza posts a transaction

- **Actor**: Shima (recipient); Riza (acting).
- **Preconditions**: Riza authenticated, Shima authenticated (or has an open session).
- **Trigger**: Riza completes any of S5, S6, S8–S15.
- **Steps**:
  1. Riza's transaction insert succeeds.
  2. In the same DB transaction, the system invokes `notification.RecipientsFor(tx, allUsers)`. For `source=web`, recipients are all users where `user_id != tx.created_by` — which is Shima.
  3. A `notification` row is written for Shima with `type=transaction_created`, `title` and `body` summarizing Riza's action, `related_transaction_id` set, `read_at = NULL`.
- **Outcome**: Shima's unread count rises. The header bell badge updates within 30 seconds via HTMX poll. **Shima never receives notifications for her own actions** (recipient rule excludes the creator).
- **Spec refs**: §4.5, §5.4, §6.5.

### S22 — Open the notifications feed and mark items read

- **Actor**: Shima, authenticated.
- **Preconditions**: at least one unread notification exists.
- **Trigger**: Shima clicks the bell icon (or navigates to `/notifications`).
- **Steps**:
  1. Page renders the per-user feed, newest first, with unread rows visually distinguished.
  2. Shima clicks a row → app sets `read_at` to the current time; the row's clickthrough takes her to `/transactions/:related_transaction_id` if set.
  3. Or: Shima clicks "Mark all read" at the top → all unread rows for Shima get `read_at` set in one update.
- **Outcome**: unread count drops; bell badge updates on next poll.
- **Spec refs**: §6.5.

---

## LLM API and initial setup

### S23 — Operator seeds initial state via the LLM and gsheet

- **Actor**: Operator (Riza acting as deployer, day zero); LLM as agent.
- **Preconditions**: app is deployed; `LLM_API_KEY` is set in the environment; the existing Google Sheet of historical balances is accessible to the LLM.
- **Trigger**: Operator points the LLM at the gsheet and instructs it to seed.
- **Steps**:
  1. LLM reads the sheet.
  2. LLM `POST`s to `/api/v1/accounts` once per account (with header `x-api-key`).
  3. LLM `POST`s to `/api/v1/pos` once per Pos, including `currency` and optional `target`.
  4. LLM `POST`s to `/api/v1/transactions` once per opening-balance entry: type `money_in`, counterparty `"Opening Balance"`, `effective_date` matching the sheet, `idempotency_key` unique per row.
  5. Each insert stamps `source = api`. Per the recipient rule (§4.5), `source=api` transactions notify both users — initial setup will therefore generate notifications.
- **Outcome**: per-currency `Σ(Pos.cash) = Σ(Account)` is verifiable by reading current balances (§9). The dashboard (S17) reflects the imported state.
- **Spec refs**: §9, §7.2.

### S24 — LLM logs a transaction on a user's behalf

- **Actor**: Riza via Telegram → AI assistant; LLM as caller.
- **Preconditions**: ongoing operation; `LLM_API_KEY` set.
- **Trigger**: Riza tells the assistant something like *"log Rp 50,000 groceries from Pos Bulanan via BCA Shima"*.
- **Steps**:
  1. Assistant calls `POST /api/v1/transactions` with a JSON body matching the spec, including a fresh `idempotency_key`.
  2. App validates per §5.1 (same logic as web).
  3. Insert succeeds; transaction is stamped `source = api`; both users receive notifications (recipient rule).
- **Outcome**: transaction appears in the list (S16); Riza and Shima both see a notification.
- **Spec refs**: §7.2, §4.5.

### S25 — LLM lists or reverses transactions via API

- **Actor**: LLM.
- **Trigger**: a need to read state or undo an entry.
- **Steps**:
  - **List**: `GET /api/v1/transactions` with filters mirroring §6.1 (date range, account, Pos, counterparty, type). JSON response.
  - **Reverse**: `POST /api/v1/transactions/:id/reverse`. App inserts a reversal entry (same effect as S15).
- **Outcome**: read or correction completed via API; same validation and notification rules as the web path.
- **Spec refs**: §7.2.

### S26 — API request without `x-api-key` is rejected

- **Actor**: any caller.
- **Trigger**: a request to any `/api/v1/*` endpoint missing or carrying an invalid `x-api-key`.
- **Outcome**: `401`. Body is `{"error": "code", "message": "..."}`.
- **Spec refs**: §7.2.

---

## Coverage check

This document covers every operation surface in 0001:

- §3 (auth) — S1–S4.
- §4 (data model) and §5 (operations) — S5–S15, S20.
- §6 (views) — S16–S19, S22.
- §4.5, §5.4, §6.5 (notifications) — S21, S22.
- §7.2 (LLM API), §9 (initial setup) — S23–S26.

Items deliberately *not* covered, per §11 deferred list: cash / e-wallet / credit-card / investment account types, cross-currency exchange transactions, FX-aware net-worth, recurring transactions, attachments, manual adjustment / write-off, push notifications beyond OTP, additional notification types, counterparty-total / cash-flow / net-worth views, user self-service registration.
