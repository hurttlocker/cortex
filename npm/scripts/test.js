#!/usr/bin/env node
/**
 * Quick smoke test for the npm package.
 */

const { execSync } = require("child_process");
const path = require("path");
const fs = require("fs");
const os = require("os");

const BIN_NAME = os.platform() === "win32" ? "cortex.exe" : "cortex";
const BIN_PATH = path.join(__dirname, "..", "bin", BIN_NAME);

function test(name, fn) {
  try {
    fn();
    console.log(`  ✓ ${name}`);
  } catch (err) {
    console.error(`  ✗ ${name}: ${err.message}`);
    process.exit(1);
  }
}

console.log("@cortex-ai/mcp smoke tests\n");

test("cortex-mcp.js exists", () => {
  const binScript = path.join(__dirname, "..", "bin", "cortex-mcp.js");
  if (!fs.existsSync(binScript)) throw new Error("bin/cortex-mcp.js not found");
});

test("package.json valid", () => {
  const pkg = require("../package.json");
  if (!pkg.name) throw new Error("missing name");
  if (!pkg.bin["cortex-mcp"]) throw new Error("missing bin entry");
});

// Only test binary if it's been installed
if (fs.existsSync(BIN_PATH)) {
  test("cortex binary executable", () => {
    const stats = fs.statSync(BIN_PATH);
    if (!(stats.mode & 0o111)) throw new Error("binary not executable");
  });

  test("cortex --version works", () => {
    const version = execSync(`"${BIN_PATH}" --version 2>&1`, {
      encoding: "utf-8",
    }).trim();
    if (!version.includes("cortex")) throw new Error(`unexpected version: ${version}`);
  });
} else {
  console.log("  ⊘ Binary not installed — skipping binary tests");
}

console.log("\n✓ All tests passed");
