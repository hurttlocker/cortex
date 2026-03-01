# Local-first (offline) setup

Use Cortex without cloud API keys.

## 1) Copy config

```bash
mkdir -p ~/.cortex
cp examples/local-first/config.yaml ~/.cortex/config.yaml
```

## 2) (Optional) semantic embeddings with Ollama

```bash
ollama pull nomic-embed-text
```

## 3) Import + extract + search

```bash
cortex import ~/notes --recursive --extract
cortex search "what did I decide about deployment"
cortex doctor
```

This mode keeps everything local in `~/.cortex/cortex.db`.
