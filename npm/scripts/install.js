#!/usr/bin/env node
/**
 * Post-install script for @cortex-ai/mcp
 *
 * Downloads the platform-specific Cortex binary from GitHub releases.
 * Falls back gracefully if download fails (user can provide their own binary).
 */

const { execSync } = require("child_process");
const fs = require("fs");
const path = require("path");
const https = require("https");
const os = require("os");

const VERSION = "v0.4.0";
const REPO = "hurttlocker/cortex";
const BIN_DIR = path.join(__dirname, "..", "bin");
const BIN_NAME = os.platform() === "win32" ? "cortex.exe" : "cortex";
const BIN_PATH = path.join(BIN_DIR, BIN_NAME);

/**
 * Map Node.js platform/arch to goreleaser artifact names.
 */
function getArtifactName() {
  const platform = os.platform();
  const arch = os.arch();

  const platformMap = {
    darwin: "darwin",
    linux: "linux",
    win32: "windows",
  };

  const archMap = {
    x64: "amd64",
    arm64: "arm64",
  };

  const p = platformMap[platform];
  const a = archMap[arch];

  if (!p || !a) {
    throw new Error(
      `Unsupported platform: ${platform}/${arch}. ` +
        `Supported: darwin/linux/windows on amd64/arm64.`
    );
  }

  const ext = platform === "win32" ? ".zip" : ".tar.gz";
  return `cortex_${p}_${a}${ext}`;
}

/**
 * Download a file from URL, following redirects.
 */
function download(url) {
  return new Promise((resolve, reject) => {
    const get = (url, redirects = 0) => {
      if (redirects > 5) return reject(new Error("Too many redirects"));

      https
        .get(url, { headers: { "User-Agent": "cortex-mcp-installer" } }, (res) => {
          if (res.statusCode >= 300 && res.statusCode < 400 && res.headers.location) {
            return get(res.headers.location, redirects + 1);
          }
          if (res.statusCode !== 200) {
            return reject(new Error(`HTTP ${res.statusCode} for ${url}`));
          }

          const chunks = [];
          res.on("data", (chunk) => chunks.push(chunk));
          res.on("end", () => resolve(Buffer.concat(chunks)));
          res.on("error", reject);
        })
        .on("error", reject);
    };
    get(url);
  });
}

/**
 * Extract the cortex binary from a tar.gz archive.
 */
function extractTarGz(buffer, destDir) {
  const tmpFile = path.join(os.tmpdir(), `cortex-${Date.now()}.tar.gz`);
  fs.writeFileSync(tmpFile, buffer);
  try {
    execSync(`tar xzf "${tmpFile}" -C "${destDir}" --strip-components=0`, {
      stdio: "pipe",
    });
  } finally {
    fs.unlinkSync(tmpFile);
  }
}

/**
 * Extract the cortex binary from a zip archive (Windows).
 */
function extractZip(buffer, destDir) {
  const tmpFile = path.join(os.tmpdir(), `cortex-${Date.now()}.zip`);
  fs.writeFileSync(tmpFile, buffer);
  try {
    // PowerShell for Windows
    execSync(
      `powershell -Command "Expand-Archive -Path '${tmpFile}' -DestinationPath '${destDir}' -Force"`,
      { stdio: "pipe" }
    );
  } finally {
    fs.unlinkSync(tmpFile);
  }
}

async function main() {
  // Skip if binary already exists (e.g., manual install)
  if (fs.existsSync(BIN_PATH)) {
    console.log(`✓ Cortex binary already exists at ${BIN_PATH}`);
    return;
  }

  // Skip if cortex is already in PATH
  try {
    const existing = execSync("which cortex 2>/dev/null || where cortex 2>NUL", {
      encoding: "utf-8",
      stdio: ["pipe", "pipe", "pipe"],
    }).trim();
    if (existing) {
      console.log(`✓ Cortex found in PATH: ${existing}`);
      // Symlink to it
      fs.mkdirSync(BIN_DIR, { recursive: true });
      fs.symlinkSync(existing, BIN_PATH);
      return;
    }
  } catch {
    // Not in PATH, proceed with download
  }

  const artifact = getArtifactName();
  const url = `https://github.com/${REPO}/releases/download/${VERSION}/${artifact}`;

  console.log(`⬇ Downloading Cortex ${VERSION} for ${os.platform()}/${os.arch()}...`);
  console.log(`  ${url}`);

  try {
    const buffer = await download(url);
    console.log(`  Downloaded ${(buffer.length / 1024 / 1024).toFixed(1)} MB`);

    fs.mkdirSync(BIN_DIR, { recursive: true });

    if (artifact.endsWith(".tar.gz")) {
      extractTarGz(buffer, BIN_DIR);
    } else {
      extractZip(buffer, BIN_DIR);
    }

    // Find the binary in extracted files
    const candidates = ["cortex", "cortex.exe"];
    let found = false;
    for (const name of candidates) {
      const candidate = path.join(BIN_DIR, name);
      if (fs.existsSync(candidate)) {
        fs.chmodSync(candidate, 0o755);
        found = true;
        break;
      }
    }

    // Check nested directory (goreleaser creates cortex_platform_arch/)
    if (!found) {
      const dirs = fs.readdirSync(BIN_DIR).filter((d) =>
        fs.statSync(path.join(BIN_DIR, d)).isDirectory()
      );
      for (const dir of dirs) {
        for (const name of candidates) {
          const candidate = path.join(BIN_DIR, dir, name);
          if (fs.existsSync(candidate)) {
            // Move to bin root
            fs.renameSync(candidate, path.join(BIN_DIR, name));
            fs.chmodSync(path.join(BIN_DIR, name), 0o755);
            // Clean up directory
            fs.rmSync(path.join(BIN_DIR, dir), { recursive: true, force: true });
            found = true;
            break;
          }
        }
        if (found) break;
      }
    }

    if (!found) {
      console.error("⚠ Could not find cortex binary in downloaded archive.");
      console.error("  Contents:", fs.readdirSync(BIN_DIR));
      process.exit(1);
    }

    console.log(`✓ Cortex ${VERSION} installed to ${BIN_PATH}`);
  } catch (err) {
    console.error(`⚠ Failed to download Cortex binary: ${err.message}`);
    console.error("  You can install manually: https://github.com/hurttlocker/cortex/releases");
    console.error("  Or: go install github.com/hurttlocker/cortex@latest");
    // Don't fail the install — user might have cortex elsewhere
    process.exit(0);
  }
}

main();
