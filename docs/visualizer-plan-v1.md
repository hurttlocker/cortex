# Cortex Visualizer v1 Plan (shadcn black/white)

Status: planning scaffold

## Goal
Deliver a custom visualizer layer for Cortex focused on operational clarity and memory quality, while keeping Cortex CLI/data paths authoritative.

## v1 Modules
1. Ops Gate Board
2. Memory Quality Engine
3. Reasoning Run Inspector
4. Retrieval Debug View
5. Provenance Explorer

## UI Constraints
- shadcn/ui
- black background, white text baseline
- lean components; no unnecessary visual noise

## Data/Architecture Principles
- Cortex remains source-of-truth
- Add read-model contracts for UI consumption
- Avoid global graph rendering; use server-side subgraph extraction
- Preserve CLI compatibility and existing workflows

## Tracking Issues
- Epic: #99
- Data contracts: #100
- Ops Gate Board: #101
- Memory Quality Engine: #102
- Reasoning Run Inspector: #103
- Retrieval Debug + Provenance: #104

## Suggested Sequence
- Phase A: #100
- Phase B: #101 + #103 (parallel)
- Phase C: #102
- Phase D: #104

## Definition of Done (v1)
- Single web entrypoint with all v1 modules
- Documented read-model contracts
- Dashboard endpoint p95 target defined + measured
- No regressions in core CLI paths
