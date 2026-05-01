# Screenshots — Phase 9 UI

Rendered against the Ant Design v5 design tokens (Polar Green primary,
green-8 `#237804` for AA contrast on white). Sir Jonathan Ive (persona
review) scored these 9/10 — ship — after five rounds of critique.

All shots taken with no DB pool, so authenticated pages render their
empty states.

## Pre-auth (compact card, max-width 420px)

### Sign in
![Sign in](./screenshots/login.png)

### Verify code
![Verify code](./screenshots/verify.png)

## Authenticated (max-width 720px, global nav)

### Home
![Home](./screenshots/home.png)

### Notifications
![Notifications](./screenshots/notifications.png)

### Transactions
![Transactions](./screenshots/transactions.png)

### Spending
![Spending](./screenshots/spending.png)

## Reproducing

```bash
go run ./scripts/dump_login.go > .ive_dump/login.html
go run ./scripts/dump_verify.go > .ive_dump/verify.html
go run ./scripts/dump_authed.go home > .ive_dump/home.html
go run ./scripts/dump_authed.go notifications > .ive_dump/notifications.html
go run ./scripts/dump_authed.go transactions > .ive_dump/transactions.html
go run ./scripts/dump_authed.go spending > .ive_dump/spending.html
```

Then headless-screenshot each `.ive_dump/*.html` (any modern Chromium
will do — `msedge --headless=new --screenshot=... file:///...`).
