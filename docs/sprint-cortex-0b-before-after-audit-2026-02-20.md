# Cortex Sprint 0-B — Before/After Extraction Audit

Date: 2026-02-20

## Goal
Measure impact of 0-B extractor root fixes on noisy auto-capture facts.

## Method
- Sample: latest **400** memories where `source_file LIKE '%auto-capture%'` from `~/.cortex/cortex.db`
- Compared two code states on the **same sample**:
  - **Before 0-B**: commit `7a459a7` (post-0-A, pre-0-B)
  - **After 0-B**: current HEAD (`38514ce` + `6ed7c77`)
- Metric runner: temporary extractor benchmark script calling `extract.NewPipeline().Extract(...)`

## Results

| Metric | Before | After | Delta |
|---|---:|---:|---:|
| facts_total | 784 | 372 | **-52.55%** |
| kv_facts | 742 | 344 | **-53.64%** |
| kv_ratio | 94.64% | 92.47% | -2.17 pp |
| noisy_kv_predicates | 5 | 0 | **-100%** |

### Top predicates (before)
Included noisy items like `current time`.

### Top predicates (after)
Noisy scaffold predicates removed; remaining top predicates are mostly domain content (`url`, `amount`, etc.).

## Live DB Context (pre-reimport)
Current production DB still contains historical noise from prior extraction runs:
- total_kv: 2,836,650
- noisy_kv: 296,641
- noisy_share_kv: 10.46%

This is expected until re-extraction/reimport with the new rules.

## Conclusion
0-B fixes materially reduce extraction volume and eliminate measured scaffold-noise predicates on the audit sample. Next step is controlled reprocessing to realize gains in the live DB.
