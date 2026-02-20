# Cortex External Audit Guide

> **Version:** v0.3.2 (post `f0eea09`)
> **Date:** 2026-02-19
> **Purpose:** Break everything. Report what survives.

Welcome, auditor. This guide walks you through a full functional test of Cortex — from zero to recursive AI reasoning. Every section is designed to be run in order. If something fails, document it and keep going.

**Time estimate:** 45-60 minutes for full audit.

---

## 0. Prerequisites

| Requirement | Check |
|---|---|
| Go 1.22+ | `go version` |
| Ollama (optional, for semantic search) | `ollama --version` |
| OpenRouter API key (optional, for `reason`) | `echo $OPENROUTER_API_KEY` |
| A clean temp directory | `export CORTEX_TEST_DIR=$(mktemp -d)` |

```bash
# Set up clean environment
export CORTEX_TEST_DIR=$(mktemp -d)
export CORTEX_DB="$CORTEX_TEST_DIR/cortex.db"
export HOME_BACKUP="$HOME"
```

---

## 1. Installation (3 ways — test all)

### 1a. Binary install
```bash
# Pick your platform:
# macOS ARM: cortex-darwin-arm64.tar.gz
# macOS Intel: cortex-darwin-amd64.tar.gz
# Linux x86: cortex-linux-amd64.tar.gz
curl -sSL https://github.com/hurttlocker/cortex/releases/latest/download/cortex-$(uname -s | tr A-Z a-z)-$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/').tar.gz | tar xz
./cortex version
# ✅ PASS: prints "cortex 0.3.3" (or latest)
# ❌ FAIL: missing binary, wrong arch, permission denied
```

### 1b. Go install
```bash
go install github.com/hurttlocker/cortex/cmd/cortex@latest
cortex version
# ✅ PASS: prints version
```

### 1c. Build from source
```bash
git clone https://github.com/hurttlocker/cortex.git "$CORTEX_TEST_DIR/src"
cd "$CORTEX_TEST_DIR/src"
go build -o "$CORTEX_TEST_DIR/cortex" ./cmd/cortex/
"$CORTEX_TEST_DIR/cortex" version
# ✅ PASS: builds clean, prints version
# ❌ FAIL: compile errors, missing deps
```

---

## 2. Import & Basic Operations

### 2a. Create test data
```bash
mkdir -p "$CORTEX_TEST_DIR/notes"

cat > "$CORTEX_TEST_DIR/notes/project.md" << 'EOF'
# Project Alpha

## Decisions
- 2026-01-15: Chose PostgreSQL over MySQL for the main database
- 2026-01-20: Switched from REST to GraphQL for the API layer
- 2026-02-01: Adopted Kubernetes for container orchestration

## Team
- Alice: Backend lead, prefers Go
- Bob: Frontend, React specialist
- Carol: DevOps, manages CI/CD pipeline

## Architecture
The system uses a microservices architecture with 5 core services:
1. Auth service (Go)
2. API gateway (Node.js)
3. Data pipeline (Python)
4. Search service (Rust)
5. Notification service (Go)
EOF

cat > "$CORTEX_TEST_DIR/notes/meeting.md" << 'EOF'
# Meeting Notes — Feb 10, 2026

## Attendees
Alice, Bob, Carol, Dave

## Discussion
- Alice raised concerns about PostgreSQL performance at scale
- Bob proposed switching the frontend to Svelte (rejected — too risky mid-project)
- Carol reported 99.7% uptime for January
- Dave suggested hiring a dedicated security engineer

## Action Items
- [ ] Alice: Run PostgreSQL load tests by Feb 15
- [ ] Bob: Prototype dashboard redesign
- [ ] Carol: Set up staging environment on new K8s cluster
- [ ] Dave: Draft security engineer job posting
EOF

cat > "$CORTEX_TEST_DIR/notes/config.yaml" << 'EOF'
database:
  host: db.internal.company.com
  port: 5432
  name: alpha_production
  max_connections: 100

api:
  rate_limit: 1000
  timeout_ms: 5000
  graphql_depth_limit: 10

monitoring:
  prometheus_port: 9090
  alert_email: ops@company.com
EOF

cat > "$CORTEX_TEST_DIR/notes/decisions.json" << 'EOF'
[
  {"date": "2026-01-15", "decision": "Use PostgreSQL", "reason": "Better JSON support", "author": "Alice"},
  {"date": "2026-01-20", "decision": "Switch to GraphQL", "reason": "Reduce over-fetching", "author": "Bob"},
  {"date": "2026-02-01", "decision": "Adopt Kubernetes", "reason": "Scaling requirements", "author": "Carol"}
]
EOF

echo "Created 4 test files (md, yaml, json)"
```

### 2b. Import single file
```bash
cortex import "$CORTEX_TEST_DIR/notes/project.md"
# ✅ PASS: "Imported 1 memory from..."
# ❌ FAIL: error, crash, or silent failure
```

### 2c. Import with fact extraction
```bash
cortex import "$CORTEX_TEST_DIR/notes/meeting.md" --extract
# ✅ PASS: "Imported 1 memory... extracted N facts"
# ❌ FAIL: extraction crashes, zero facts from obvious data
```

### 2d. Import directory recursively
```bash
cortex import "$CORTEX_TEST_DIR/notes/" --recursive --extract
# ✅ PASS: imports remaining files (config.yaml, decisions.json)
# Note: should skip already-imported files (dedup check)
# ❌ FAIL: re-imports duplicates, crashes on yaml/json
```

### 2e. Import with class tagging
```bash
cortex import "$CORTEX_TEST_DIR/notes/decisions.json" --class decision --extract
# ✅ PASS: imported with class "decision"
# ❌ FAIL: class not stored, error on flag
```

### 2f. Import with metadata
```bash
cortex import "$CORTEX_TEST_DIR/notes/project.md" --metadata '{"agent_id":"auditor","channel":"test"}'
# ✅ PASS: metadata stored
# ❌ FAIL: JSON parsing error, metadata lost
```

### 2g. Stats check
```bash
cortex stats
# ✅ PASS: shows memories (≥4), facts (≥5), sources (≥4)
# Record the exact numbers: memories=___ facts=___ sources=___
```

---

## 3. Search

### 3a. Keyword search (BM25)
```bash
cortex search "PostgreSQL database" --mode keyword --limit 5
# ✅ PASS: finds project.md and meeting.md content, scores > 0
# ❌ FAIL: no results, or irrelevant results ranked first
```

### 3b. Semantic search (requires ollama + embeddings)
```bash
# Skip if no ollama
cortex embed ollama/nomic-embed-text
cortex search "database performance concerns" --mode semantic --embed ollama/nomic-embed-text --limit 5
# ✅ PASS: finds meeting.md (Alice's PostgreSQL concern) — conceptual match
# ❌ FAIL: no results, or keyword-only matches
```

### 3c. Hybrid search
```bash
cortex search "team roles and responsibilities" --mode hybrid --embed ollama/nomic-embed-text --limit 5
# ✅ PASS: finds project.md team section, score > 0.5
# ❌ FAIL: lower quality than keyword or semantic alone
```

### 3d. Search with class filter
```bash
cortex search "PostgreSQL" --class decision --limit 5
# ✅ PASS: only returns decision-class memories
# ❌ FAIL: ignores class filter, returns all
```

### 3e. Search with metadata filter
```bash
cortex search "project" --agent auditor --limit 5
# ✅ PASS: only returns memories with agent_id=auditor
# ❌ FAIL: ignores agent filter
```

### 3f. Search with explain
```bash
cortex search "Kubernetes" --explain --limit 3
# ✅ PASS: shows provenance, confidence, rank factors for each result
# ❌ FAIL: --explain flag ignored or crashes
```

### 3g. Search JSON output
```bash
cortex search "PostgreSQL" --json | python3 -c "import sys,json; d=json.load(sys.stdin); print(f'Results: {len(d)}'); assert len(d) > 0"
# ✅ PASS: valid JSON array with results
# ❌ FAIL: invalid JSON, empty array
```

---

## 4. Facts & Conflicts

### 4a. List facts
```bash
cortex list --facts --limit 20
# ✅ PASS: shows extracted facts with subjects and predicates
# ❌ FAIL: no facts, or all facts have empty subjects
```

### 4b. Check conflicts
```bash
cortex conflicts
# ✅ PASS: finds conflict (PostgreSQL chosen vs concerns raised)
# If no conflicts: that's valid too for this small dataset
# ❌ FAIL: crashes, hangs, or takes > 30s on small dataset
```

### 4c. Resolve conflicts (dry run)
```bash
cortex conflicts --resolve last-write-wins --dry-run
# ✅ PASS: shows what would be resolved without changing data
# ❌ FAIL: actually modifies data on dry-run, crashes
```

### 4d. Supersede a fact
```bash
# Get two fact IDs
FACT1=$(cortex list --facts --json --limit 1 | python3 -c "import sys,json; row=json.load(sys.stdin)[0]; print(row.get('id', row.get('ID')))" )
FACT2=$(cortex list --facts --json --limit 2 | python3 -c "import sys,json; row=json.load(sys.stdin)[1]; print(row.get('id', row.get('ID')))" )
cortex supersede "$FACT1" --by "$FACT2" --reason "Auditor test"
# ✅ PASS: fact marked as superseded
# ❌ FAIL: error, or fact not actually marked
```

### 4e. Verify superseded facts hidden by default
```bash
cortex list --facts --limit 50 | grep -c "superseded"
# ✅ PASS: superseded fact NOT in default output
cortex list --facts --include-superseded --limit 50
# ✅ PASS: superseded fact IS visible with flag
```

---

## 5. Memory Lifecycle

### 5a. Stale facts
```bash
cortex stale --days 1
# ✅ PASS: shows facts not reinforced in the last day, with confidence scores
# Note: --days 0 may return empty if all facts were just created
# ❌ FAIL: crashes or hangs
```

### 5b. Reinforce a fact (Ebbinghaus reset)
```bash
FACT_ID=$(cortex list --facts --json --limit 1 | python3 -c "import sys,json; row=json.load(sys.stdin)[0]; print(row.get('id', row.get('ID')))" )
cortex reinforce "$FACT_ID"
# ✅ PASS: "Reinforced fact <id>" — confidence should stay high
# ❌ FAIL: error or no effect
```

### 5c. Update a memory
> **Note:** `cortex update` is not yet a CLI command (store method exists internally).
> Test via reimport instead:
```bash
echo "# Updated project content" > "$CORTEX_TEST_DIR/notes/update_test.md"
cortex import "$CORTEX_TEST_DIR/notes/update_test.md"
# Then modify the file and reimport:
echo "# Updated project content v2 — auditor edit" > "$CORTEX_TEST_DIR/notes/update_test.md"
cortex import "$CORTEX_TEST_DIR/notes/update_test.md"
# ✅ PASS: content updated (hash changed, reimported)
# ❌ FAIL: deduped as unchanged despite different content
```

### 5d. Cleanup (dry run)
```bash
cortex cleanup --dry-run
# ✅ PASS: shows counts of what WOULD be cleaned, no data modified
# ❌ FAIL: actually deletes data, crashes, or flag not recognized
```

### 5e. Cleanup (execute)
```bash
cortex cleanup
# ✅ PASS: shows what was cleaned (short/garbage memories, headless facts)
# ❌ FAIL: crashes or corrupts database
```

---

## 6. Reasoning (requires OpenRouter API key)

> Skip this section if `$OPENROUTER_API_KEY` is not set.

### 6a. Interactive reason
```bash
cortex reason "What technology decisions has the team made?" --embed ollama/nomic-embed-text
# ✅ PASS: synthesized answer referencing PostgreSQL, GraphQL, K8s
# ❌ FAIL: empty output, hallucinated data, or crash
```

### 6b. Recursive reason
```bash
cortex reason "What are the risks in this project?" --recursive --embed ollama/nomic-embed-text
# ✅ PASS: multi-iteration analysis, pulls multiple memories
# Check: output shows "N iterations, N calls" in footer
# ❌ FAIL: single iteration only, or recursive loop
```

### 6c. Preset reason
```bash
cortex reason --preset daily-digest --embed ollama/nomic-embed-text
cortex reason --preset fact-audit --embed ollama/nomic-embed-text
cortex reason --preset weekly-dive "project architecture" --embed ollama/nomic-embed-text
# ✅ PASS: each produces structured output with headers
# ❌ FAIL: preset not found, or empty output
```

### 6d. List presets
```bash
cortex reason --list
# ✅ PASS: shows 5 presets (daily-digest, fact-audit, conflict-check, weekly-dive, agent-review)
# ❌ FAIL: missing presets or crash
```

### 6e. Custom model override
```bash
cortex reason "summarize the project" --model openrouter/google/gemini-2.5-flash --embed ollama/nomic-embed-text
# ✅ PASS: uses specified model, shows model name in footer
# ❌ FAIL: ignores model flag, or unsupported model error
```

---

## 7. Benchmarking

### 7a. Basic bench (requires OpenRouter)
```bash
cortex bench --embed ollama/nomic-embed-text --json 2>&1 | head -5
# ✅ PASS: starts running models × presets
# ❌ FAIL: crashes before first result
```

### 7b. Compare mode
```bash
cortex bench --compare openrouter/google/gemini-3-flash-preview,openrouter/openai/gpt-5.1-codex-mini --embed ollama/nomic-embed-text --json | python3 -c "import sys,json; r=json.load(sys.stdin); print(f'Compare: {r[\"compare_mode\"]}, Models: {r[\"models_tested\"]}')"
# ✅ PASS: compare_mode: true, models_tested: 2
# ❌ FAIL: compare flag ignored
```

### 7c. Recursive bench
```bash
cortex bench --recursive --models openrouter/google/gemini-3-flash-preview --embed ollama/nomic-embed-text --json | python3 -c "import sys,json; r=json.load(sys.stdin); print(f'Recursive: {r[\"recursive\"]}')"
# ✅ PASS: recursive: true in output
# ❌ FAIL: recursive flag ignored
```

---

## 8. MCP Server

### 8a. Stdio mode
```bash
echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}' | head -c 999 | cortex mcp 2>/dev/null &
MCP_STDIO_PID=$!; sleep 3; kill $MCP_STDIO_PID 2>/dev/null
# Alternative for macOS (no `timeout`): use background + sleep + kill
# ✅ PASS: returns JSON-RPC response with server capabilities
# ❌ FAIL: hangs, crashes, or invalid JSON
```

### 8b. HTTP mode (background)
```bash
cortex mcp --port 8787 &
MCP_PID=$!
sleep 2
curl -s http://localhost:8787/sse | head -1
kill $MCP_PID 2>/dev/null
# ✅ PASS: SSE endpoint responds
# ❌ FAIL: port bind error, no response
```

---

## 9. Edge Cases & Stress Tests

### 9a. Empty database search
```bash
CORTEX_DB="$CORTEX_TEST_DIR/empty.db" cortex search "anything"
# ✅ PASS: returns empty results gracefully
# ❌ FAIL: crashes on empty DB
```

### 9b. Binary file import
```bash
dd if=/dev/urandom of="$CORTEX_TEST_DIR/random.bin" bs=1024 count=10 2>/dev/null
cortex import "$CORTEX_TEST_DIR/random.bin"
# ✅ PASS: rejects binary file with clear message
# ❌ FAIL: imports garbage, or crashes
```

### 9c. Huge file import
```bash
python3 -c "print('# Big File\n' + 'Lorem ipsum dolor sit amet. ' * 10000)" > "$CORTEX_TEST_DIR/huge.md"
cortex import "$CORTEX_TEST_DIR/huge.md"
# ✅ PASS: imports (possibly chunked), no OOM
# ❌ FAIL: crashes, OOM, or corrupted import
```

### 9d. Unicode/emoji content
```bash
cat > "$CORTEX_TEST_DIR/unicode.md" << 'EOF'
# 🧠 Cortex テスト

## 日本語テスト
これはテストです。Ñoño café résumé naïve.

## Emoji stress: 🏴‍☠️👨‍👩‍👧‍👦🇺🇸 ✅❌⚠️🔥💀
EOF
cortex import "$CORTEX_TEST_DIR/unicode.md" --extract
cortex search "テスト" --limit 3
# ✅ PASS: imports and finds Unicode content
# ❌ FAIL: encoding errors, garbled output
```

### 9e. Concurrent imports
```bash
for i in $(seq 1 10); do
  cat > "$CORTEX_TEST_DIR/concurrent_$i.md" <<EOF
# Concurrent Note $i

Decision: use lock-safe import path for worker $i.
Status: worker $i completed preflight checks.
Context: this fixture is intentionally substantive (not low-signal) for import locking validation.
EOF
  cortex import "$CORTEX_TEST_DIR/concurrent_$i.md" &
done
wait
cortex stats
# ✅ PASS: all 10 imported, no SQLITE_BUSY errors
# ❌ FAIL: locked database errors, missing imports
```

### 9f. Read-only mode
```bash
cortex search "PostgreSQL" --read-only --limit 3
# ✅ PASS: search works without write access
# ❌ FAIL: attempts to write, errors
```

### 9g. Empty/malformed input
```bash
echo "" | cortex import /dev/stdin 2>&1
# ✅ PASS: reports "empty or whitespace-only content" error (not silent)
cortex search "" 2>&1
# ✅ PASS: graceful error or empty results
cortex reason "" 2>&1
# ✅ PASS: graceful error message
# ❌ FAIL: any of the three panics, crashes, hangs, or silently succeeds with no output
```

### 9h. Metadata survives reimport
```bash
# Import without metadata first
cortex import "$CORTEX_TEST_DIR/notes/project.md"
# Now reimport WITH metadata — should update existing memory
cortex import "$CORTEX_TEST_DIR/notes/project.md" --metadata '{"agent_id":"auditor","channel":"test"}'
# Search with agent filter
cortex search "PostgreSQL" --agent auditor --limit 5
# ✅ PASS: returns results (metadata was applied to existing memory)
# ❌ FAIL: empty results (metadata not updated on dedup match)
```

### 9i. bench --json is clean JSON
```bash
# Verify bench JSON output has no progress pollution on stdout
cortex bench --models openrouter/google/gemini-3-flash-preview --embed ollama/nomic-embed-text --json 2>/dev/null | python3 -c "import sys,json; d=json.load(sys.stdin); print(f'Clean JSON: {len(d[\"results\"])} results')"
# ✅ PASS: valid JSON parsed successfully (progress went to stderr)
# ❌ FAIL: JSON parse error (progress mixed into stdout)
```

---

## 10. Auto-Capture Hygiene (#36)

### 10a. Near-duplicate detection
```bash
cortex import "$CORTEX_TEST_DIR/notes/project.md" --capture-dedupe --similarity-threshold 0.90
# ✅ PASS: skips import (already exists), reports duplicate
# ❌ FAIL: reimports duplicate
```

### 10b. Low-signal filtering
```bash
echo "ok" > "$CORTEX_TEST_DIR/lowsignal.md"
cortex import "$CORTEX_TEST_DIR/lowsignal.md"
# ✅ PASS: skipped as too short (CLI rejects very short content)
# Note: This is EXPECTED behavior — files under ~10 chars are low-signal
# The OpenClaw plugin has additional filtering (burst coalescing, near-dupe)
```

---

## 11. HNSW Index (#18)

### 11a. Build index
```bash
cortex index --embed ollama/nomic-embed-text
# ✅ PASS: builds HNSW index, reports vector count and time
# ❌ FAIL: crashes, or zero vectors indexed
```

### 11b. Verify index used in search
```bash
cortex search "database" --mode semantic --embed ollama/nomic-embed-text --limit 3
# With index built, this should be faster than brute-force
# ✅ PASS: returns results (index loaded silently)
# ❌ FAIL: index not loaded, falls back to brute-force without notice
```

---

## 12. Embed Watch Daemon (#33)

```bash
cortex embed ollama/nomic-embed-text --watch --interval 5s &
WATCH_PID=$!
sleep 3

# Import a new file while watch is running
echo "# New file for watch test" > "$CORTEX_TEST_DIR/watchtest.md"
cortex import "$CORTEX_TEST_DIR/watchtest.md"
sleep 20  # Give watch daemon enough time for at least 2 polling cycles

kill $WATCH_PID 2>/dev/null
# ✅ PASS: new memory gets embedded automatically
# ❌ FAIL: watch doesn't detect new memories
```

---

## Audit Report Template

Copy and fill this out:

```markdown
# Cortex Audit Report
**Auditor:** [name]
**Date:** [date]
**Version:** [cortex version output]
**Platform:** [OS/arch]
**Ollama:** [yes/no + version]
**OpenRouter:** [yes/no]

## Results Summary
| Section | Tests | Pass | Fail | Skip | Notes |
|---------|-------|------|------|------|-------|
| 1. Install | 3 | | | | |
| 2. Import | 7 | | | | |
| 3. Search | 7 | | | | |
| 4. Facts | 5 | | | | |
| 5. Lifecycle | 5 | | | | |
| 6. Reasoning | 5 | | | | |
| 7. Bench | 3 | | | | |
| 8. MCP | 2 | | | | |
| 9. Edge Cases | 10 | | | | |
| 10. Hygiene | 2 | | | | |
| 11. HNSW | 2 | | | | |
| 12. Watch | 1 | | | | |
| **TOTAL** | **52** | | | | |

## Critical Failures (blocks release)
- [ ] ...

## Major Issues (should fix before next release)
- [ ] ...

## Minor Issues (nice to fix)
- [ ] ...

## Positive Observations
- ...

## Recommendations
- ...
```

---

## Cleanup
```bash
rm -rf "$CORTEX_TEST_DIR"
```

---

*Thank you for auditing Cortex. File issues at https://github.com/hurttlocker/cortex/issues with the tag `audit`.*
