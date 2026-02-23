#!/usr/bin/env node
/**
 * cortex-mcp — Zero-config MCP memory server for AI agents.
 *
 * Usage:
 *   npx @cortex-ai/mcp              # Start MCP server (stdio)
 *   npx @cortex-ai/mcp --port 8080  # Start MCP server (HTTP+SSE)
 *   npx @cortex-ai/mcp --help       # Show help
 *
 * MCP Config (Claude Desktop, Cursor, etc.):
 *   {
 *     "mcpServers": {
 *       "cortex": {
 *         "command": "npx",
 *         "args": ["-y", "@cortex-ai/mcp"]
 *       }
 *     }
 *   }
 */

const { spawn, execSync } = require("child_process");
const path = require("path");
const fs = require("fs");
const os = require("os");

const BIN_NAME = os.platform() === "win32" ? "cortex.exe" : "cortex";

/**
 * Find the cortex binary. Priority:
 * 1. Bundled binary (from postinstall download)
 * 2. System PATH
 * 3. Common install locations
 */
function findBinary() {
  // 1. Bundled
  const bundled = path.join(__dirname, BIN_NAME);
  if (fs.existsSync(bundled)) return bundled;

  // 2. PATH
  try {
    const cmd = os.platform() === "win32" ? "where cortex" : "which cortex";
    const found = execSync(cmd, { encoding: "utf-8", stdio: ["pipe", "pipe", "pipe"] }).trim();
    if (found) return found.split("\n")[0];
  } catch {
    // Not in PATH
  }

  // 3. Common locations
  const common = [
    path.join(os.homedir(), "bin", "cortex"),
    "/usr/local/bin/cortex",
    "/opt/homebrew/bin/cortex",
  ];
  for (const p of common) {
    if (fs.existsSync(p)) return p;
  }

  return null;
}

function main() {
  const binary = findBinary();

  if (!binary) {
    console.error("Error: Cortex binary not found.");
    console.error("");
    console.error("Install options:");
    console.error("  brew install hurttlocker/cortex/cortex-memory");
    console.error("  go install github.com/hurttlocker/cortex@latest");
    console.error("  Download from: https://github.com/hurttlocker/cortex/releases");
    process.exit(1);
  }

  // Pass all args to `cortex mcp`
  const args = ["mcp", ...process.argv.slice(2)];

  const child = spawn(binary, args, {
    stdio: "inherit",
    env: {
      ...process.env,
      // Ensure DB path is set to default if not overridden
      CORTEX_DB: process.env.CORTEX_DB || path.join(os.homedir(), ".cortex", "cortex.db"),
    },
  });

  child.on("error", (err) => {
    console.error(`Failed to start Cortex: ${err.message}`);
    process.exit(1);
  });

  child.on("exit", (code) => {
    process.exit(code || 0);
  });

  // Forward signals
  for (const sig of ["SIGINT", "SIGTERM", "SIGHUP"]) {
    process.on(sig, () => child.kill(sig));
  }
}

main();
