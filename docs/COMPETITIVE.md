# Competitive Analysis

Updated: 2026-02-16

---

## The Market

AI agent memory is a rapidly growing space. The core problem is well-understood: LLMs are stateless, agents need persistence. The market is splitting into:

1. **Full platforms** (Letta) — Agent framework with memory built in
2. **Memory-as-a-service** (Mem0, Zep) — SaaS memory with APIs
3. **Lightweight local tools** (Engram, custom MEMORY.md) — File/DB-based memory

Cortex occupies a unique position: **infrastructure-layer memory that's local-first, import-first, and platform-agnostic.**

---

## Detailed Competitor Analysis

### Mem0 (mem0.ai)
- **Funding:** $24M Series A (YC W24)
- **GitHub:** ~25K stars
- **Architecture:** Vector store + LLM extraction (requires gpt-4.1-nano or equivalent)
- **Pricing:** Free tier → $19/mo Starter → $249/mo Pro
- **OpenClaw integration:** Yes — plugin launched Feb 2026
- **Strengths:** Most mature SaaS, good API design, batch operations, multi-scope memory
- **Weaknesses:**
  - Requires an LLM for all memory operations (even storing a simple preference)
  - Self-hosting is poorly documented and complex
  - No import from existing memory sources
  - No observability (can't see what's stored, what's stale)
  - Lock-in: no meaningful export
  - Python-first (JS SDK secondary)

### Zep (getzep.com)
- **Architecture:** Graph-based (Graphiti), PostgreSQL backend
- **Strengths:** Sophisticated knowledge graph, good technical blog, academic rigor
- **Weaknesses:**
  - Requires PostgreSQL (not embedded)
  - Cloud-first; community edition is limited
  - SaaS not production-ready for small teams
  - No import story
  - The academic paper is widely criticized as "marketing disguised with equations"

### Letta (letta.com, formerly MemGPT)
- **Architecture:** Full agent framework with memory as a subsystem
- **Strengths:** Truly open source, vibrant Discord community, Desktop UI
- **Weaknesses:**
  - You adopt the ENTIRE framework or nothing
  - Memory quality depends heavily on the LLM used
  - Not production-ready for mission-critical apps
  - Can't bolt onto existing agent setups (OpenClaw, custom, etc.)

### Engram
- **Architecture:** Go binary, SQLite + FTS5, HTTP API
- **Strengths:** Zero deps, lightweight, self-hosted, simple
- **Weaknesses:**
  - Keyword search only (no semantic)
  - No import engine
  - No observability
  - Very early, small community

### Custom Solutions (MEMORY.md, ai-agent-memory-system, etc.)
- **Architecture:** File-based, loaded into context window
- **Strengths:** Simple, human-readable, no dependencies
- **Weaknesses:**
  - Context compaction destroys them
  - No search beyond what's in the context window
  - No deduplication, conflict detection, or staleness tracking
  - Not portable between platforms

---

## Our Positioning: The Switzerland of Agent Memory

We are NOT:
- "Better Mem0" (we'd lose that fight)
- "Another memory SaaS" (no cloud, no pricing tiers)
- "A full agent framework" (just the memory layer)

We ARE:
- **The tool you use BEFORE picking a memory provider** — organize and understand what you have
- **The tool you use TO SWITCH between providers** — import from A, export to B
- **The memory layer for people who want to own their data** — local, offline, no API keys
- **The observability layer nobody else provides** — see what your agent knows

---

## Feature Comparison

| Feature | Cortex | Mem0 | Zep | Letta | Engram |
|---------|--------|------|-----|-------|--------|
| Import existing memory | ✅ Core feature | ❌ | ❌ | ❌ | ❌ |
| Zero LLM dependency | ✅ (local default) | ❌ | ❌ | ❌ | ✅ |
| LLM-assist mode | ✅ Any provider | 🟡 GPT only | ❌ | Depends | ❌ |
| Observability | ✅ | ❌ | ❌ | Basic | ❌ |
| Self-hosted | ✅ Single binary | 🟡 Complex | 🟡 Postgres | 🟡 Framework | ✅ |
| Semantic search | ✅ Local ONNX | ✅ Cloud | ✅ Cloud | ✅ | ❌ |
| Works offline | ✅ | ❌ | ❌ | ❌ | ✅ |
| Export/portability | ✅ | ❌ | ❌ | ❌ | 🟡 |
| Cross-platform | ✅ Any agent | 🟡 Python-first | 🟡 | ❌ Letta only | 🟡 |
