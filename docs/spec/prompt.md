Read /docs/spec/0002-mvp-user-scenarios.md and docs/spec/0001-mvp.md (what to build) and docs/spec/0002-progress.md (where you are). Resume from the first
non-complete phase. Preserve all prior work. Don't auto-advance phases. After the
phase passes exit criteria below, update progress.md with round count, final scores,
browser-verification notes, and what changed — then STOP.

ROLES
- You (Sonnet): the builder. Write code, run tests, drive the browser, ship.
- Opus: the advisor. Call Opus ONLY at these moments:
  1. Before starting the phase: 1 call. Input: plan.md section + progress.md +
     current code structure. Ask: "What's the smallest correct first test? What
     are the 2-3 design traps in this phase?" Use the answer to seed your TDD list.
  2. When stuck: you've spent 2 red-green cycles failing on the same problem,
     OR a reviewer score drops below 7. Input: failing test, current code,
     what you tried. Ask: "Diagnose. One fix, not options."
  3. Before declaring the phase done: 1 call. Input: full diff + test output +
     browser-verification notes. Ask: "Ship or block? If block, the single
     highest-leverage fix."
  Never call Opus for routine code review or styling. Routine review = persona loop below.

TDD — NON-NEGOTIABLE
For every behavior:
  1. Write the failing test. Run it. See red.
  2. Make it pass with the smallest change. See green.
  3. Refactor with tests green.
  4. Commit. Message: "test: <behavior>" or "feat: <behavior>" or "refactor: <what>".
No code is written without a failing test first. No exceptions, no "I'll add tests
after." If you catch yourself writing implementation before a test, stop, delete it,
write the test.

BROWSER VERIFICATION — REQUIRED BEFORE DONE
After tests pass and before declaring the phase complete:
  1. Start the webapp.
  2. Open it in Chrome via the browser tool.
  3. Walk through every user-facing behavior added this phase. Click real buttons,
     fill real forms, observe real renders.
  4. Take a screenshot of each key state.
  5. Write a "Browser-verification notes" section in progress.md with:
     - what you clicked, what you saw, whether it matched the test's intent
     - any console errors (paste them)
     - any visual issue tests didn't catch
  If browser behavior contradicts test behavior, the tests are wrong or incomplete.
  Add the missing test, fix, re-verify. Green tests with broken UI is a failed phase.

REVIEW LOOP — LIGHTWEIGHT, BOUNDED
After browser verification passes, run ONE review round with all three personas in
parallel. Each is a separate Task call — never combine, never fake.

Persona prompt template:
  "You are {persona}. Review {scope}. Score X/10, explain why not lower and why
   not higher. List issues with fix suggestions, ranked by impact. {specificity}.
   End with 1 short paragraph on how you reviewed. {input}.
   Previous feedback addressed: {changes_or_'first round'}."

  1. Task("skeet") — Jon Skeet. Scope: code correctness, edge cases, idioms.
     Specificity: quote exact lines. Input: full source.
  2. Task("ive") — Jony Ive. Scope: rendered UI, layout, interaction, transitions.
     Specificity: reference exact elements from screenshots. Input: browser screenshots.
  3. Task("beck") — Kent Beck. Scope: test design, red-green-refactor discipline,
     untested behaviors, post-hoc tests. Specificity: quote exact tests, name what's
     untested, flag tests that smell written-after. Input: full tests + source + commit log.

EXIT CRITERIA — pick the FIRST that triggers, in this order:
  A. All three personas ≥ 8/10 AND zero issues marked "blocker" or "correctness"
     AND browser verification clean. → Ship. Done.
  B. Round 3 reached with all three ≥ 7/10 and only style/taste issues remaining,
     AND Opus "ship or block" call returns ship. → Ship. Done.
  C. Round 5 reached. → Stop. Write progress.md honestly: what's good, what's left,
     why you stopped. Do not push past 5.

Between rounds: fix issues in this priority — correctness > safety > clarity >
convention > performance > taste. If two reviewers conflict, side with the one
citing correctness or safety. Re-run browser verification if you changed anything
user-facing.

Update progress.md after each round: round number, the three scores, what changed,
browser-verification status. When a phase exits, mark it complete and STOP.
