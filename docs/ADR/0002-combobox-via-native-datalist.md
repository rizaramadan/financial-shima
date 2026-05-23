# ADR 0002: Combobox inputs use native `<datalist>`, not JS

## Status

Accepted — 2026-05-23

## Context

`/transactions/new` originally rendered the **Pos charged** picker as a plain `<select>`. As the Pos list grows, scrolling to the right entry becomes slow, and there is no way to type-to-jump beyond the browser's single-letter native shortcut. We want a combobox: a control where the user can both pick from a list and type to filter.

The constraints active at decision time:

- **CSP forbids inline scripts.** `web/setup/setup.go` sets `default-src 'self'` with no `script-src 'unsafe-inline'`. Inline `<script>` blocks won't execute. Only same-origin script files would.
- **No static asset handler exists.** The project ships no `/static/`, no `/js/`, no asset pipeline. There is no `e.Static(...)` call in `cmd/server/main.go`. Adding one is a small but real infrastructure step (asset directory, route registration, cache headers, embed vs. filesystem decision).
- **ADR 0001 nominates HTMX + Alpine.js for client interactivity**, but neither is wired in yet. Adopting either would mean adding a CDN include or hosting them, plus the script-src CSP allowance that goes with it.
- **The form is IDR-only today.** Web spending/income only accepts IDR Pos; cross-currency is routed through the API. `pos.(name, currency)` is `UNIQUE`, so within IDR a Pos name identifies exactly one row.

## Decision

Use a native HTML `<input list="pos-options">` bound to a `<datalist>` of Pos names. Submit the typed name as `pos_label`. The server resolves the label against `ListPos` with a case-insensitive name match and rejects unknown values.

```html
<input id="pos_label" name="pos_label" type="text" required maxlength="80"
       list="pos-options" autocomplete="off"
       value="{{.PosLabel}}" placeholder="Type to filter…">
<datalist id="pos-options">
  {{range .PosOptions}}<option value="{{.Name}}"></option>{{end}}
</datalist>
```

On validation failure the typed value round-trips into the input so the user can correct it.

## Alternatives Considered

- **Vanilla JS combobox served from a new `/static/` route.** Gives a richer control (highlighting, keyboard navigation we'd own, async filter on huge lists). Rejected for now: introduces the asset-pipeline question on a tiny single-page concern. Reconsider if a second control needs the same treatment.
- **Adopt Alpine.js (per ADR 0001) and bind a `x-data` filter.** Same asset-pipeline question, plus a CSP allowance for the Alpine bundle. Worth doing the first time we have *multiple* dynamic-UI surfaces that justify a shared dependency; one input does not.
- **HTMX server-side typeahead** (`hx-get` on input, re-render filtered list). Works but adds a per-keystroke round-trip for a list that easily fits in the page, and still needs the HTMX include + CSP allowance. Native filter is instant and offline-capable.
- **Relax CSP to allow `script-src 'unsafe-inline'`** and write a small inline script. Rejected: weakens the defence ADR 0001 implicitly relied on; small JS isn't worth a permanent CSP downgrade.
- **Submit `pos_id` (UUID) via a hidden field synced by JS.** Removes the server-side name lookup, but requires JS, which puts us back into the CSP/asset-pipeline conversation. The name-based resolution is cheap (list is small, the user already needed to be authenticated to reach the form).

## Consequences

**Easier:**

- Zero JavaScript, zero new dependencies, no CSP changes, no `/static/` handler. The change is two files: `web/template/template.go` and `web/handler/transaction_new.go`.
- Native control gets accessibility (screen reader, keyboard) and mobile keyboard handling for free.
- Degrades gracefully: a browser without `<datalist>` support (very old) renders a plain text input — the form still works, just without autocomplete.
- The pattern is reusable for any other small picklist where the form is single-currency and names are unique.

**Harder:**

- The form now submits a name, not a UUID. The handler does a `ListPos` + case-insensitive scan instead of a `uuid.Parse`. Cheap for a household-scale Pos list (tens of rows), but it's a linear scan in Go — not suitable if this pattern moves to a list of thousands.
- **Identifier ambiguity is constrained by current scope.** Name → row is unambiguous *only because* the web form is IDR-only and `(name, currency)` is `UNIQUE`. If we ever let the web form accept non-IDR Pos:
  - Either extend the datalist `<option value>` to include currency (e.g. `Groceries (idr)`) and parse it server-side, or
  - Switch to a JS-backed picker that submits the UUID. At that point the asset-pipeline question is worth re-opening anyway.
- Misspellings show as a generic "Pos not found" error rather than a guided correction. The user's typed text is preserved on re-render, so they can edit and resubmit, but there is no inline suggestion. Acceptable for a household-scale list.
- Browser autofill can sometimes interfere with `<input list>` (offering history values alongside datalist options). We set `autocomplete="off"` to suppress this; behavior across browsers is not perfectly uniform.

This ADR is scoped to the **Pos charged** input on `/transactions/new`. The decision is per-control, not project-wide — when the next interactivity need arises, weigh it against this baseline rather than assuming `<datalist>` is the answer.
