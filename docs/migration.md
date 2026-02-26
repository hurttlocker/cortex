# Migration Guide

## Upgrading to v1.0 (from any version)

### TL;DR

**Recommended for all users:** Clean reimport.

```bash
# 1. Back up your database
cp ~/.cortex/cortex.db ~/.cortex/cortex.db.backup

# 2. Install v1.0
brew upgrade cortex-memory
# or: download from https://github.com/hurttlocker/cortex/releases/latest

# 3. Delete old database
rm ~/.cortex/cortex.db

# 4. Reimport your files with full enrichment
cortex import ~/notes/ --recursive --extract

# 5. Verify
cortex stats
cortex doctor
```

**Why reimport instead of upgrade-in-place?** The extraction pipeline improved dramatically between v0.3.x and v0.9.0. Old databases often contain:
- Noisy `kv` facts from weak governor rules (pre-v0.6.0)
- Duplicate facts from extraction bugs (pre-v0.9.0, issue #228)
- Misclassified fact types (pre-v0.9.0 classification)

A clean reimport with the current pipeline produces 50-90% fewer, higher-quality facts.

---

## Version-Specific Notes

### From v0.8.x → v0.9.0+

**What changed:**
- LLM enrichment is now on by default when `--extract` is used
- Auto-classification runs after extraction
- Governor tightened: `MaxFacts=10` (was 20), `MinPredicate=5`, `MinObject=3`
- Content-hash dedup fixes (same content from different files → separate memories)
- Extraction only targets newly imported memories (fixes fact explosion bug)

**Action needed:**
- If you have an LLM provider configured, `--extract` will now also enrich + classify. Use `--no-enrich --no-classify` to keep old behavior.
- Clean reimport recommended to benefit from tighter governor and dedup fixes.

### From v0.7.x → v0.8.0+

**What changed:**
- HNSW approximate nearest neighbor index for faster semantic search
- Graph cluster detection
- Graph ranking with pagination
- New MCP tools: graph explore, graph impact, clusters

**Action needed:**
- No breaking changes. Upgrade binary and restart MCP server.
- Run `cortex embed <provider/model>` to generate embeddings for new ANN index.

### From v0.6.x → v0.7.0+

**What changed:**
- 8 source connectors (GitHub, Gmail, Calendar, Drive, Slack, Discord, Telegram, Notion)
- Connector state tables added to database (auto-migrated)
- Agent scoping (`--agent` flag on most commands)
- `cortex connect` subcommands added
- `cortex agents` command added

**Action needed:**
- No breaking changes. Database migrates automatically.
- To use connectors: `cortex connect init` (first time only).

### From v0.5.x → v0.6.0+

**What changed:**
- Interactive 2D graph explorer
- Extraction governor overhaul (`MaxSubjectLength=50`, auto-capture cap=5)
- Search result filtering

**Action needed:**
- Clean reimport **strongly recommended** — the governor changes mean old facts include a lot of noise that the new governor would filter out.

### From v0.3.x → v0.5.0+

**What changed:**
- Fact type system (9 types)
- Ebbinghaus confidence decay
- Knowledge graph (subject → predicate → object)
- MCP server
- Complete extraction pipeline rewrite

**Action needed:**
- **Clean reimport required.** The data model changed significantly. Old databases will work for basic operations but won't have typed facts, decay curves, or graph data.

### From v0.1.x / v0.2.x → v0.5.0+

**Action needed:**
- Clean reimport required. These versions predate the current storage schema.

---

## Database Location

Default: `~/.cortex/cortex.db`

Override with:
```bash
# Environment variable
export CORTEX_DB_PATH=/path/to/cortex.db

# Config file (~/.cortex/config.yaml)
db_path: /path/to/cortex.db

# CLI flag
cortex stats --db /path/to/cortex.db
```

## Backup & Restore

```bash
# Backup (safe — SQLite WAL mode handles this)
cp ~/.cortex/cortex.db ~/.cortex/cortex.db.backup

# Restore
cp ~/.cortex/cortex.db.backup ~/.cortex/cortex.db

# Or just reimport from source files (recommended)
rm ~/.cortex/cortex.db
cortex import ~/notes/ --recursive --extract
```

Your source files are the real source of truth. The database is a derivative. When in doubt, reimport.

## Verifying Your Upgrade

After upgrading, run:

```bash
# Check version
cortex version

# Check database health
cortex doctor

# Check stats
cortex stats

# Check for stale facts
cortex stale --days 30

# Check for conflicts
cortex conflicts
```

If `doctor` reports issues, the fix is almost always: backup → delete DB → reimport.
