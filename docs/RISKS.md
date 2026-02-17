# Known Risks & Honest Assessments

Acknowledging what's hard builds trust with contributors. These are the real challenges.

---

## Risk 1: Go NLP Ecosystem Weakness
**Severity:** High  
**Impact:** Extraction quality for unstructured text  
**Details:** Go's NLP libraries (`prose`, `go-nlp`) cannot do coreference resolution, relationship extraction, or complex fact patterns. Python's spaCy/transformers ecosystem is a decade ahead.  
**Mitigation:** Two-tier extraction (ADR-003). Local extraction targets structured input. LLM-assist handles unstructured. Honest about limitations in docs.  
**Future:** Consider ONNX models for local extraction (GLiNER for NER, T5-small for structured extraction) in v1.1+.

## Risk 2: Competing with Funded Incumbents
**Severity:** High  
**Impact:** Adoption, mindshare  
**Details:** Mem0 ($24M Series A, YC, 25K stars) just launched an OpenClaw plugin. Zep and Letta also have funding and teams.  
**Mitigation:** Different positioning (ADR-007). We solve import/export/observability — problems they don't address. Never position as "better X."

## Risk 3: Community Building
**Severity:** High  
**Impact:** Long-term viability  
**Details:** OSS projects die in silence, not technical failure. GitHub stars ≠ contributors. Need sustained effort: regular releases, responsive issue triage, content marketing, Discord.  
**Mitigation:** Require 6-12 month commitment. HN launch, Reddit presence (r/AI_Agents, r/ClaudeAI, r/LocalLLM), blog posts, demo videos. Tag good-first-issues aggressively.

## Risk 4: ONNX Cross-Platform Compilation
**Severity:** Medium  
**Impact:** Distribution on ARM Mac, Linux ARM, Windows  
**Details:** ONNX Runtime Go bindings (`onnxruntime-go`) require platform-specific shared libraries. Cross-compilation is non-trivial.  
**Mitigation:** CI matrix testing across platforms. Two-binary strategy (ADR-006) lets users fall back to lite version. Pre-built binaries via GitHub releases.

## Risk 5: Search Quality Tuning
**Severity:** Medium  
**Impact:** User experience  
**Details:** Hybrid search (BM25 + embeddings with reciprocal rank fusion) has parameters that significantly affect quality. No right answer — needs iteration with real data.  
**Mitigation:** Dogfood on our own OpenClaw memory. Configurable fusion weights. Benchmark against Mem0's search quality on same data.

## Risk 6: Deduplication Accuracy
**Severity:** Medium  
**Impact:** Memory quality  
**Details:** Same fact in different words across sources. Embedding similarity helps but has false positives. Temporal dimension (is it an update or a conflict?) adds complexity.  
**Mitigation:** Conservative dedup (flag, don't auto-merge). `cortex conflicts` surfaces ambiguous cases. User confirms.

## Risk 7: Scale with Large Imports
**Severity:** Low-Medium  
**Impact:** Import UX for power users  
**Details:** 5,000-file Obsidian vault import — time and DB performance at 50K+ entries unknown.  
**Mitigation:** Benchmark early. SQLite handles millions of rows. FTS5 indexing may need batch optimization. Progress bar for large imports.
