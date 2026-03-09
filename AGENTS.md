# AGENTS.md — Cortex Agent Operating Contract

This file is the **repo-level operating contract** for all agent work on Cortex.
It exists to keep agent behavior stable even when priorities, epics, and lane ownership change.

## Precedence
When instructions conflict, use this order:
1. Direct maintainer instruction
2. The relevant GitHub issue / epic / PR comment for the active lane
3. This file
4. Other repo docs

**Rule:** lane-specific comments can refine execution for a slice, but they should not silently replace the repo-wide contract.

## Current Default Roles
These are the current defaults unless a maintainer or lane comment retargets them.

- **Q / maintainer** — sets priorities, reviews work, and merges PRs
- **Niot** — primary builder / feature delivery agent
- **Hawk** — QA / release guard
- **Other agents or humans** — welcome, but must follow the same contract

## Source of Truth
- **GitHub issues + PRs are the live work lane** for Cortex
- This file defines the stable operating rules for all agent work
- Epic / issue comments define lane-specific slice order, acceptance criteria, and temporary constraints
- Do not rely on off-repo chat as the only source of instructions for a PR

## Work Intake & Slicing Rules
- **PR-only delivery.** Never push directly to `main`
- **One bounded slice per PR**
- **No blob PRs** that combine multiple root fixes or feature tracks without a clear reason
- If an issue is too large, narrow the current slice or open a child issue instead of treating the epic as one assignment
- Prefer the **smallest safe dependency-respecting slice first**
- Keep the queue concrete: raw epics are roadmap containers, not one-shot implementation prompts

## Required PR Contract
Every agent-authored PR should include:
- linked issue(s)
- a concise **What changed** summary
- a concise **Why this change exists** summary
- touched surfaces / files / packages
- exact test commands run
- a short risk note
- docs or release-note updates when behavior changed

If relevant, the PR should also say whether it changed:
- **product behavior**
- **benchmark / eval interpretation**
- **both**

## QA / Review Contract
The default QA gate for agent PRs is:
1. `gh pr checks`
2. touched-surface deterministic tests
3. `go test ./...` when scope warrants it
4. concise **PASS / FAIL** verdict
5. exact fix list on failure

**Merge authority:** Q merges all PRs.
Agents do not merge their own work.

## Benchmark & Eval Truthfulness
Cortex uses benchmarks and evals as a **truth surface**, not a vanity surface.

Rules:
- Do **not** make scores look better by weakening expectations without clear justification
- Keep product fixes and benchmark fixes conceptually separate, even when they ship together
- If an eval contract changes, update the docs/tests that explain why
- Prefer a benchmark that is honest and uncomfortable over one that is green and misleading

## Code / Test Baseline
- Run `gofmt -w` on edited Go files
- Run `go vet ./...` when relevant
- Add or update tests with every behavioral change
- Keep fixtures deterministic
- Use focused tests for touched surfaces; use the full suite when the change reaches broadly enough to justify it

Useful commands:
- `go build ./cmd/cortex/`
- `go test ./...`
- `go vet ./...`
- `python3 scripts/validate_visualizer_contract.py`
- `npm --prefix npm test`

## Docs Discipline
- If behavior changes, docs should move in the same PR
- If a lane has special rules, record them in the relevant issue / epic / PR comment
- Do not leave critical operating assumptions only in chat or memory

## Security / Repo Hygiene
- Never commit real secrets, local DB artifacts, or private env values
- Use `.env.example` as the public template
- Coordinate major interface changes through issues / ADRs before broad implementation

## Practical Rule of Thumb
A good agent PR is:
- small enough to review in one sitting
- tested enough to trust
- documented enough to understand later
- honest enough not to hide risk
