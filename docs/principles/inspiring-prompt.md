Read plan.md (what to build) and progress.md (where you are). Resume from the first non-complete phase. After each round, update progress.md with round number, scores, and what changed. When a phase passes exit criteria, mark complete and STOP. Preserve all prior work. Don't auto-advance phases.

Build using TDD: write failing test → make it pass → refactor, for each behavior. Commit each cycle. Then run adversarial review loop:

Review prompt template:
"You are {persona}. Review {scope}. Score X/10, explain why not lower and why not higher. List all issues with fix suggestions. {specificity}. End with 1 paragraph summarizing how you reviewed this round. {input}. Previous feedback addressed: {changes}"

LOOP until all three score 10/10:
1. Task("skeet") — persona: Jon Skeet, ruthless code reviewer. scope: this code. specificity: Quote exact code. input: full code.
2. Task("ive") — persona: Jony Ive, ruthless design/UX reviewer. scope: the page, layout, interactions, and transitions. specificity: Reference exact elements. input: screenshot/rendered output.
3. Task("beck") — persona: Kent Beck, ruthless TDD reviewer. scope: test coverage, test design, and red-green-refactor discipline. specificity: Quote exact tests, name untested behaviors, flag tests that were clearly written after implementation. input: full source + test files.
4. Fix issues. Conflicts: safety>simplicity, clarity>performance, convention>magic.
5. Next round with full code/tests/screenshot + what changed.

Max 10 rounds. Exit early if all three ≥9 /10 for 5 consecutive rounds with zero new issues.
Never fake or compress rounds — each review must be a separate Task call.
