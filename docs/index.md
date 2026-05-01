---
layout: default
title: financial-shima — UI showcase
---

# financial-shima — UI showcase

A two-user family financial manager. Telegram OTP login, multi-currency
Pos envelope budgeting (IDR + USD), and an append-only ledger with
inter-Pos transfers and obligations.

The visual layer is **Ant Design v5** with the Polar Green palette —
primary shifted to **green-8 `#237804`** so primary text on white
clears WCAG AA at ~6:1, while `--success` stays at green-6 to keep
the two semantic tokens distinct.

Screenshots below were rendered with seeded sample data (not the
empty-state placeholder) so the formatting, type chips, currency
rendering, progress bars, and obligation flow are all visible.

## Pre-auth — compact card (`max-width: 420px`)

### Sign in
![Sign in](./screenshots/login.png)

### Verify code
6-digit code input with monospace + letter-spacing + text-indent.

![Verify code](./screenshots/verify.png)

## Home — accounts + Pos by currency

Account balances in IDR, Pos grouped by currency with budget-progress
rails on rows that have a target, unread-count pill in the nav.

![Home](./screenshots/home.png)

## Transactions — chips + colored amounts

Income / Expense / Transfer chips, color-coded amounts (green `+` for
income, default for expense, neutral for transfers), reversed
transactions render line-through with a `reverses →` link, USD
formatting alongside IDR (`$500.00` vs `Rp 25.000.000`).

![Transactions](./screenshots/transactions.png)

## Spending — months × top-N Pos pivot

6 months × 5 Pos with row totals and a Pos-totals footer. Wide card
modifier (`max-width: 920px`) for data-dense views.

![Spending](./screenshots/spending.png)

## Pos detail — balance, obligations, transactions

Single-Pos drill-in: balance row, open obligations with direction chip
(`payable` / `receivable`), and the scoped transaction list.

![Pos detail](./screenshots/pos.png)

## Notifications — read/unread feed

Unread items render at full text weight; read items fade to secondary.
Each item shows a relative timestamp and a per-item Mark-read action,
plus a Mark-all-read at the top.

![Notifications](./screenshots/notifications.png)

## Reproducing

Empty-state renders go through the actual handler chain (DB pool nil →
empty data fallback):

```bash
go run ./scripts/dump_login.go > .ive_dump/login.html
go run ./scripts/dump_verify.go > .ive_dump/verify.html
go run ./scripts/dump_authed.go {home|notifications|transactions|spending} > out.html
```

Loaded renders bypass the handler and call `template.Renderer` directly
with seeded data:

```bash
go run ./scripts/dump_loaded.go {home|transactions|spending|notifications|pos|verify} > out.html
```

Headless-screenshot each `.html` with any Chromium:

```bash
msedge --headless=new --screenshot=out.png --window-size=900,1100 file:///path/to/out.html
```
