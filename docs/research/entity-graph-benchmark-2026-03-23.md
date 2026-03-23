# Entity Graph Benchmark — 2026-03-23

Issue: `#354`  
Branch: `feat/entity-resolution-graph`

## Goal

Measure the query-time impact of the new entity system on LoCoMo-style multi-entity reasoning, with the same DB, same embedder, same reader model, and only `--entity-graph` toggled.

## Setup

- Binary: `/tmp/cortex-entitygraph-bench-v3`
- Corpus: `/tmp/cortex-locomo-combined-2026-03-22/corpus`
- Fresh DB: `/tmp/cortex-locomo-entitygraph-fresh2-2026-03-23/cortex.db`
- Import command:

```bash
/tmp/cortex-entitygraph-bench-v3 \
  --db /tmp/cortex-locomo-entitygraph-fresh2-2026-03-23/cortex.db \
  import /tmp/cortex-locomo-combined-2026-03-22/corpus \
  --recursive \
  --extract \
  --no-enrich \
  --no-classify
```

- Embeddings:

```bash
/tmp/cortex-entitygraph-bench-v3 \
  --db /tmp/cortex-locomo-entitygraph-fresh2-2026-03-23/cortex.db \
  embed openrouter/text-embedding-3-small
```

- Reader path:
  - `cortex ask`
  - `--mode rrf`
  - `--embed openrouter/text-embedding-3-small`
  - `--model google/gemini-2.5-flash`
  - paired runs with and without `--entity-graph`

## DB State

After fresh import:

- Memories: `764`
- Facts: `4456`
- Canonical entities: `53`
- Entity aliases: `0`
- Facts with `entity_id`: `4456`
- Unresolved entities: `0`

Notes:

- The new speaker heuristic in entity resolution converts transcript-shaped facts like `subject=Session ...`, `predicate=jon (d1` into canonical speaker entities such as `Jon` and `Gina`.
- LoCoMo did not exercise alias merging much in this run because most speaker names are already canonical.

## Slice

The full union of LoCoMo category `3` plus all commonality questions was too slow for a same-turn paired benchmark, so this report uses a smaller targeted slice:

- All explicit commonality questions: `12`
- Category `3` sample: first `2` questions per conversation that contains category `3`, deduped against the commonality slice: `19`
- Total targeted questions: `30`

This is enough to measure the intended behavior without hiding the result behind a long-running eval.

## Results

| Slice | Questions | F1 Off | F1 On | Delta | Avg Latency Off | Avg Latency On |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| All targeted | 30 | 0.0366 | 0.0556 | +0.0190 | 12.020s | 11.211s |
| Category 3 sample | 19 | 0.0127 | 0.0148 | +0.0021 | 11.751s | 10.830s |
| Commonality | 12 | 0.0798 | 0.1240 | +0.0442 | 12.733s | 12.012s |
| `conv-30` commonality | 4 | 0.1429 | 0.2338 | +0.0909 | 9.338s | 11.457s |

Interpretation:

- The entity graph helps most on explicit commonality questions.
- The category-3 hypothetical/inference slice barely moves. Retrieval is not the main bottleneck there; answer synthesis and extraction quality still dominate.
- The commonality lift is real but modest because the current rule extractor still stores many transcript facts in a noisy `session header + speaker line` shape.

## Example Wins

`conv-30`

- `How do Jon and Gina both like to destress?`
  - Off: `not enough evidence`
  - On: `Jon and Gina both like to destress by dancing`
  - F1 delta: `+0.3636`

`conv-42`

- `What movies have both Joanna and Nate seen?`
  - Off: `not enough evidence`
  - On: `Joanna and Nate have both seen "The Lord of the Rings" Trilogy`
  - F1 delta: `+0.2308`

`conv-48`

- `Is Deborah married?`
  - Off: `not enough evidence`
  - On: `Yes, Deborah got married at a beach that is special to her`
  - F1 delta: `+0.1538`

## Example Regressions

`conv-42`

- `What animal do both Nate and Joanna like?`
  - Off: correct `turtles`
  - On: degraded fallback output
  - F1 delta: `-0.2857`

`conv-49`

- `Which type of vacation would Evan prefer with his family, walking tours in metropolitan cities or camping trip in the outdoors?`
  - Off: partially grounded answer
  - On: degraded fallback output
  - F1 delta: `-0.0800`

The failures are not entity-resolution failures. They are mostly reader-path failures:

- Off degraded outputs: `3`
- On degraded outputs: `7`

That means the retrieval gain is being partially masked by the current `ask` synthesis path.

## Retrieval Spot Check

Query: `What do Jon and Gina both have in common?`

Without `--entity-graph`, the top result is the opening `Session 1` memory, followed by generic motivation/support sessions.

With `--entity-graph`, the result set still keeps `Session 1`, but it also injects additional Jon/Gina-linked memories and profile-style candidates earlier, including a direct profile hit from `Session 16`.

That matches the intended behavior: broader entity-linked candidate coverage before answer synthesis.

## Caveats

- This benchmark does not use the full 107-question target union; it uses a transparent targeted slice of 30 questions for runtime reasons.
- The LoCoMo extractor path is still noisy. Many facts remain transcript-shaped, so the entity system is rescuing speaker identity from the predicate/source quote rather than starting from a clean atomic-fact representation.
- Because the benchmark path uses `cortex ask`, reader degradation can dominate the final answer score even when retrieval improved.
- Alias functionality is implemented and tested, but this corpus did not stress it much.

## Bottom Line

The entity graph channel is directionally correct:

- `+0.0442` F1 on the commonality slice
- `+0.0909` F1 on `conv-30` commonality
- only `+0.0021` on sampled category-3 inference questions

What this means for Cortex:

- Canonical entities, profiles, and graph retrieval are worth keeping.
- The next gains will come from better conversation extraction and a more reliable answer reader, not from more entity-graph complexity alone.
