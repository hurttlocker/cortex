# Cortex + OpenClaw Happy Path (Canonical)

> Single source of truth for first-time OpenClaw users.
> Goal: clean machine → working Cortex plugin with actionable verification.

## 0) Prerequisites
- OpenClaw installed and runnable
- Cortex binary installed (`cortex version` works)
- Optional but recommended for hybrid search: Ollama + `nomic-embed-text`

## 1) Install / update Cortex runtime

```bash
# Homebrew (macOS)
brew install hurttlocker/cortex/cortex-memory
# or
brew upgrade hurttlocker/cortex/cortex-memory

# Verify runtime
cortex version
```

If `~/bin/cortex` exists from older manual installs, either update it or use one canonical binary path to avoid runtime drift.

## 2) Initialize Cortex DB and embeddings

```bash
cortex init

# optional semantic/hybrid path
ollama pull nomic-embed-text
cortex embed ollama/nomic-embed-text
```

## 3) Install plugin

```bash
# from npm
openclaw plugins install openclaw-cortex

# or from local repo
openclaw plugins install ./plugin
```

## 4) Configure OpenClaw (must nest under `config`)

Add to `~/.openclaw/openclaw.json`:

```json
{
  "plugins": {
    "entries": {
      "openclaw-cortex": {
        "enabled": true,
        "config": {
          "autoRecall": true,
          "autoCapture": true,
          "extractFacts": true,
          "searchMode": "hybrid",
          "embedProvider": "ollama/nomic-embed-text"
        }
      }
    }
  }
}
```

> Important: plugin keys at top-level (outside `config`) cause setup drift and startup problems.

## 5) Run canonical verification

```bash
openclaw cortex setup
```

This command now verifies and prints next actions for:
- OpenClaw config file + plugin entry + config placement
- Cortex binary execution/version
- stale `~/bin/cortex` drift against PATH runtime
- runtime compatibility drift checks
- doctor-derived DB/embedding/version health
- HNSW warning + rebuild health (`cortex index` guidance)

If a check fails, fix the printed `next:` action and rerun `openclaw cortex setup`.

## 6) Smoke test

```bash
openclaw cortex search "cortex openclaw setup"
openclaw cortex stats
```

If both return valid results, the happy path is complete.

---

## Troubleshooting quick map

| Symptom | Next action |
|---|---|
| `cannot execute <binary>` | install/update Cortex, set `plugins.entries.openclaw-cortex.config.binaryPath` |
| `config_placement` fail | move plugin settings under `config` |
| stale `~/bin/cortex` warning | update/remove stale binary or unify binary path |
| `doctor_hnsw_index` warning | run `cortex index`, rerun setup |
| embeddings warning | run `cortex embed ollama/nomic-embed-text` |

Keep this document as the canonical OpenClaw setup path. Other docs should link here instead of duplicating steps.
