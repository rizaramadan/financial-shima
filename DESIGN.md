# Design system

**This file and `tokens.css` are authoritative for this project.** Follow them over
any conflicting instinct. Do not invent colours, spacing or type values that are
not expressed as tokens; a hardcoded value a token already covers is a defect.

## Tokens

The single source of values is `tokens.css`:
`--bg`, `--surface`, `--fg`, `--muted`, `--border`, `--accent`, the type
steps (`--step-*`), and the space scale (`--space-*`).

## Rules for every screen

- Colour comes from the tokens above; no ad-hoc hex values.
- Type comes from the modular scale; no invented sizes.
- Spacing comes from `--space-*`; no magic numbers.
- Every interactive element has a visible `:focus-visible` treatment and a 44px
  touch target.
- Render data surfaces in all five states: Loading, Empty, Error, Populated, Edge.
