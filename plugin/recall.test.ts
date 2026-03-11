import test from "node:test";
import assert from "node:assert/strict";

import { buildRecallPlan, formatRecallContext, type RecallResult } from "./recall.ts";

function makeResult(overrides: Partial<RecallResult>): RecallResult {
  return {
    content: "default memory content",
    source_file: "memory.md",
    source_line: 1,
    source_section: "section",
    score: 0.8,
    match_type: "hybrid",
    memory_id: 1,
    ...overrides,
  };
}

test("buildRecallPlan deterministically ranks ties by memory_id", () => {
  const raw = [
    makeResult({ memory_id: 22, score: 0.91, content: "second" }),
    makeResult({ memory_id: 11, score: 0.91, content: "first" }),
  ];

  const plan = buildRecallPlan(raw, raw, { limit: 3, budgetChars: 3000 });
  assert.deepEqual(plan.selected.map((r) => r.memory_id), [11, 22]);
  assert.equal(plan.manifest.selected_count, 2);
  assert.equal(plan.manifest.dropped_count, 0);
});

test("buildRecallPlan drops deterministically under hard budget", () => {
  const longA = "A".repeat(900);
  const longB = "B".repeat(900);

  const raw = [
    makeResult({ memory_id: 1, score: 0.95, content: longA, source_section: "alpha" }),
    makeResult({ memory_id: 2, score: 0.94, content: longB, source_section: "beta" }),
  ];

  const plan = buildRecallPlan(raw, raw, { limit: 5, budgetChars: 1400 });

  assert.equal(plan.selected.length, 1);
  assert.equal(plan.selected[0].memory_id, 1);
  assert.equal(plan.manifest.dropped_count, 1);
  assert.equal(plan.manifest.dropped[0]?.memory_id, 2);
  assert.equal(plan.manifest.dropped[0]?.reason, "budget");
  assert.ok(plan.context.length <= plan.manifest.budget_chars);
});

test("buildRecallPlan applies limit after ranking with explicit drop reasons", () => {
  const raw = [
    makeResult({ memory_id: 3, score: 0.93 }),
    makeResult({ memory_id: 2, score: 0.92 }),
    makeResult({ memory_id: 1, score: 0.91 }),
  ];

  const plan = buildRecallPlan(raw, raw, { limit: 2, budgetChars: 5000 });
  assert.deepEqual(plan.selected.map((r) => r.memory_id), [3, 2]);
  assert.equal(plan.manifest.selected_count, 2);
  assert.equal(plan.manifest.dropped_count, 1);
  assert.equal(plan.manifest.dropped[0]?.memory_id, 1);
  assert.equal(plan.manifest.dropped[0]?.reason, "limit");
});

test("formatRecallContext keeps expected wrapper shape", () => {
  const context = formatRecallContext([makeResult({ memory_id: 7, content: "heartbeat <metadata>" })]);
  assert.equal(context.includes("<cortex-memories>"), true);
  assert.equal(context.includes("</cortex-memories>"), true);
  assert.equal(context.includes("&lt;metadata&gt;"), true);
});
