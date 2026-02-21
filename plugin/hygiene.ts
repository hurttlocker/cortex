export interface RecallResultLike {
  content: string;
}

const memoryContextBlockRE = /<(cortex-memories|relevant-memories)>[\s\S]*?<\/\1>/gi;
const untrustedMetadataBlockRE = /(Conversation info|Sender)\s*\(untrusted metadata\):\s*```(?:json)?[\s\S]*?```/gi;
const queuedEnvelopeLineRE = /^\[Queued messages while agent was busy\]\s*$/gim;
const queuedSeparatorRE = /^---\s*\nQueued\s*#\d+\s*$/gim;

/**
 * Removes boilerplate wrappers that pollute capture quality but preserves the actual user ask.
 */
export function sanitizeCaptureMessage(text: string): string {
  if (!text) return "";

  let out = text;
  out = out.replace(memoryContextBlockRE, " ");
  out = out.replace(untrustedMetadataBlockRE, " ");
  out = out.replace(queuedEnvelopeLineRE, " ");
  out = out.replace(queuedSeparatorRE, " ");

  // Collapse excessive whitespace while preserving paragraph boundaries.
  out = out
    .replace(/\r\n/g, "\n")
    .replace(/\n{3,}/g, "\n\n")
    .replace(/[ \t]{2,}/g, " ")
    .trim();

  return out;
}

export function normalizeCaptureText(text: string): string {
  return sanitizeCaptureMessage(text)
    .toLowerCase()
    .replace(/[^a-z0-9\s]/g, " ")
    .replace(/\s+/g, " ")
    .trim();
}

function wordFreq(text: string): Map<string, number> {
  const out = new Map<string, number>();
  for (const token of normalizeCaptureText(text).split(" ")) {
    if (!token || token.length < 2) continue;
    out.set(token, (out.get(token) ?? 0) + 1);
  }
  return out;
}

export function cosineSimilarity(a: string, b: string): number {
  const av = wordFreq(a);
  const bv = wordFreq(b);
  if (av.size === 0 || bv.size === 0) return 0;

  let dot = 0;
  let normA = 0;
  let normB = 0;

  for (const [token, v] of av.entries()) {
    dot += v * (bv.get(token) ?? 0);
    normA += v * v;
  }
  for (const v of bv.values()) {
    normB += v * v;
  }

  if (normA === 0 || normB === 0) return 0;
  return dot / (Math.sqrt(normA) * Math.sqrt(normB));
}

function hasImportantTag(text: string): boolean {
  return /(#[ ]?important|\[important\]|important:|!important)/i.test(text);
}

export function isLowSignalMessage(text: string, patterns: string[], minCaptureChars: number): boolean {
  if (!text || hasImportantTag(text)) return false;
  const normalized = normalizeCaptureText(text);
  if (!normalized) return true;

  if (normalized.length < Math.max(1, minCaptureChars)) {
    return true;
  }

  if (normalized.split(" ").length > 8) return false;

  const normalizedPatterns = patterns.map((p) => normalizeCaptureText(p)).filter(Boolean);
  if (normalizedPatterns.includes(normalized)) return true;

  // Common one-liners, acknowledgements, and trivial command-like utterances.
  return /^(ok|okay|yes|yep|got it|sounds good|sure|thanks|thank you|cool|heartbeat ok|fire the test|run test|do it)$/i.test(normalized);
}

export function dedupeRecallResults<T extends RecallResultLike>(results: T[], similarityThreshold: number): T[] {
  const kept: T[] = [];

  for (const candidate of results) {
    const candidateNorm = normalizeCaptureText(candidate.content);
    if (!candidateNorm) continue;

    let duplicate = false;
    for (const existing of kept) {
      const existingNorm = normalizeCaptureText(existing.content);
      if (!existingNorm) continue;

      if (existingNorm === candidateNorm) {
        duplicate = true;
        break;
      }

      const sim = cosineSimilarity(candidateNorm, existingNorm);
      if (sim >= similarityThreshold) {
        duplicate = true;
        break;
      }
    }

    if (!duplicate) {
      kept.push(candidate);
    }
  }

  return kept;
}
