# Architecture Decision Records

Every significant decision logged with rationale. This is the project's institutional memory.

---

## ADR-001: Go as Primary Language
**Date:** 2026-02-16  
**Status:** Accepted  
**Context:** Need single binary distribution, fast compilation, good SQLite bindings.  
**Decision:** Go for MVP. Consider Rust for performance-critical paths later.  
**Rationale:** Lower contributor barrier than Rust. Pure Go SQLite (`modernc.org/sqlite`) means no CGO. Cross-compilation is trivial. Matches Engram's stack.  
**Risk:** Go's NLP ecosystem is weak compared to Python. Extraction quality may suffer for unstructured text. See ADR-003.

## ADR-002: SQLite as Single Storage Backend
**Date:** 2026-02-16  
**Status:** Accepted  
**Context:** Need embedded, zero-config, portable storage with full-text search.  
**Decision:** SQLite + FTS5 for all data (memories, facts, embeddings).  
**Rationale:** Single file = trivially portable. FTS5 is battle-tested BM25. WAL mode handles concurrent reads. Embeddings stored as BLOBs (float32 arrays).  
**Risk:** Embedding similarity search via brute-force BLOB comparison may be slow at >100K entries. Mitigation: consider LanceDB or faiss bindings if needed later.

## ADR-003: Two-Tier Extraction (Local + LLM-Assist)
**Date:** 2026-02-16  
**Status:** Accepted  
**Context:** Local-only NER in Go (`prose` library) is insufficient for relationship extraction, coreference resolution, and complex fact patterns. Mem0 solves this by requiring an LLM (gpt-4.1-nano). We need both options.  
**Decision:** Ship with two extraction tiers from day one:
- **Tier 1 (default):** Rule-based + local NLP. Works for structured input (MEMORY.md, JSON, YAML). Zero dependencies.
- **Tier 2 (opt-in):** LLM-assisted extraction via any OpenAI-compatible API. Works for unstructured text. User provides their own API key/endpoint.

**Rationale:** Being honest about what local extraction can and can't do builds trust. LLM-assist from day one prevents the "local extraction sucks, this tool is useless" failure mode. Supporting ANY provider (not just OpenAI) differentiates us from Mem0.  
**Risk:** If local extraction is too weak, everyone just uses LLM mode and our "zero-dep" pitch weakens. Mitigation: Invest in structured-text extraction quality; accept that raw conversation import is an LLM-assist use case.

## ADR-004: Import-First Architecture
**Date:** 2026-02-16  
**Status:** Accepted  
**Context:** Every existing memory tool assumes users start fresh. Real users have months of accumulated context.  
**Decision:** The import engine is the primary entry point. Every feature assumes pre-existing data.  
**Rationale:** This is our #1 differentiator. Solving migration is solving adoption.

## ADR-005: Cortex Memory Format (CMF)
**Date:** 2026-02-16  
**Status:** Proposed  
**Context:** No standard interchange format exists for AI agent memory.  
**Decision:** Define a clean JSON format called CMF. Don't call it a "standard" — call it "Cortex Memory Format." If adoption is large enough, it becomes a standard organically.  
**Rationale:** XKCD 927 — creating standards from nothing fails. But if 10K+ users export to CMF, tools will support it because users demand it.

## ADR-006: Two Binary Distribution
**Date:** 2026-02-16  
**Status:** Proposed  
**Context:** ONNX embedding model adds ~80MB to binary size. Some users want minimal download.  
**Decision:** Ship two binaries:
- `cortex-lite` (~10MB) — BM25 search only, no embeddings
- `cortex` (~90MB) — Full semantic search with bundled ONNX model

**Rationale:** Users choose their tradeoff. README defaults to full binary. Lite version for CI/CD, Docker, or constrained environments.

## ADR-007: "Switzerland" Positioning
**Date:** 2026-02-16  
**Status:** Accepted  
**Context:** Mem0 has $24M funding, 25K GitHub stars, and just launched an OpenClaw plugin. We cannot compete head-on.  
**Decision:** Position as "the tool you use before and between memory providers." Import into Cortex, export to anything. Not "better Mem0" — a different tool entirely.  
**Rationale:** Competing on features against a $24M company is suicide. Competing on portability and openness is a game they can't play because their business model requires lock-in.
