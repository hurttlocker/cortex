/**
 * OpenClaw Cortex Plugin
 *
 * Local-first AI memory with hybrid search and confidence decay.
 * Zero cloud dependencies — uses the local cortex binary + ollama embeddings.
 *
 * Features:
 *   - Auto-recall: injects relevant memories before each AI turn
 *   - Auto-capture: extracts facts from conversations after each AI turn
 *   - Hybrid search: BM25 + semantic via local embeddings
 *   - Confidence decay: Ebbinghaus-based memory aging
 *   - Full observability: SQLite DB, queryable, exportable
 *
 * Install: openclaw plugins install ./plugin  (from cortex repo)
 *    or:   openclaw plugins install openclaw-cortex  (from npm)
 */

import type { OpenClawPluginApi } from "openclaw/plugin-sdk";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { writeFile, unlink, mkdtemp, readFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { homedir } from "node:os";
import { existsSync } from "node:fs";

import { cosineSimilarity, dedupeRecallResults, isLowSignalMessage, sanitizeCaptureMessage } from "./hygiene.ts";
import { buildRecallPlan } from "./recall.ts";

const execFileAsync = promisify(execFile);

// ============================================================================
// Types
// ============================================================================

interface CaptureHygieneConfig {
  dedupe: {
    enabled: boolean;
  };
  similarityThreshold: number;
  dedupeWindowSec: number;
  coalesceWindowSec: number;
  shortCaptureMaxChars: number;
  minCaptureChars: number;
  lowSignalPatterns: string[];
}

interface RecallDedupeConfig {
  enabled: boolean;
  similarityThreshold: number;
}

interface CortexConfig {
  binaryPath: string;
  dbPath: string;
  embedProvider: string;
  searchMode: "hybrid" | "bm25" | "semantic";
  autoCapture: boolean;
  autoRecall: boolean;
  recallLimit: number;
  recallBudgetChars: number;
  minScore: number;
  captureMaxChars: number;
  extractFacts: boolean;
  capture: CaptureHygieneConfig;
  recallDedupe: RecallDedupeConfig;
}

interface CortexSearchResult {
  content: string;
  source_file: string;
  source_line: number;
  source_section: string;
  score: number;
  match_type: string;
  memory_id: number;
  snippet?: string;
}

interface CortexSearchOptions {
  agent?: string;
  channel?: string;
  sessionKey?: string;
  boostAgent?: string;
  boostChannel?: string;
  boostSessionKey?: string;
  after?: string;
}

interface CortexStats {
  memories: number;
  facts: number;
  sources: number;
  storage_bytes: number;
  avg_confidence: number;
  facts_by_type: Record<string, number>;
  confidence_distribution: {
    high: number;
    medium: number;
    low: number;
    total: number;
  };
}

interface DoctorCheck {
  name: string;
  status: "pass" | "warn" | "fail";
  details: string;
  hint?: string;
}

interface DoctorReport {
  generated_at: string;
  db_path: string;
  summary: {
    pass: number;
    warn: number;
    fail: number;
  };
  checks: DoctorCheck[];
}

interface SetupCheck {
  name: string;
  status: "pass" | "warn" | "fail";
  details: string;
  hint?: string;
}

/** Structured metadata attached to memories (Issue #30). */
interface CortexMetadata {
  session_key?: string;
  channel?: string;
  channel_id?: string;
  channel_name?: string;
  agent_id?: string;
  agent_name?: string;
  model?: string;
  input_tokens?: number;
  output_tokens?: number;
  message_count?: number;
  surface?: string;
  chat_type?: string;
  timestamp_start?: string;
  timestamp_end?: string;
}

// ============================================================================
// Config Parser
// ============================================================================

function resolveDefaultBinaryPath(): string {
  const candidates = [
    join(homedir(), "bin", "cortex"),
    "/usr/local/bin/cortex",
    "/usr/bin/cortex",
  ];
  for (const p of candidates) {
    if (existsSync(p)) return p;
  }
  return "cortex"; // Fall back to PATH
}

function parseConfig(raw: unknown): CortexConfig {
  const cfg = (raw && typeof raw === "object" ? raw : {}) as Record<string, unknown>;
  const capture = (cfg.capture && typeof cfg.capture === "object" ? cfg.capture : {}) as Record<string, unknown>;
  const dedupe = (capture.dedupe && typeof capture.dedupe === "object" ? capture.dedupe : {}) as Record<string, unknown>;
  const recallDedupe = (cfg.recallDedupe && typeof cfg.recallDedupe === "object" ? cfg.recallDedupe : {}) as Record<string, unknown>;

  const lowSignalDefaults = [
    "ok",
    "okay",
    "yes",
    "yep",
    "got it",
    "sounds good",
    "thanks",
    "thank you",
    "heartbeat_ok",
    "fire the test",
  ];
  const lowSignalPatterns = Array.isArray(capture.lowSignalPatterns)
    ? capture.lowSignalPatterns.filter((p): p is string => typeof p === "string" && p.trim().length > 0)
    : lowSignalDefaults;

  const binaryPath = typeof cfg.binaryPath === "string" ? expandHomePath(cfg.binaryPath) : resolveDefaultBinaryPath();
  const dbPath = typeof cfg.dbPath === "string" ? expandHomePath(cfg.dbPath) : join(homedir(), ".cortex", "cortex.db");

  return {
    binaryPath,
    dbPath,
    embedProvider: typeof cfg.embedProvider === "string" ? cfg.embedProvider : "ollama/nomic-embed-text",
    searchMode: (cfg.searchMode as CortexConfig["searchMode"]) ?? "hybrid",
    autoCapture: cfg.autoCapture === true,
    autoRecall: cfg.autoRecall !== false, // Default ON
    recallLimit: typeof cfg.recallLimit === "number" ? cfg.recallLimit : 3,
    recallBudgetChars:
      typeof cfg.recallBudgetChars === "number" && Number.isFinite(cfg.recallBudgetChars)
        ? cfg.recallBudgetChars
        : 3000,
    minScore: typeof cfg.minScore === "number" ? cfg.minScore : 0.3,
    captureMaxChars: typeof cfg.captureMaxChars === "number" ? cfg.captureMaxChars : 2000,
    extractFacts: cfg.extractFacts !== false, // Default ON
    capture: {
      dedupe: {
        enabled: dedupe.enabled !== false,
      },
      similarityThreshold:
        typeof capture.similarityThreshold === "number" && capture.similarityThreshold > 0 && capture.similarityThreshold <= 1
          ? capture.similarityThreshold
          : 0.95,
      dedupeWindowSec:
        typeof capture.dedupeWindowSec === "number" && capture.dedupeWindowSec > 0
          ? capture.dedupeWindowSec
          : 300,
      coalesceWindowSec:
        typeof capture.coalesceWindowSec === "number" && capture.coalesceWindowSec >= 0
          ? capture.coalesceWindowSec
          : 20,
      shortCaptureMaxChars:
        typeof capture.shortCaptureMaxChars === "number" && capture.shortCaptureMaxChars > 0
          ? capture.shortCaptureMaxChars
          : 220,
      minCaptureChars:
        typeof capture.minCaptureChars === "number" && capture.minCaptureChars > 0
          ? capture.minCaptureChars
          : 20,
      lowSignalPatterns,
    },
    recallDedupe: {
      enabled: recallDedupe.enabled !== false,
      similarityThreshold:
        typeof recallDedupe.similarityThreshold === "number" && recallDedupe.similarityThreshold > 0 && recallDedupe.similarityThreshold <= 1
          ? recallDedupe.similarityThreshold
          : 0.98,
    },
  };
}

const canonicalOpenClawSetupDoc = "docs/openclaw-happy-path.md";
const minimumRecommendedCortexVersion = "1.2.4";
const knownPluginConfigKeys = new Set([
  "binaryPath", "dbPath", "embedProvider", "searchMode", "autoCapture", "autoRecall", "recallLimit", "recallBudgetChars", "minScore",
  "captureMaxChars", "extractFacts", "capture", "recallDedupe",
]);

function isCompactionLikePrompt(prompt: string): boolean {
  return /(post-compaction context refresh|pre-compaction memory flush|compaction|memory flush)/i.test(prompt);
}

function formatDateYYYYMMDD(date: Date): string {
  const yyyy = date.getFullYear();
  const mm = String(date.getMonth() + 1).padStart(2, "0");
  const dd = String(date.getDate()).padStart(2, "0");
  return `${yyyy}-${mm}-${dd}`;
}

function expandHomePath(input: string): string {
  const trimmed = input.trim();
  if (trimmed === "~") return homedir();
  if (trimmed.startsWith("~/")) return join(homedir(), trimmed.slice(2));
  return trimmed;
}

function extractCortexVersion(raw: string): string | null {
  const match = raw.match(/(\d+)\.(\d+)\.(\d+)/);
  return match ? `${match[1]}.${match[2]}.${match[3]}` : null;
}

function compareSemver(a: string, b: string): number {
  const pa = a.split(".").map((x) => parseInt(x, 10) || 0);
  const pb = b.split(".").map((x) => parseInt(x, 10) || 0);
  const len = Math.max(pa.length, pb.length);
  for (let i = 0; i < len; i++) {
    const va = pa[i] ?? 0;
    const vb = pb[i] ?? 0;
    if (va > vb) return 1;
    if (va < vb) return -1;
  }
  return 0;
}

function iconForSetupStatus(status: SetupCheck["status"]): string {
  if (status === "pass") return "✅";
  if (status === "warn") return "⚠️";
  return "❌";
}

function findBinaryInPath(binName: string): string | null {
  const pathEnv = process.env.PATH || "";
  for (const dir of pathEnv.split(":")) {
    if (!dir) continue;
    const candidate = join(dir, binName);
    if (existsSync(candidate)) {
      return candidate;
    }
  }
  return null;
}

async function readBinaryVersion(binaryPath: string): Promise<string | null> {
  try {
    const { stdout } = await execFileAsync(binaryPath, ["--version"], {
      timeout: 10_000,
      env: { ...process.env, HOME: homedir() },
      maxBuffer: 1024 * 1024,
    });
    return extractCortexVersion(stdout.trim());
  } catch {
    return null;
  }
}

function findDoctorCheck(report: DoctorReport | null, name: string): DoctorCheck | undefined {
  if (!report?.checks) return undefined;
  return report.checks.find((c) => c.name === name);
}

async function inspectOpenClawConfigPlacement(): Promise<SetupCheck[]> {
  const checks: SetupCheck[] = [];
  const configPath = join(homedir(), ".openclaw", "openclaw.json");

  if (!existsSync(configPath)) {
    checks.push({
      name: "openclaw_config",
      status: "fail",
      details: `missing ${configPath}`,
      hint: "Create ~/.openclaw/openclaw.json, add plugins.entries.openclaw-cortex, then rerun `openclaw cortex setup`.",
    });
    return checks;
  }

  checks.push({
    name: "openclaw_config",
    status: "pass",
    details: `found ${configPath}`,
  });

  try {
    const raw = await readFile(configPath, "utf8");
    const parsed = JSON.parse(raw) as Record<string, any>;
    const entry = parsed?.plugins?.entries?.["openclaw-cortex"];

    if (!entry || typeof entry !== "object") {
      checks.push({
        name: "plugin_entry",
        status: "fail",
        details: "plugins.entries.openclaw-cortex is missing",
        hint: "Install plugin with `openclaw plugins install openclaw-cortex` and add the entry under plugins.entries.",
      });
      return checks;
    }

    checks.push({
      name: "plugin_entry",
      status: "pass",
      details: "plugins.entries.openclaw-cortex is configured",
    });

    if (entry.config && typeof entry.config === "object") {
      checks.push({
        name: "config_placement",
        status: "pass",
        details: "plugin settings are nested under config",
      });
    } else {
      const misplaced = Object.keys(entry).filter((k) => knownPluginConfigKeys.has(k));
      checks.push({
        name: "config_placement",
        status: misplaced.length > 0 ? "fail" : "warn",
        details: misplaced.length > 0
          ? `found config keys at top-level (${misplaced.join(", ")})`
          : "plugin config block is missing",
        hint: "Move Cortex settings under plugins.entries.openclaw-cortex.config to avoid gateway startup failures.",
      });
    }
  } catch (err: any) {
    checks.push({
      name: "openclaw_config_parse",
      status: "fail",
      details: `cannot parse ~/.openclaw/openclaw.json: ${err?.message ?? err}`,
      hint: "Fix JSON syntax, then rerun `openclaw cortex setup`.",
    });
  }

  return checks;
}

// ============================================================================
// Cortex CLI Wrapper
// ============================================================================

class CortexCLI {
  constructor(
    private readonly binaryPath: string,
    private readonly dbPath: string,
    private readonly embedProvider: string,
    private readonly defaultMode: string,
    private readonly logger: { info: (...args: any[]) => void; warn: (...args: any[]) => void },
  ) {}

  private async exec(args: string[], timeoutMs = 30_000): Promise<string> {
    try {
      const { stdout } = await execFileAsync(this.binaryPath, ["--db", this.dbPath, ...args], {
        timeout: timeoutMs,
        maxBuffer: 1024 * 1024, // 1MB
        env: { ...process.env, HOME: homedir() },
      });
      return stdout.trim();
    } catch (err: any) {
      this.logger.warn(`cortex exec failed: ${err.message}`);
      throw err;
    }
  }

  async search(
    query: string,
    limit = 5,
    mode?: string,
    minScore?: number,
    options?: CortexSearchOptions,
  ): Promise<CortexSearchResult[]> {
    const searchMode = mode ?? this.defaultMode;
    const args = ["search", query, "--limit", String(limit), "--json"];

    if (searchMode === "hybrid" || searchMode === "semantic") {
      args.push("--mode", searchMode, "--embed", this.embedProvider);
    } else {
      args.push("--mode", searchMode);
    }

    if (minScore !== undefined) {
      args.push("--min-score", String(minScore));
    }

    if (options?.agent) args.push("--agent", options.agent);
    if (options?.channel) args.push("--channel", options.channel);
    if (options?.sessionKey) args.push("--session-key", options.sessionKey);
    if (options?.boostAgent) args.push("--boost-agent", options.boostAgent);
    if (options?.boostChannel) args.push("--boost-channel", options.boostChannel);
    if (options?.boostSessionKey) args.push("--boost-session-key", options.boostSessionKey);
    if (options?.after) args.push("--after", options.after);

    const output = await this.exec(args);
    if (!output || output === "null" || output === "[]") return [];

    try {
      return JSON.parse(output) as CortexSearchResult[];
    } catch {
      return [];
    }
  }

  async importText(
    text: string,
    source: string,
    extract = true,
    metadata?: CortexMetadata,
    hygiene?: {
      dedupeEnabled?: boolean;
      similarityThreshold?: number;
      dedupeWindowSec?: number;
      lowSignalEnabled?: boolean;
      minCaptureChars?: number;
      lowSignalPatterns?: string[];
    },
  ): Promise<void> {
    // Write text to a temp file, import it, then clean up
    const tmpDir = await mkdtemp(join(tmpdir(), "cortex-capture-"));
    const tmpFile = join(tmpDir, `${source}.md`);

    try {
      await writeFile(tmpFile, text, "utf-8");
      const args = ["import", tmpFile];
      if (extract) args.push("--extract");

      // Capture hygiene server-side dedupe controls (Issue #36)
      if (hygiene?.dedupeEnabled) {
        args.push("--capture-dedupe");
        if (typeof hygiene.similarityThreshold === "number") {
          args.push("--similarity-threshold", String(hygiene.similarityThreshold));
        }
        if (typeof hygiene.dedupeWindowSec === "number") {
          args.push("--dedupe-window-sec", String(hygiene.dedupeWindowSec));
        }
      }
      if (hygiene?.lowSignalEnabled) {
        args.push("--capture-low-signal");
        if (typeof hygiene.minCaptureChars === "number") {
          args.push("--capture-min-chars", String(hygiene.minCaptureChars));
        }
        for (const pattern of hygiene.lowSignalPatterns ?? []) {
          args.push("--capture-low-signal-pattern", pattern);
        }
      }

      // Attach structured metadata if provided (Issue #30)
      if (metadata) {
        // Strip undefined/null values for clean JSON
        const clean: Record<string, unknown> = {};
        for (const [k, v] of Object.entries(metadata)) {
          if (v !== undefined && v !== null && v !== "" && v !== 0) {
            clean[k] = v;
          }
        }
        if (Object.keys(clean).length > 0) {
          args.push("--metadata", JSON.stringify(clean));
        }
      }

      await this.exec(args, 60_000);
    } finally {
      try {
        await unlink(tmpFile);
      } catch { /* best effort */ }
    }
  }

  async stats(): Promise<CortexStats | null> {
    try {
      const output = await this.exec(["stats", "--json"]);
      return JSON.parse(output) as CortexStats;
    } catch {
      return null;
    }
  }

  async doctor(): Promise<DoctorReport | null> {
    try {
      const output = await this.exec(["doctor", "--json"], 60_000);
      return JSON.parse(output) as DoctorReport;
    } catch {
      return null;
    }
  }

  async stale(days = 30): Promise<any[]> {
    try {
      const output = await this.exec(["stale", "--days", String(days), "--json"]);
      return JSON.parse(output) ?? [];
    } catch {
      return [];
    }
  }

  async reinforce(ids: string[]): Promise<void> {
    if (ids.length === 0) return;
    await this.exec(["reinforce", ...ids]);
  }
}

// ============================================================================
// Capture Logic
// ============================================================================

function shouldCapture(text: string, maxChars: number): boolean {
  if (!text || text.length < 20 || text.length > maxChars) return false;
  // Skip XML-like system content
  if (text.startsWith("<") && text.includes("</")) return false;
  // Skip memory context blocks
  if (text.includes("<cortex-memories>") || text.includes("<relevant-memories>")) return false;
  // Skip prompt injection patterns
  if (/ignore (all|previous|above) instructions/i.test(text)) return false;
  return true;
}

function formatCapturedExchange(userMsg: string, assistantMsg: string, channel?: string): string {
  const timestamp = new Date().toISOString();
  const header = `## Conversation Capture — ${timestamp}`;
  const channelLine = channel ? `Channel: ${channel}` : "";
  const parts = [header];
  if (channelLine) parts.push(channelLine);
  parts.push("", "### User", userMsg, "", "### Assistant", assistantMsg);
  return parts.join("\n");
}

interface CaptureRecord {
  text: string;
  canonical: string;
  metadata: CortexMetadata;
  source: string;
  createdAtMs: number;
  updatedAtMs: number;
  charCount: number;
  segmentCount: number;
}

interface HygieneResult {
  status: "captured" | "queued" | "skipped_low_signal" | "skipped_near_duplicate";
  similarity?: number;
  coalescedSegments?: number;
}

class CaptureHygiene {
  private readonly recent: Array<{ canonical: string; ts: number }> = [];
  private pending: CaptureRecord | null = null;
  private pendingTimer: ReturnType<typeof setTimeout> | null = null;

  constructor(
    private readonly cli: CortexCLI,
    private readonly logger: { info: (...args: any[]) => void; warn: (...args: any[]) => void },
    private readonly captureCfg: CaptureHygieneConfig,
    private readonly extractFacts: boolean,
  ) {}

  async ingest(record: CaptureRecord): Promise<HygieneResult> {
    // Drop low-signal trivial acknowledgements unless explicitly tagged important.
    if (isLowSignalMessage(record.canonical, this.captureCfg.lowSignalPatterns, this.captureCfg.minCaptureChars)) {
      return { status: "skipped_low_signal" };
    }

    // Plugin-side near-duplicate suppression against recent captures.
    if (this.captureCfg.dedupe.enabled) {
      const threshold = this.captureCfg.similarityThreshold;
      const windowMs = this.captureCfg.dedupeWindowSec * 1000;
      const now = Date.now();

      let best = 0;
      for (let i = this.recent.length - 1; i >= 0; i--) {
        const rec = this.recent[i];
        if (now - rec.ts > windowMs) continue;
        const score = cosineSimilarity(record.canonical, rec.canonical);
        if (score > best) best = score;
      }

      if (best >= threshold) {
        return { status: "skipped_near_duplicate", similarity: best };
      }
    }

    // Burst coalescing for short rapid-fire captures.
    const isShort = record.charCount <= this.captureCfg.shortCaptureMaxChars;
    if (this.captureCfg.coalesceWindowSec > 0 && isShort) {
      if (this.pending && (record.createdAtMs - this.pending.updatedAtMs) <= this.captureCfg.coalesceWindowSec * 1000) {
        this.pending.text = `${this.pending.text}\n\n---\n\n${record.text}`;
        this.pending.canonical = `${this.pending.canonical} ${record.canonical}`.trim();
        this.pending.updatedAtMs = record.createdAtMs;
        this.pending.charCount += record.charCount;
        this.pending.segmentCount += 1;
        this.pending.metadata.message_count = (this.pending.metadata.message_count ?? 0) + (record.metadata.message_count ?? 0);
        this.pending.metadata.timestamp_end = record.metadata.timestamp_end ?? new Date().toISOString();
        this.scheduleFlush();
        return { status: "queued", coalescedSegments: this.pending.segmentCount };
      }

      await this.flushPending();
      this.pending = record;
      this.scheduleFlush();
      return { status: "queued", coalescedSegments: 1 };
    }

    await this.flushPending();
    await this.persist(record);
    return { status: "captured", coalescedSegments: 1 };
  }

  async flushPending(): Promise<void> {
    if (!this.pending) return;

    const record = this.pending;
    this.pending = null;
    if (this.pendingTimer) {
      clearTimeout(this.pendingTimer);
      this.pendingTimer = null;
    }

    await this.persist(record);
  }

  private scheduleFlush() {
    if (this.pendingTimer) {
      clearTimeout(this.pendingTimer);
    }
    const waitMs = Math.max(1, this.captureCfg.coalesceWindowSec) * 1000;
    this.pendingTimer = setTimeout(() => {
      void this.flushPending().catch((err) => this.logger.warn(`cortex: pending flush failed: ${String(err)}`));
    }, waitMs);
  }

  private async persist(record: CaptureRecord): Promise<void> {
    await this.cli.importText(record.text, record.source, this.extractFacts, record.metadata, {
      dedupeEnabled: this.captureCfg.dedupe.enabled,
      similarityThreshold: this.captureCfg.similarityThreshold,
      dedupeWindowSec: this.captureCfg.dedupeWindowSec,
      lowSignalEnabled: true,
      minCaptureChars: this.captureCfg.minCaptureChars,
      lowSignalPatterns: this.captureCfg.lowSignalPatterns,
    });

    this.recent.push({ canonical: record.canonical, ts: Date.now() });
    while (this.recent.length > 50) {
      this.recent.shift();
    }
  }
}

// ============================================================================
// Plugin Definition
// ============================================================================

const cortexPlugin = {
  id: "openclaw-cortex",
  name: "Cortex Memory",
  description: "Local-first AI memory with hybrid search, fact extraction, and confidence decay. Zero cloud dependencies.",
  kind: "extension" as const,

  register(api: OpenClawPluginApi) {
    const cfg = parseConfig(api.pluginConfig);
    const cli = new CortexCLI(cfg.binaryPath, cfg.dbPath, cfg.embedProvider, cfg.searchMode, api.logger);
    const captureHygiene = new CaptureHygiene(cli, api.logger, cfg.capture, cfg.extractFacts);

    api.logger.info(`cortex: plugin registered (binary: ${cfg.binaryPath}, db: ${cfg.dbPath}, mode: ${cfg.searchMode})`);

    // ========================================================================
    // Tools
    // ========================================================================

    api.registerTool(
      {
        name: "cortex_search",
        label: "Cortex Search",
        description:
          "Search Cortex memory (local knowledge base). Uses hybrid search (BM25 + semantic) with confidence decay. Use for finding past decisions, preferences, facts, and context.",
        parameters: {
          type: "object",
          properties: {
            query: { type: "string", description: "Search query" },
            limit: { type: "number", description: "Max results (default: 5)" },
            mode: { type: "string", enum: ["hybrid", "bm25", "semantic"], description: "Search mode (default: hybrid)" },
          },
          required: ["query"],
        },
        async execute(_toolCallId, params) {
          const { query, limit = 5, mode } = params as {
            query: string;
            limit?: number;
            mode?: "hybrid" | "bm25" | "semantic";
          };

          const results = await cli.search(query, limit, mode);

          if (results.length === 0) {
            return {
              content: [{ type: "text", text: "No relevant memories found in Cortex." }],
              details: { count: 0 },
            };
          }

          const text = results
            .map((r, i) => {
              const section = r.source_section ? ` [${r.source_section}]` : "";
              return `${i + 1}. ${r.content}${section} (${(r.score * 100).toFixed(0)}% match, ${r.match_type})`;
            })
            .join("\n\n");

          return {
            content: [{ type: "text", text: `Found ${results.length} memories:\n\n${text}` }],
            details: {
              count: results.length,
              results: results.map((r) => ({
                content: r.content,
                source_file: r.source_file,
                source_section: r.source_section,
                score: r.score,
                match_type: r.match_type,
                memory_id: r.memory_id,
              })),
            },
          };
        },
      },
      { name: "cortex_search" },
    );

    api.registerTool(
      {
        name: "cortex_store",
        label: "Cortex Store",
        description:
          "Save information to Cortex memory with automatic fact extraction. Use for important decisions, preferences, facts.",
        parameters: {
          type: "object",
          properties: {
            text: { type: "string", description: "Information to remember" },
            source: { type: "string", description: "Source label (default: manual)" },
            extract: { type: "boolean", description: "Extract facts (default: true)" },
          },
          required: ["text"],
        },
        async execute(_toolCallId, params, context) {
          const { text, source = "manual", extract = true } = params as {
            text: string;
            source?: string;
            extract?: boolean;
          };

          // Build metadata from tool call context (Issue #30)
          const ctx = (context ?? {}) as Record<string, unknown>;
          const metadata: CortexMetadata = {
            timestamp_start: new Date().toISOString(),
          };
          if (typeof ctx.sessionKey === "string") metadata.session_key = ctx.sessionKey;
          if (typeof ctx.channel === "string") metadata.channel = ctx.channel;
          if (typeof ctx.agentId === "string") metadata.agent_id = ctx.agentId;
          if (typeof ctx.model === "string") metadata.model = ctx.model;

          await cli.importText(text, source, extract, metadata);

          return {
            content: [{ type: "text", text: `Stored in Cortex: "${text.slice(0, 100)}..."${extract ? " (facts extracted)" : ""}` }],
            details: { action: "stored", extract },
          };
        },
      },
      { name: "cortex_store" },
    );

    api.registerTool(
      {
        name: "cortex_stats",
        label: "Cortex Stats",
        description: "Show Cortex memory statistics: total memories, facts, confidence distribution, storage size.",
        parameters: { type: "object", properties: {} },
        async execute() {
          const stats = await cli.stats();
          if (!stats) {
            return {
              content: [{ type: "text", text: "Failed to get Cortex stats." }],
              details: { error: "stats_failed" },
            };
          }

          const sizeMB = (stats.storage_bytes / 1024 / 1024).toFixed(1);
          const text = [
            `Memories: ${stats.memories}`,
            `Facts: ${stats.facts}`,
            `Sources: ${stats.sources}`,
            `DB Size: ${sizeMB} MB`,
            `Avg Confidence: ${(stats.avg_confidence * 100).toFixed(1)}%`,
            `Confidence: ${stats.confidence_distribution.high} high / ${stats.confidence_distribution.medium} medium / ${stats.confidence_distribution.low} low`,
            `Types: ${Object.entries(stats.facts_by_type).map(([k, v]) => `${k}(${v})`).join(", ")}`,
          ].join("\n");

          return {
            content: [{ type: "text", text }],
            details: { stats },
          };
        },
      },
      { name: "cortex_stats" },
    );

    api.registerTool(
      {
        name: "cortex_profile",
        label: "Cortex Profile",
        description:
          "Build a user profile from Cortex memories. Aggregates identity, preference, and decision facts.",
        parameters: {
          type: "object",
          properties: {
            query: { type: "string", description: "Focus area for profile (e.g., 'trading', 'wedding')" },
          },
        },
        async execute(_toolCallId, params) {
          const { query = "user preferences identity decisions" } = params as { query?: string };

          const results = await cli.search(query, 10, "hybrid");
          if (results.length === 0) {
            return {
              content: [{ type: "text", text: "No profile data found." }],
              details: { count: 0 },
            };
          }

          const text = results
            .map((r) => `- ${r.content}`)
            .join("\n");

          return {
            content: [{ type: "text", text: `User profile (${results.length} facts):\n\n${text}` }],
            details: { count: results.length },
          };
        },
      },
      { name: "cortex_profile" },
    );

    // ========================================================================
    // CLI Commands
    // ========================================================================

    api.registerCli(
      ({ program }) => {
        const cortex = program.command("cortex").description("Cortex memory plugin commands");

        cortex
          .command("search")
          .description("Search Cortex memories")
          .argument("<query>", "Search query")
          .option("--limit <n>", "Max results", "5")
          .option("--mode <mode>", "Search mode: hybrid, bm25, semantic", cfg.searchMode)
          .action(async (query: string, opts: any) => {
            const results = await cli.search(query, parseInt(opts.limit), opts.mode);
            console.log(JSON.stringify(results, null, 2));
          });

        cortex
          .command("stats")
          .description("Show memory statistics")
          .action(async () => {
            const stats = await cli.stats();
            console.log(JSON.stringify(stats, null, 2));
          });

        cortex
          .command("setup")
          .description("Verify canonical Cortex + OpenClaw setup flow")
          .action(async () => {
            const checks: SetupCheck[] = [];
            const addCheck = (check: SetupCheck) => checks.push(check);

            console.log("Cortex + OpenClaw setup check");
            console.log(`Canonical guide: ${canonicalOpenClawSetupDoc}`);
            console.log(`Binary: ${cfg.binaryPath}`);
            console.log(`DB: ${cfg.dbPath}`);
            console.log(`Embed: ${cfg.embedProvider}`);
            console.log("");

            // Verify OpenClaw config + plugin config placement first.
            const configChecks = await inspectOpenClawConfigPlacement();
            checks.push(...configChecks);

            // Verify configured Cortex binary and version.
            const binaryVersion = await readBinaryVersion(cfg.binaryPath);
            if (!binaryVersion) {
              addCheck({
                name: "cortex_binary",
                status: "fail",
                details: `cannot execute ${cfg.binaryPath}`,
                hint: "Install Cortex from GitHub releases and set plugins.entries.openclaw-cortex.config.binaryPath to the installed binary.",
              });
            } else {
              addCheck({
                name: "cortex_binary",
                status: "pass",
                details: `${cfg.binaryPath} (v${binaryVersion})`,
              });
            }

            // Stale ~/bin/cortex detection + runtime drift against PATH runtime.
            const homeBin = join(homedir(), "bin", "cortex");
            const pathBinary = findBinaryInPath("cortex");
            const configuredBinaryForCompare = cfg.binaryPath === "cortex" && pathBinary ? pathBinary : cfg.binaryPath;
            const homeBinVersion = existsSync(homeBin) ? await readBinaryVersion(homeBin) : null;
            const pathBinaryVersion = pathBinary ? await readBinaryVersion(pathBinary) : null;

            if (existsSync(homeBin) && pathBinary && pathBinary !== homeBin && homeBinVersion && pathBinaryVersion && compareSemver(homeBinVersion, pathBinaryVersion) < 0) {
              addCheck({
                name: "stale_home_bin",
                status: "warn",
                details: `~/bin/cortex is stale (v${homeBinVersion}) vs PATH cortex (v${pathBinaryVersion} at ${pathBinary})`,
                hint: "Update or remove stale ~/bin/cortex, or point binaryPath to the intended runtime to avoid drift.",
              });
            } else {
              addCheck({
                name: "stale_home_bin",
                status: "pass",
                details: "no stale ~/bin/cortex drift detected",
              });
            }

            if (binaryVersion && compareSemver(binaryVersion, minimumRecommendedCortexVersion) < 0) {
              addCheck({
                name: "runtime_drift",
                status: "warn",
                details: `plugin is running against Cortex v${binaryVersion} (< recommended v${minimumRecommendedCortexVersion})`,
                hint: "Upgrade Cortex runtime (brew upgrade hurttlocker/cortex/cortex-memory or latest GitHub release).",
              });
            } else if (binaryVersion) {
              addCheck({
                name: "runtime_drift",
                status: "pass",
                details: `runtime version is current enough (v${binaryVersion})`,
              });
            }

            if (binaryVersion && pathBinaryVersion && pathBinary && configuredBinaryForCompare !== pathBinary && compareSemver(binaryVersion, pathBinaryVersion) !== 0) {
              addCheck({
                name: "binary_path_drift",
                status: "warn",
                details: `configured binary (${cfg.binaryPath} v${binaryVersion}) differs from PATH cortex (${pathBinary} v${pathBinaryVersion})`,
                hint: "Use one canonical binary path in plugin config and shell PATH to avoid inconsistent setup/doctor output.",
              });
            } else {
              addCheck({
                name: "binary_path_drift",
                status: "pass",
                details: "configured binary and PATH runtime are aligned",
              });
            }

            // Pull actionable checks from cortex doctor.
            const doctor = await cli.doctor();
            if (!doctor) {
              addCheck({
                name: "doctor",
                status: "fail",
                details: "failed to run `cortex doctor --json`",
                hint: "Run `cortex doctor` directly to inspect errors, then rerun `openclaw cortex setup`.",
              });
            } else {
              const interesting = ["database_path", "database_open", "database_stats", "embeddings", "embed_config", "hnsw_index", "version"];
              for (const key of interesting) {
                const found = findDoctorCheck(doctor, key);
                if (!found) continue;
                addCheck({
                  name: `doctor_${key}`,
                  status: found.status,
                  details: found.details,
                  hint: found.hint,
                });
              }

              const hnswCheck = findDoctorCheck(doctor, "hnsw_index");
              if (hnswCheck && hnswCheck.status !== "pass") {
                addCheck({
                  name: "hnsw_rebuild_health",
                  status: "warn",
                  details: "ANN index is missing or unhealthy for semantic/hybrid retrieval",
                  hint: "Run `cortex index` to rebuild HNSW, then rerun `openclaw cortex setup`.",
                });
              } else {
                addCheck({
                  name: "hnsw_rebuild_health",
                  status: "pass",
                  details: "HNSW health check passed",
                });
              }
            }

            // Optional connector summary (non-blocking visibility)
            try {
              const { stdout } = await execFileAsync(cfg.binaryPath, ["connect", "status"], {
                timeout: 10_000,
                env: { ...process.env, CORTEX_DB: cfg.dbPath },
              });
              console.log("📡 Connectors:");
              console.log(stdout.trim() || "  No connectors configured");
            } catch {
              console.log("📡 Connectors: none configured");
            }

            console.log("\nSetup checks:");
            for (const check of checks) {
              console.log(`  ${iconForSetupStatus(check.status)} ${check.name}: ${check.details}`);
              if (check.hint && check.status !== "pass") {
                console.log(`     next: ${check.hint}`);
              }
            }

            const summary = checks.reduce(
              (acc, c) => {
                if (c.status === "pass") acc.pass += 1;
                else if (c.status === "warn") acc.warn += 1;
                else acc.fail += 1;
                return acc;
              },
              { pass: 0, warn: 0, fail: 0 },
            );

            console.log(`\nSummary: ${summary.pass} pass, ${summary.warn} warn, ${summary.fail} fail`);
            if (summary.fail > 0) {
              console.log("\nFix the failing checks above, then rerun: openclaw cortex setup");
              process.exitCode = 1;
            } else if (summary.warn > 0) {
              console.log("\nSetup is usable, but warnings should be resolved for a stable happy-path install.");
            } else {
              console.log("\n✅ Happy-path setup verified. You can now run OpenClaw with Cortex recall/capture enabled.");
            }
          });

        // --- Connect subcommands ---

        const connectCmd = cortex.command("connect").description("Manage Cortex Connect integrations");

        connectCmd
          .command("status")
          .description("Show connector status")
          .action(async () => {
            try {
              const { stdout } = await execFileAsync(cfg.binaryPath, ["connect", "status"], {
                timeout: 15000,
                env: { ...process.env, CORTEX_DB: cfg.dbPath },
              });
              console.log(stdout.trim());
            } catch (err: any) {
              console.error(`Connect status failed: ${err.message}`);
            }
          });

        connectCmd
          .command("sync")
          .description("Sync connectors (with fact extraction)")
          .argument("[provider]", "Specific provider to sync (omit for all)")
          .option("--no-extract", "Skip fact extraction on imported records")
          .option("--no-infer", "Skip edge inference after extraction")
          .action(async (provider?: string, options?: { extract?: boolean; infer?: boolean }) => {
            try {
              const args = ["connect", "sync"];
              if (provider) {
                args.push("--provider", provider);
              } else {
                args.push("--all");
              }
              // Default: extract facts (use --no-extract to disable)
              if (options?.extract !== false) {
                args.push("--extract");
              }
              if (options?.infer === false) {
                args.push("--no-infer");
              }
              const { stdout } = await execFileAsync(cfg.binaryPath, args, {
                timeout: 120000,
                env: { ...process.env, CORTEX_DB: cfg.dbPath },
              });
              console.log(stdout.trim());
            } catch (err: any) {
              console.error(`Connect sync failed: ${err.message}`);
            }
          });

        connectCmd
          .command("providers")
          .description("List available connector providers")
          .action(async () => {
            try {
              const { stdout } = await execFileAsync(cfg.binaryPath, ["connect", "providers"], {
                timeout: 10000,
                env: { ...process.env, CORTEX_DB: cfg.dbPath },
              });
              console.log(stdout.trim());
            } catch (err: any) {
              console.error(`Connect providers failed: ${err.message}`);
            }
          });
      },
      { commands: ["cortex"] },
    );

    // ========================================================================
    // Lifecycle Hooks — Auto-Recall
    // ========================================================================

    if (cfg.autoRecall) {
      api.on("before_agent_start", async (event, ctx) => {
        if (!event.prompt || event.prompt.length < 10) return;

        try {
          const compactionMode = isCompactionLikePrompt(event.prompt);
          const fetchLimit = Math.max(cfg.recallLimit, cfg.recallLimit * 3);
          const now = new Date();
          const todayKey = formatDateYYYYMMDD(now);
          const yesterday = new Date(now);
          yesterday.setDate(yesterday.getDate() - 1);
          const yesterdayKey = formatDateYYYYMMDD(yesterday);

          const baseSearchOptions: CortexSearchOptions = {
            boostAgent: typeof ctx?.agentId === "string" ? ctx.agentId : undefined,
            boostChannel: typeof ctx?.channelId === "string" ? ctx.channelId : undefined,
            boostSessionKey: typeof ctx?.sessionKey === "string" ? ctx.sessionKey : undefined,
          };

          const mergedRawResults: CortexSearchResult[] = [];
          const seenMemoryIds = new Set<number>();
          const appendResults = (rows: CortexSearchResult[]) => {
            for (const row of rows) {
              if (seenMemoryIds.has(row.memory_id)) continue;
              seenMemoryIds.add(row.memory_id);
              mergedRawResults.push(row);
            }
          };

          if (typeof ctx?.sessionKey === "string" && ctx.sessionKey.trim() !== "") {
            appendResults(
              await cli.search(event.prompt, fetchLimit, cfg.searchMode, cfg.minScore, {
                ...baseSearchOptions,
                sessionKey: ctx.sessionKey,
                after: compactionMode ? todayKey : undefined,
              }),
            );

            if (mergedRawResults.length === 0 && compactionMode) {
              appendResults(
                await cli.search(event.prompt, fetchLimit, cfg.searchMode, cfg.minScore, {
                  ...baseSearchOptions,
                  sessionKey: ctx.sessionKey,
                  after: yesterdayKey,
                }),
              );
            }
          }

          if (mergedRawResults.length < cfg.recallLimit) {
            appendResults(
              await cli.search(event.prompt, fetchLimit, cfg.searchMode, cfg.minScore, {
                ...baseSearchOptions,
                agent: typeof ctx?.agentId === "string" ? ctx.agentId : undefined,
                after: compactionMode ? yesterdayKey : undefined,
              }),
            );
          }

          if (mergedRawResults.length === 0) return;

          const deduped = cfg.recallDedupe.enabled
            ? dedupeRecallResults(mergedRawResults, cfg.recallDedupe.similarityThreshold)
            : mergedRawResults;

          const recallPlan = buildRecallPlan(mergedRawResults, deduped, {
            limit: compactionMode ? Math.min(cfg.recallLimit, 1) : cfg.recallLimit,
            budgetChars: compactionMode ? Math.min(cfg.recallBudgetChars, 900) : cfg.recallBudgetChars,
          });

          if (recallPlan.selected.length === 0) {
            api.logger.warn(
              `cortex: recall manifest selected 0/${recallPlan.manifest.packed_count} packed groups (raw=${recallPlan.manifest.raw_count}, deduped=${recallPlan.manifest.deduped_count}) under budget ${recallPlan.manifest.budget_chars} chars`,
            );
            return;
          }

          api.logger.info(
            `cortex: recall manifest raw=${recallPlan.manifest.raw_count} deduped=${recallPlan.manifest.deduped_count} packed=${recallPlan.manifest.packed_count} collapsed_hits=${recallPlan.manifest.collapsed_hits}`,
          );
          api.logger.info(
            `cortex: recall manifest budget=${recallPlan.manifest.budget_chars} chars (used=${recallPlan.manifest.context_chars}) selected=${recallPlan.manifest.selected_count} dropped=${recallPlan.manifest.dropped_count}`,
          );
          api.logger.info(
            `cortex: injecting ${recallPlan.selected.length} packed recall entries${compactionMode ? " (compaction-biased)" : ""} (scores: ${recallPlan.selected.map((r) => r.score.toFixed(2)).join(", ")})`,
          );

          return {
            prependContext: recallPlan.context,
          };
        } catch (err: any) {
          api.logger.warn(`cortex: auto-recall failed: ${err.message}`);
        }
      });
    }

    // ========================================================================
    // Lifecycle Hooks — Auto-Capture
    // ========================================================================

    if (cfg.autoCapture) {
      api.on("agent_end", async (event) => {
        if (!event.success || !event.messages || event.messages.length === 0) return;

        try {
          // Extract user and assistant messages from this turn
          let userText = "";
          let assistantText = "";
          let messageCount = 0;

          for (const msg of event.messages) {
            if (!msg || typeof msg !== "object") continue;
            const msgObj = msg as Record<string, unknown>;
            const role = msgObj.role as string;
            const content = msgObj.content;

            let text = "";
            if (typeof content === "string") {
              text = content;
            } else if (Array.isArray(content)) {
              text = content
                .filter((b: any) => b?.type === "text" && typeof b.text === "string")
                .map((b: any) => b.text)
                .join("\n");
            }

            const sanitized = sanitizeCaptureMessage(text);

            if (role === "user") {
              if (shouldCapture(sanitized, cfg.captureMaxChars)) {
                userText = sanitized;
                messageCount++;
              }
            } else if (role === "assistant" && sanitized.length > 20) {
              assistantText = sanitized;
              messageCount++;
            }
          }

          if (!userText && !assistantText) return;

          // Build metadata from session context (Issue #30)
          // OpenClaw's agent_end event exposes session info that we capture
          const ev = event as Record<string, unknown>;
          const metadata: CortexMetadata = {
            timestamp_start: new Date().toISOString(),
            message_count: messageCount,
          };

          // Session key (e.g. "agent:main:main", "agent:sage:main")
          if (typeof ev.sessionKey === "string") metadata.session_key = ev.sessionKey;
          else if (typeof ev.session_key === "string") metadata.session_key = ev.session_key;

          // Channel (e.g. "discord", "telegram", "signal")
          if (typeof ev.channel === "string") metadata.channel = ev.channel;
          if (typeof ev.channelId === "string") metadata.channel_id = ev.channelId;
          else if (typeof ev.channel_id === "string") metadata.channel_id = ev.channel_id;
          if (typeof ev.channelName === "string") metadata.channel_name = ev.channelName;
          else if (typeof ev.channel_name === "string") metadata.channel_name = ev.channel_name;

          // Agent info
          if (typeof ev.agentId === "string") metadata.agent_id = ev.agentId;
          else if (typeof ev.agent_id === "string") metadata.agent_id = ev.agent_id;
          if (typeof ev.agentName === "string") metadata.agent_name = ev.agentName;
          else if (typeof ev.agent_name === "string") metadata.agent_name = ev.agent_name;

          // Model
          if (typeof ev.model === "string") metadata.model = ev.model;

          // Token usage
          if (typeof ev.inputTokens === "number") metadata.input_tokens = ev.inputTokens;
          else if (typeof ev.input_tokens === "number") metadata.input_tokens = ev.input_tokens;
          if (typeof ev.outputTokens === "number") metadata.output_tokens = ev.outputTokens;
          else if (typeof ev.output_tokens === "number") metadata.output_tokens = ev.output_tokens;

          // Surface and chat type
          if (typeof ev.surface === "string") metadata.surface = ev.surface;
          if (typeof ev.chatType === "string") metadata.chat_type = ev.chatType;
          else if (typeof ev.chat_type === "string") metadata.chat_type = ev.chat_type;

          metadata.timestamp_end = new Date().toISOString();

          const safeUser = userText || "(no user message)";
          const safeAssistant = assistantText || "(no assistant message)";
          const exchange = formatCapturedExchange(safeUser, safeAssistant, metadata.channel);

          const result = await captureHygiene.ingest({
            text: exchange,
            canonical: `${safeUser}\n${safeAssistant}`,
            metadata,
            source: "auto-capture",
            createdAtMs: Date.now(),
            updatedAtMs: Date.now(),
            charCount: exchange.length,
            segmentCount: 1,
          });

          const metaFields = Object.keys(metadata).filter(
            (k) => metadata[k as keyof CortexMetadata] !== undefined,
          ).length;

          if (result.status === "captured") {
            api.logger.info(
              `cortex: auto-captured exchange (${userText.length + assistantText.length} chars, ${metaFields} metadata fields)`,
            );
          } else if (result.status === "queued") {
            api.logger.info(
              `cortex: queued capture for coalescing (${result.coalescedSegments ?? 1} segment(s) in burst window)`,
            );
          } else if (result.status === "skipped_low_signal") {
            api.logger.info("cortex: skipped low-signal capture");
          } else if (result.status === "skipped_near_duplicate") {
            api.logger.info(`cortex: skipped near-duplicate capture (similarity=${(result.similarity ?? 0).toFixed(3)})`);
          }
        } catch (err: any) {
          api.logger.warn(`cortex: auto-capture failed: ${err.message}`);
        }
      });
    }

    // ========================================================================
    // Service
    // ========================================================================

    api.registerService({
      id: "cortex",
      start: async () => {
        // Verify Cortex is working on startup
        try {
          const stats = await cli.stats();
          if (stats) {
            api.logger.info(
              `cortex: ready — ${stats.memories} memories, ${stats.facts} facts, ${(stats.storage_bytes / 1024 / 1024).toFixed(1)} MB`,
            );
          }
        } catch (err: any) {
          api.logger.warn(`cortex: binary not found at ${cfg.binaryPath} — install from https://github.com/hurttlocker/cortex/releases`);
        }
      },
      stop: async () => {
        try {
          await captureHygiene.flushPending();
        } catch (err: any) {
          api.logger.warn(`cortex: failed to flush pending capture on stop: ${err?.message ?? err}`);
        }
        api.logger.info("cortex: stopped");
      },
    });
  },
};

export default cortexPlugin;
