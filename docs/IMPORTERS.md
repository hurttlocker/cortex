# Importer Specifications

> Format-specific details for each supported import source.

## General Principles

All importers follow the same contract:

1. **Parse** the source file into chunks (memory units)
2. **Preserve provenance** — source file path, line number, original timestamps
3. **Handle errors gracefully** — skip unparseable sections, log warnings, continue
4. **Be idempotent** — re-importing the same file updates existing entries (dedup by source + content hash)

## Markdown Importer

**File extensions:** `.md`, `.markdown`

### Chunking Strategy

- Split on `## ` (h2 headers) as primary boundaries
- Each section becomes one memory unit
- If no headers exist, split on double newlines (paragraphs)
- Preserve header text as metadata

### Special Handling

- **MEMORY.md format:** Detect key-value patterns like `- **Key:** Value` and extract as structured facts
- **Daily notes:** Detect date-based filenames (`2024-01-15.md`) and attach date metadata
- **Front matter:** Parse YAML front matter if present
- **Code blocks:** Preserved as-is within the memory unit (not split)

### Example

Input (`MEMORY.md`):
```markdown
## User Preferences
- **Editor:** VS Code with vim keybindings
- **Theme:** Dark mode always
- **Language:** Prefers Go for CLIs, TypeScript for web

## Project Context
Working on Cortex — an AI memory layer.
Primary repo: github.com/LavonTMCQ/cortex
```

Result: 2 memory units, 3 extracted key-value facts from the first section.

---

## JSON Importer

**File extensions:** `.json`

### Supported Structures

1. **Array of objects** — Each object becomes one memory unit
2. **Single object** — Each top-level key becomes one memory unit
3. **Nested objects** — Flattened with dot notation (`user.preferences.theme`)

### Special Handling

- **Conversation logs:** Detect `role`/`content` patterns (OpenAI format) and merge into conversation chunks
- **Agent state:** Detect common agent state patterns and extract facts
- **Large arrays:** Chunked into groups if individual items are very small

---

## YAML Importer

**File extensions:** `.yaml`, `.yml`

### Behavior

- Parsed identically to JSON after YAML→JSON conversion
- Multi-document YAML (separated by `---`) creates one memory unit per document
- Anchors and aliases are resolved before import

---

## CSV Importer

**File extensions:** `.csv`, `.tsv`

### Behavior

- First row treated as headers (configurable with `--no-header`)
- Each row becomes one memory unit
- Headers become fact keys, cell values become fact values
- Empty cells are skipped

### Example

Input:
```csv
name,role,team
Alice,Engineer,Platform
Bob,Designer,Product
```

Result: 2 memory units, each with 3 key-value facts.

---

## Plain Text Importer

**File extensions:** `.txt`, `.log`, and any unrecognized format

### Chunking Strategy

- Split on double newlines (paragraph boundaries)
- If paragraphs are very long (>500 words), split on sentence boundaries
- If no paragraph breaks, split on fixed token count (~256 tokens)

### Special Handling

- **Chat logs:** Detect `Username: message` or `[timestamp] message` patterns
- **Terminal output:** Detect `$` or `>` prompt patterns and group command+output

---

## Directory Import

```bash
cortex import ~/notes/ --recursive
```

- Walks the directory tree
- Applies the appropriate importer based on file extension
- Respects `.gitignore` and `.cortexignore` patterns
- Skips binary files, images, and files over 10MB (configurable)

---

## Deduplication

Re-importing the same file:
1. Hash each memory unit (SHA-256 of content + source path)
2. If hash exists → skip (no duplicate)
3. If source path exists but hash changed → update the entry
4. If new → insert

This makes `cortex import` safe to run repeatedly (e.g., in a cron job or pre-commit hook).
