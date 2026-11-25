#!/usr/bin/env node
/**
 * Bump or verify the 接上AI desktop client version across all 5 source files
 * atomically so Tauri, Cargo.lock, package.json, and the Go engine never drift.
 *
 * Usage:
 *   node scripts/bump-client-version.mjs --check
 *   node scripts/bump-client-version.mjs 0.2.0-beta.12
 */
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const SEMVER_RE = /^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$/;

const FILES = [
  {
    rel: "client/desktop/package.json",
    read(text) {
      return JSON.parse(text).version;
    },
    write(text, version) {
      const next = text.replace(
        /("version"\s*:\s*")([^"]+)(")/,
        `$1${version}$3`,
      );
      if (next === text && JSON.parse(text).version !== version) {
        throw new Error("Could not locate version field in package.json");
      }
      return next;
    },
  },
  {
    rel: "client/desktop/src-tauri/tauri.conf.json",
    read(text) {
      return JSON.parse(text).version;
    },
    write(text, version) {
      const next = text.replace(
        /("version"\s*:\s*")([^"]+)(")/,
        `$1${version}$3`,
      );
      if (next === text && JSON.parse(text).version !== version) {
        throw new Error("Could not locate version field in tauri.conf.json");
      }
      return next;
    },
  },
  {
    rel: "client/desktop/src-tauri/Cargo.toml",
    read(text) {
      const m = /^\[package\][\s\S]*?^version\s*=\s*"([^"]+)"/m.exec(text);
      return m ? m[1] : "";
    },
    write(text, version) {
      const re = /(^\[package\][\s\S]*?^version\s*=\s*")([^"]+)(")/m;
      if (!re.test(text)) {
        throw new Error("Could not locate [package] version in Cargo.toml");
      }
      return text.replace(re, `$1${version}$3`);
    },
  },
  {
    rel: "client/desktop/src-tauri/Cargo.lock",
    read(text) {
      const m = /\[\[package\]\]\r?\nname = "jieshangai"\r?\nversion = "([^"]+)"/.exec(text);
      return m ? m[1] : "";
    },
    write(text, version) {
      const re = /(\[\[package\]\]\r?\nname = "jieshangai"\r?\nversion = ")([^"]+)(")/;
      if (!re.test(text)) {
        throw new Error('Could not locate [[package]] name = "jieshangai" in Cargo.lock');
      }
      return text.replace(re, `$1${version}$3`);
    },
  },
  {
    rel: "client/runtime/cmd/jieshangai-engine/main.go",
    read(text) {
      const m = /^var version = "([^"]+)"/m.exec(text);
      return m ? m[1] : "";
    },
    write(text, version) {
      const re = /(^var version = ")([^"]+)(")/m;
      if (!re.test(text)) {
        throw new Error("Could not locate var version in main.go");
      }
      return text.replace(re, `$1${version}$3`);
    },
  },
];

function readVersions() {
  return FILES.map((entry) => {
    const abs = path.join(repoRoot, entry.rel);
    const text = fs.readFileSync(abs, "utf8");
    const version = entry.read(text);
    return { ...entry, abs, text, version };
  });
}

function main() {
  const arg = (process.argv[2] || "").trim();
  if (!arg || arg === "-h" || arg === "--help") {
    console.log("Usage:");
    console.log("  node scripts/bump-client-version.mjs --check");
    console.log("  node scripts/bump-client-version.mjs <version>   (e.g. 0.2.0-beta.12)");
    process.exit(arg ? 0 : 1);
  }

  const records = readVersions();

  if (arg === "--check") {
    const unique = new Set(records.map((r) => r.version));
    if (unique.size !== 1 || !records[0].version || !SEMVER_RE.test(records[0].version)) {
      console.error("Error: client desktop version mismatch across files:");
      for (const r of records) {
        console.error(`  - ${r.rel}: ${r.version || "<missing>"}`);
      }
      process.exit(1);
    }
    console.log(`[ok] All ${records.length} client version files match: ${records[0].version}`);
    return;
  }

  const nextVersion = arg.replace(/^v/, "");
  if (!SEMVER_RE.test(nextVersion)) {
    console.error(`Error: invalid SemVer version "${arg}" (expected e.g. 0.2.0-beta.12 or 1.0.0)`);
    process.exit(1);
  }

  // Prepare all replacements in memory first so we never leave partial updates.
  const prepared = records.map((r) => ({
    ...r,
    nextText: r.write(r.text, nextVersion),
  }));

  for (const r of prepared) {
    if (r.nextText !== r.text) {
      fs.writeFileSync(r.abs, r.nextText, "utf8");
      console.log(`[updated] ${r.rel}: ${r.version} -> ${nextVersion}`);
    } else {
      console.log(`[unchanged] ${r.rel}: ${nextVersion}`);
    }
  }

  // Re-verify all files read back identically.
  const verified = readVersions();
  if (verified.some((r) => r.version !== nextVersion)) {
    console.error("Error: post-write verification failed.");
    process.exit(1);
  }
  console.log(`\n[done] Client version synced to ${nextVersion}.`);
  console.log(`Next: commit & push, then run GitHub Action "接上AI Desktop" or push tag:`);
  console.log(`  git tag client-v${nextVersion} && git push origin client-v${nextVersion}`);
}

main();
