# CLAUDE.md

Practices for working in this repo. Extracted from `docs/`. Bias toward caution over speed; use judgment on trivial tasks.

## 1. Think Before Coding

Don't assume. Don't hide confusion. Surface tradeoffs.

- State assumptions explicitly. If uncertain, ask.
- If multiple interpretations exist, present them — don't pick silently.
- If a simpler approach exists, say so. Push back when warranted.
- If something is unclear, stop. Name what's confusing. Ask.

## 2. Simplicity First

Minimum code that solves the problem. Nothing speculative.

- No features beyond what was asked.
- No abstractions for single-use code.
- No "flexibility" or "configurability" that wasn't requested.
- No error handling for impossible scenarios.
- If you write 200 lines and it could be 50, rewrite it.

Test: "Would a senior engineer say this is overcomplicated?" If yes, simplify.

## 3. Surgical Changes

Touch only what you must. Clean up only your own mess.

- Don't "improve" adjacent code, comments, or formatting.
- Don't refactor things that aren't broken.
- Match existing style, even if you'd do it differently.
- If you notice unrelated dead code, mention it — don't delete it.
- Remove imports/variables/functions that *your* changes made unused. Leave pre-existing dead code alone unless asked.

Test: every changed line should trace directly to the user's request.

## 4. Goal-Driven Execution

Define success criteria. Loop until verified.

Transform tasks into verifiable goals:
- "Add validation" → write tests for invalid inputs, then make them pass.
- "Fix the bug" → write a test that reproduces it, then make it pass.
- "Refactor X" → ensure tests pass before and after.

For multi-step tasks, state a brief plan with a verification check per step.

## 5. Code Structure: Three Layers

Organize source code by what each part really is. Three categories of folders at the root:

- **Logic** — pure computation libraries with no external dependencies. The reason the software exists.
- **Input/Output** — adapters that bridge outside world and Logic (web framework, CLI, UI).
- **Dependencies** — external systems the software talks to (database, cache, third-party APIs, storage).

Rules:
- Logic folders depend on nothing external. I/O and Dependencies depend on Logic, not the other way around.
- If two Logic folders need shared code, introduce a `Common` folder rather than creating a cycle.
- This is a baseline, not a framework. Resist over-applying it — the wisdom of trade-offs comes first. Hexagonal/Onion/Clean architectures fail when overused; so will this.

## 6. TDD Workflow

For each behavior: write failing test → make it pass → refactor. Commit each cycle.

- Don't write tests after implementation. Tests written after the fact are easy to spot and don't drive design.
- A bug fix starts with a failing test that reproduces the bug.

## 7. Correctness Invariants

Financial software has non-negotiable correctness properties. Encode them in types and tests; don't rely on memory.

- **Money is integer cents.** Never `float64`. Currency stored alongside the amount.
- **Logic layer is deterministic.** No `time.Now()`, no `rand`, no globals — inject as parameters or via `Clock` / `IDGen` interfaces. Tests pass fixed values; production passes wall-clock.
- **Append-only ledger.** Financial records aren't edited; corrections are new offsetting entries. The audit trail *is* the data model.
- **Idempotency on every state-changing op.** Every write carries a request ID or natural unique key, enforced by a Postgres unique constraint.
- **Named domain invariants.** Write them down and test them. For double-entry: `Σ(debits) == Σ(credits)` per transaction; account balance = `Σ(entries)` for that account.

Test: every named invariant has a property-based test. If you can't express it as a property, it isn't an invariant — it's a hope.
