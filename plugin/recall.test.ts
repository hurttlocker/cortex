import test from "node:test";
import assert from "node:assert/strict";

import { buildRecallPlan, formatRecallContext, type RecallResult } from "./recall.ts";

function makeResult(overrides: Partial<RecallResult>): RecallResult {
  return {
    content: "default memory content",
    source_file: "project-a/memory.md",
    source_line: 1,
    source_section: "section",
    score: 0.8,
    match_type: "hybrid",
    memory_id: 1,
    ...overrides,
  };
}

test("buildRecallPlan deterministically ranks ties across grouped sources", () => {
  const raw = [
    makeResult({ memory_id: 22, score: 0.91, content: "second", source_file: "beta/b.md", source_section: "s1" }),
    makeResult({ memory_id: 11, score: 0.91, content: "first", source_file: "alpha/a.md", source_section: "s1" }),
  ];

  const plan = buildRecallPlan(raw, raw, { limit: 3, budgetChars: 3000 });
  assert.deepEqual(plan.selected.map((r) => r.memory_id), [11, 22]);
  assert.equal(plan.manifest.selected_count, 2);
  assert.equal(plan.manifest.dropped_count, 0);
});

test("buildRecallPlan collapses same source hits with provenance-safe metadata", () => {
  const raw = [
    makeResult({
      memory_id: 1,
      score: 0.95,
      source_file: "project-alpha/notes.md",
      source_section: "runbook",
      content: "Primary recall hit from runbook",
    }),
    makeResult({
      memory_id: 2,
      score: 0.92,
      source_file: "project-alpha/notes.md",
      source_section: "runbook",
      content: "Secondary supporting hit from same section",
    }),
    makeResult({
      memory_id: 3,
      score: 0.90,
      source_file: "project-alpha/other.md",
      source_section: "runbook",
      content: "Different file hit",
    }),
  ];

  const plan = buildRecallPlan(raw, raw, { limit: 5, budgetChars: 5000 });

  assert.equal(plan.selected.length, 2);
  assert.equal(plan.selected[0]?.packed_hits, 2);
  assert.deepEqual(plan.selected[0]?.packed_memory_ids, [1, 2]);
  assert.equal(plan.selected[0]?.project, "project-alpha");
  assert.equal(plan.manifest.packed_count, 2);
  assert.equal(plan.manifest.collapsed_hits, 1);
  assert.equal(plan.manifest.selected[0]?.packed_hits, 2);
});

test("buildRecallPlan keeps collapsed marker + secondary detail under long primary snippet", () => {
  const raw = [
    makeResult({
      memory_id: 10,
      score: 0.99,
      source_file: "project-alpha/notes.md",
      source_section: "runbook",
      content: "A".repeat(1200),
    }),
    makeResult({
      memory_id: 11,
      score: 0.95,
      source_file: "project-alpha/notes.md",
      source_section: "runbook",
      content: "secondary critical detail token for collapsed tail visibility",
    }),
  ];

  const plan = buildRecallPlan(raw, raw, { limit: 3, budgetChars: 5000 });
  assert.equal(plan.selected.length, 1);

  const packed = plan.selected[0];
  assert.equal(packed?.packed_hits, 2);
  assert.ok((packed?.content || "").includes("[collapsed 2 same-source hits:"));
  assert.ok((packed?.content || "").includes("secondary"));
  assert.ok((packed?.content || "").length <= 520);
});

test("buildRecallPlan drops deterministically under hard budget", () => {
  const longA = "A".repeat(900);
  const longB = "B".repeat(900);

  const raw = [
    makeResult({ memory_id: 1, score: 0.95, content: longA, source_file: "alpha/a.md", source_section: "alpha" }),
    makeResult({ memory_id: 2, score: 0.94, content: longB, source_file: "beta/b.md", source_section: "beta" }),
  ];

  const plan = buildRecallPlan(raw, raw, { limit: 5, budgetChars: 900 });

  assert.equal(plan.selected.length, 1);
  assert.equal(plan.selected[0]?.memory_id, 1);
  assert.equal(plan.manifest.dropped_count, 1);
  assert.equal(plan.manifest.dropped[0]?.memory_id, 2);
  assert.equal(plan.manifest.dropped[0]?.reason, "budget");
  assert.ok(plan.context.length <= plan.manifest.budget_chars);
});

test("buildRecallPlan applies limit after packed ranking with explicit drop reasons", () => {
  const raw = [
    makeResult({ memory_id: 3, score: 0.93, source_file: "gamma/a.md", source_section: "s" }),
    makeResult({ memory_id: 2, score: 0.92, source_file: "beta/a.md", source_section: "s" }),
    makeResult({ memory_id: 1, score: 0.91, source_file: "alpha/a.md", source_section: "s" }),
  ];

  const plan = buildRecallPlan(raw, raw, { limit: 2, budgetChars: 5000 });
  assert.deepEqual(plan.selected.map((r) => r.memory_id), [3, 2]);
  assert.equal(plan.manifest.selected_count, 2);
  assert.equal(plan.manifest.dropped_count, 1);
  assert.equal(plan.manifest.dropped[0]?.memory_id, 1);
  assert.equal(plan.manifest.dropped[0]?.reason, "limit");
});

test("formatRecallContext keeps wrapper + provenance-safe escaped content", () => {
  const context = formatRecallContext([
    makeResult({
      memory_id: 7,
      source_file: "project-z/file.md",
      content: "heartbeat <metadata>",
      packed_hits: 2,
      packed_memory_ids: [7, 8],
      project: "project-z",
    }),
  ]);
  assert.equal(context.includes("<cortex-memories>"), true);
  assert.equal(context.includes("</cortex-memories>"), true);
  assert.equal(context.includes("&lt;metadata&gt;"), true);
  assert.equal(context.includes("source: project-z/file.md"), true);
  assert.equal(context.includes("packed 2 hits"), true);
});
