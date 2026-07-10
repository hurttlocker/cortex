import test from "node:test";
import assert from "node:assert/strict";

import { dedupeRecallResults, isLowSignalMessage, sanitizeCaptureMessage } from "./hygiene.ts";

test("isLowSignalMessage filters trivial acknowledgements and HEARTBEAT_OK", () => {
  assert.equal(isLowSignalMessage("HEARTBEAT_OK", [], 20), true);
  assert.equal(isLowSignalMessage("Fire the test", [], 20), true);
  assert.equal(isLowSignalMessage("ok", [], 20), true);
  assert.equal(isLowSignalMessage("Q prefers Sonnet for coding tasks", [], 20), false);
});

test("sanitizeCaptureMessage strips metadata wrappers and memory context blocks", () => {
  const raw = `
<cortex-memories>
old recall
</cortex-memories>

Conversation info (untrusted metadata):
\`\`\`json
{"conversation_label":"Guild #mister"}
\`\`\`

Sender (untrusted metadata):
\`\`\`json
{"name":"cashcoldgame"}
\`\`\`

What needs improvement now?
`;
  const sanitized = sanitizeCaptureMessage(raw);
  assert.equal(sanitized.includes("cortex-memories"), false);
  assert.equal(sanitized.includes("untrusted metadata"), false);
  assert.equal(sanitized, "What needs improvement now?");
});

test("isLowSignalMessage respects important tags", () => {
  assert.equal(isLowSignalMessage("#important ok", [], 20), false);
});

test("dedupeRecallResults removes exact and near duplicates", () => {
  const deduped = dedupeRecallResults(
    [
      {
        content: "Fire the test",
        source_file: "a.md",
        source_line: 1,
        source_section: "s",
        score: 0.9,
        match_type: "hybrid",
        memory_id: 1,
      },
      {
        content: "fire   the test",
        source_file: "b.md",
        source_line: 1,
        source_section: "s",
        score: 0.88,
        match_type: "hybrid",
        memory_id: 2,
      },
      {
        content: "Q prefers Sonnet for coding tasks",
        source_file: "c.md",
        source_line: 1,
        source_section: "s",
        score: 0.8,
        match_type: "hybrid",
        memory_id: 3,
      },
    ],
    0.95,
  );

  assert.equal(deduped.length, 2);
  assert.equal(deduped[0].memory_id, 1);
  assert.equal(deduped[1].memory_id, 3);
});
