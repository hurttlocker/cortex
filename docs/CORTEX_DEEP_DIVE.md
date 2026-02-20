# Cortex: Import-First Agent Memory You Actually Own (Deep Dive)

> Your agent memory should be portable, inspectable, and durable. Cortex is a single-binary, SQLite-backed memory layer with hybrid retrieval, reasoning, observability, and release-grade ops guardrails.

---

## Last Updated

- **Updated:** 2026-02-20 (ET)
- **Repo checked:** `hurttlocker/cortex` @ `23e55ba` (main)
- **Latest stable release:** `v0.3.5`
- **Current dev fallback version in source:** `0.3.5-dev`

---

## Executive Snapshot

Cortex has moved from “feature buildout” into “operational hardening + release discipline.”

### What is true right now
- Stable release is live: **v0.3.5**
- External hostile audit on immutable RC target `v0.3.5-rc2` returned **GO** with no reproducible product findings
- Visualizer v1 closure sequence is complete (#99 and #104 closed)
- Ops hardening lanes are shipped and stable-tagged
- Current open issue count: **0** (at time of refresh)

### Most recent merged wave (top commits)
1. `23e55ba` — merge stable release prep docs/changelog for `v0.3.5` (#128)
2. `8f7aa9d` — merge rc2 audit packet + roadmap sync (#127)
3. `1a7482e` — merge release checklist portability fix (no hard `rg` dependency) (#126)
4. `e77aa5c` — merge visualizer traversal-style output path guard (#125)
5. `3a0c8db` — merge closure for external findings F-01..F-05 (#124)

---

## Where We Are in the Roadmap (Abstraction-Level View)

## Layer 1 — Core Product (DONE)
Import, extraction, hybrid search, MCP, reasoning, and observability are delivered and in production use.

## Layer 2 — Reliability + Release Readiness (DONE)
The v0.3.4 → v0.3.5 cycle delivered immutable-target auditing, release artifact verification, hostile fresh-user break testing, and explicit go/no-go handoff docs.

## Layer 3 — Ops Maturity (DONE)
High-impact ops controls are in place and exercised in a successful stable promotion:
- `cortex optimize` maintenance path
- SLO snapshot artifact generation
- CI doc drift guardrails
- Release checklist gating before publish
- Scheduled canary + artifact history
- Thresholded canary regression signaling
- Deterministic connectivity smoke gate
- One-command audit preflight evidence generation
- Adversarial break harness coverage for common failure classes

## Layer 4 — Next Expansion (NEXT)
Natural next targets are trend-aware regression intelligence, codex-in-production tuning from telemetry, and richer operator surfaces for release/audit observability.

---

## Release State

### Published tags (latest first)
`v0.3.5`, `v0.3.5-rc2`, `v0.3.5-rc1`, `v0.3.4`, `v0.3.4-rc1`, `v0.3.3`, ...

### Stable release
- **v0.3.5** (non-draft, non-prerelease)
- Cross-platform artifacts + checksums published
- Promotion preceded by external hostile-audit **GO** on immutable `v0.3.5-rc2`

### Release process maturity (current)
- CI build/test/vet gates
- PR autofix gate
- Release workflow gate: `scripts/release_checklist.sh`
- Docs drift gate: `scripts/ci_release_guard.sh`
- Runtime connectivity gate: `scripts/connectivity_smoke.sh`
- Auditor evidence gate: `scripts/audit_preflight.sh`
- Adversarial gate: `scripts/audit_break_harness.sh`

---

## Current System Stats (Live Snapshot)

From `cortex stats --json` on 2026-02-20:

- Memories: **2,448**
- Facts: **2,705,925**
- Sources: **664**
- Storage: **1,482,072,064 bytes (~1.48 GB)**
- Avg confidence: **0.867**
- Alerts:
  - `db_size_notice` (>1.0GB)
  - `fact_growth_spike`
  - `memory_growth_spike`

Interpretation: the system is healthy but in high-growth mode; ops guardrails are correctly surfacing pressure signals.

---

## What Changed Since the Prior Deep Dive (v0.3.3-era)

The prior document anchored to `v0.3.3` / `968954e` and emphasized audit hardening.

Now, beyond that baseline, Cortex added:

1. **Stable promotion to v0.3.4 and then v0.3.5** via immutable-target audit discipline
2. **Reliability lane completion** (optimize, SLO snapshot/canary, checklist/doc drift guards)
3. **Deterministic connectivity gate** for release/runtime path validation
4. **One-command audit preflight** that emits markdown + per-step logs
5. **Visualizer v1 closure** including retrieval-debug deltas and bounded provenance explorer
6. **External hostile audit GO on `v0.3.5-rc2`** with no reproducible product findings before stable promotion

---

## Ops & Reliability Toolchain (Now Available)

### 1) Built-in maintenance
```bash
cortex optimize
cortex optimize --check-only
cortex optimize --vacuum-only
cortex optimize --analyze-only
```

### 2) SLO snapshot artifact
```bash
scripts/slo_snapshot.sh \
  --warn-stats-ms 3000 --warn-search-ms 5000 --warn-conflicts-ms 5000 \
  --fail-stats-ms 7000 --fail-search-ms 10000 --fail-conflicts-ms 12000 \
  --output /tmp/slo.json --markdown /tmp/slo.md
```

### 3) CI canary (daily + manual)
- Workflow: `.github/workflows/slo-canary.yml`
- Uploads JSON + markdown artifacts per run
- Uses threshold bands for gating

### 4) CI/governance guards
```bash
scripts/ci_release_guard.sh
scripts/release_checklist.sh --tag vX.Y.Z
```

---

## Architecture (Current Practical View)

```mermaid
flowchart LR
  A[Import / Capture] --> B[(SQLite + FTS5)]
  B --> C[Hybrid Retrieval\nBM25 + Semantic + WSF]
  B --> D[Observability\nStats/Stale/Conflicts]
  C --> E[Reason Engine\nOne-shot + Recursive]
  D --> F[Ops Controls\noptimize + SLO snapshots]
  F --> G[CI Canary + Release Gates]
```

Cortex is now not just a memory engine — it is a memory engine with release-grade operational controls.

---

## Known Gaps / Honest Next Step

The platform is now strong on correctness and operations. The next strategic unlock is **trend-aware performance regression intelligence** (relative change over time, not only static thresholds), followed by richer UI/analysis surfaces.

---

## Recommended Next Roadmap Slice (Post v0.3.5 promotion)

1. **Codex real-work tuning loop:** continue production dogfooding and adjust thresholds/prompts only from measured regressions.
2. **SLO trend intelligence:** compare latest canary against historical baseline for relative regressions, not only static thresholds.
3. **Gate observability dashboard:** unify release checks, canary trends, and audit preflight history into one operator view.
4. **Cross-platform hostile soak testing:** extend destructive coverage beyond darwin artifact path into linux/windows + longer contention windows.

---

## Bottom Line

Cortex is past the point of “promising memory tool” and into “operational memory platform.”

- Stable release quality: ✅
- Audit discipline: ✅
- Ops guardrails: ✅
- Continuous canarying with thresholds: ✅

The next gains come from trend intelligence and decision UX, not foundational reliability.
