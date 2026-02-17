# Software to Piggyback On

> Libraries, tools, and patterns we leverage instead of building from scratch.

---

## Core Dependencies (MVP)

| Library | What | How We Use It | Language | Phase |
|---------|------|---------------|----------|-------|
| **modernc.org/sqlite** | Pure Go SQLite driver | Storage backbone, no CGO required | Go | MVP |
| **Cobra** (spf13/cobra) | CLI framework | Command structure, flags, help | Go | MVP |
| **prose** (jdkato/prose) | Basic Go NLP | Tokenization, sentence splitting, basic NER for Tier 1 | Go | MVP |
| **ONNX Runtime** (onnxruntime-go) | Local model inference | Semantic search embeddings | Go + C | MVP |
| **all-MiniLM-L6-v2** | Sentence embedding model | 384-dim vectors, ~80MB, runs anywhere | ONNX | MVP |

## Extraction Layer

| Library/Pattern | What | How We Use It | Phase |
|-----------------|------|---------------|-------|
| **Instructor pattern** (jxnl/instructor) | Schema-constrained LLM output | LLM-assist extraction — we implement the PATTERN in Go (schema + prompt → validate JSON), don't need the Python lib | MVP |
| **Outlines** (outlines-dev/outlines) | Constrained decoding for local models | Guarantee valid JSON from Ollama/local models. Modifies sampling to only produce schema-valid tokens | v1.1 |
| **GLiNER** (urchade/GLiNER) | Zero-shot NER without fine-tuning | Local entity extraction — pass any entity types, get structured output. ~400MB ONNX model | v1.1 |
| **Fine-tuned T5-small** | Tiny structured extraction model | ~60MB model trained on our extraction schema. Bridges gap between regex and LLM | v1.1 |

## Document Processing (Phase 2+)

| Library | What | How We Use It | Phase |
|---------|------|---------------|-------|
| **Docling** (IBM) | Any document → structured JSON | PDF, DOCX, HTML, PPTX import support | v2 |
| **Unstructured.io** | Document parsing + chunking | Alternative to Docling, more community-driven | v2 |

## Alternative Storage (If Needed)

| Library | What | When We'd Use It | Phase |
|---------|------|-------------------|-------|
| **LanceDB** | Embedded vector DB (Rust + Go bindings) | If BLOB-based embedding search in SQLite becomes too slow at >100K entries | v2 |
| **DuckDB** | Analytical database | Complex analytical queries over large memory stores | v3 |

## Key Insight

**We don't need to build an LLM framework.** The Instructor pattern is:
1. Define a JSON schema
2. Send schema + text to any OpenAI-compatible API
3. Parse response
4. Validate against schema
5. Retry if invalid

That's ~100 lines of Go. Instructor's innovation was realizing that structured output + validation is the hard part, not the LLM call itself. We just implement the pattern natively.

## Model Landscape (Feb 2026)

New models make LLM-assist nearly free:

| Model | Cost per 1M tokens | Quality | Provider |
|-------|--------------------:|---------|----------|
| Ollama/gemma2:2b | $0.00 (local) | Good for extraction | Local |
| Ollama/llama3.2:3b | $0.00 (local) | Good for extraction | Local |
| GPT-4.1-nano | ~$0.10 | Good | OpenAI |
| DeepSeek v3 | ~$0.14 | Excellent | DeepSeek |
| Gemini Flash Lite | ~$0.075 | Good | Google |
| Claude Haiku | ~$0.25 | Excellent | Anthropic |
| Qwen 3.5 (new) | TBD | Claims 8x faster | Alibaba |

At these prices, extracting facts from 1,000 documents costs less than a cup of coffee.
