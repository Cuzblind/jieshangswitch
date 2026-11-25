#!/usr/bin/env node
/**
 * Assemble desktop client build artifacts into a Caddy/CDN-ready client-dist folder
 * and generate Tauri v2 updater manifest (`updates/stable/latest.json`) atomically.
 *
 * Recognizes BOTH raw Tauri bundle names (`*_x64-setup.exe`, `*.app.tar.gz`) AND
 * CI collector names from `client/desktop/scripts/collect-installer.mjs`
 * (`jieshangai-win-x64.exe`, `jieshangai-win-arm64.exe`, `jieshangai-mac-arm64.app.tar.gz`,
 * `jieshangai-mac-x64.app.tar.gz`, plus matching `.sig` files).
 *
 * Fail-closed by default: refuses to overwrite `latest.json` if any required platform
 * (darwin-aarch64, darwin-x86_64, windows-x86_64, windows-aarch64) is missing unless
 * `--allow-partial` is explicitly passed.
 *
 * Usage:
 *   node scripts/publish-client-dist.mjs \
 *     --version 0.2.0-beta.11 \
 *     --origin https://jieshangai.com \
 *     --in ./artifacts \
 *     --out ./deploy/client-dist \
 *     [--notes "本次更新说明"] \
 *     [--allow-partial]
 */
import fs from "node:fs";
import path from "node:path";

function parseArgs(argv) {
  const args = {};
  for (let i = 0; i < argv.length; i += 1) {
    const key = argv[i];
    if (!key.startsWith("--")) continue;
    const name = key.slice(2);
    const val = argv[i + 1];
    if (!val || val.startsWith("--")) {
      args[name] = true;
    } else {
      args[name] = val;
      i += 1;
    }
  }
  return args;
}

function walkFiles(dir) {
  const out = [];
  if (!fs.existsSync(dir)) return out;
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) out.push(...walkFiles(full));
    else if (entry.isFile()) out.push(full);
  }
  return out;
}

// The collector names its files, while Tauri may only name the parent target
// directory. Prefer the closest architecture label to each payload.
function artifactArch(file, inputDir) {
  const segments = path.relative(inputDir, file).split(path.sep).reverse();
  segments.push(path.basename(inputDir));
  for (const segment of segments) {
    const match = /(?:^|[^a-z0-9])(aarch64|arm64|x86_64|x64)(?=$|[^a-z0-9])/i.exec(segment);
    if (match) return /^(arm64|aarch64)$/i.test(match[1]) ? "arm64" : "x64";
  }
  // --in can also point directly inside a raw Cargo target's release/bundle.
  const triple = /(?:^|[/\\])(aarch64|x86_64)-(?:apple-darwin|pc-windows-msvc)(?:[/\\]|$)/i.exec(inputDir);
  return triple ? (triple[1].toLowerCase() === "aarch64" ? "arm64" : "x64") : null;
}

function copyFile(src, dst) {
  fs.mkdirSync(path.dirname(dst), { recursive: true });
  const tmp = `${dst}.tmp-${process.pid}`;
  fs.copyFileSync(src, tmp);
  fs.renameSync(tmp, dst);
}

function atomicWriteUTF8(dst, content) {
  fs.mkdirSync(path.dirname(dst), { recursive: true });
  const tmp = `${dst}.tmp-${process.pid}`;
  fs.writeFileSync(tmp, content, "utf8");
  fs.renameSync(tmp, dst);
}

function nonEmptyFile(filePath) {
  try {
    return fs.statSync(filePath).size > 0;
  } catch {
    return false;
  }
}

function main() {
  const args = parseArgs(process.argv.slice(2));
  const version = String(args.version || "").trim().replace(/^v/, "");
  const origin = String(args.origin || "https://jieshangai.com").trim().replace(/\/+$/, "");
  const inDir = path.resolve(String(args.in || "./artifacts"));
  const outDir = path.resolve(String(args.out || "./deploy/client-dist"));
  const notes = String(args.notes || `接上AI v${version} 稳定性与体验优化`).trim();
  const allowPartial = Boolean(args["allow-partial"]);

  if (!version) {
    console.error("Error: --version is required (e.g. --version 0.2.0-beta.11)");
    process.exit(1);
  }
  if (!fs.existsSync(inDir)) {
    console.error(`Error: input directory does not exist: ${inDir}`);
    process.exit(1);
  }

  const files = walkFiles(inDir).map(src => ({
    src,
    arch: artifactArch(src, inDir),
    name: path.basename(src).toLowerCase(),
  }));
  const downloadsDir = path.join(outDir, "downloads");
  const updatesDir = path.join(outDir, "updates", "stable");
  const specs = [
    { os: "mac", arch: "arm64", key: "darwin-aarch64", env: "CLIENT_DL_MAC" },
    { os: "mac", arch: "x64", key: "darwin-x86_64", env: "CLIENT_DL_MAC_OTHER" },
    { os: "win", arch: "x64", key: "windows-x86_64", env: "CLIENT_DL_WIN" },
    { os: "win", arch: "arm64", key: "windows-aarch64", env: "CLIENT_DL_WIN_OTHER" },
  ];

  // Validate every required input before touching the published release.
  const stagedInstallers = [];
  const stagedPlatforms = [];
  const missingInstallers = [];
  const missingPlatforms = [];
  for (const spec of specs) {
    const candidates = files.filter(file => file.arch === spec.arch);
    const installerExt = spec.os === "mac" ? ".dmg" : ".exe";
    const installer = candidates.find(file => file.name.endsWith(installerExt) && nonEmptyFile(file.src));
    if (installer) {
      const destName = `jieshangai-${version}-${spec.os}-${spec.arch}${installerExt}`;
      stagedInstallers.push({ spec, src: installer.src, destName });
    } else {
      missingInstallers.push(spec.env);
    }

    const extensions = spec.os === "mac" ? [".app.tar.gz"] : [".exe", ".nsis.zip", ".msi.zip"];
    const bundles = candidates.filter(file => extensions.some(ext => file.name.endsWith(ext)) && nonEmptyFile(file.src));
    const signed = bundles.find(file => nonEmptyFile(`${file.src}.sig`));
    if (!signed) {
      missingPlatforms.push(`${spec.key} (${bundles.length ? "missing signature" : "missing payload"})`);
      continue;
    }
    const sigSrc = `${signed.src}.sig`;
    const signature = fs.readFileSync(sigSrc, "utf8").trim();
    if (!signature) {
      missingPlatforms.push(`${spec.key} (empty .sig)`);
      continue;
    }
    const extension = extensions.find(ext => signed.name.endsWith(ext));
    const fileName = `jieshangai-${version}-${spec.key}${extension}`;
    stagedPlatforms.push({ spec, src: signed.src, sigSrc, signature, fileName });
  }

  if (stagedPlatforms.length === 0) {
    console.error("Error: no signed updater bundles found; refusing to overwrite latest.json.");
    for (const m of missingPlatforms) console.error(`  - ${m}`);
    process.exit(1);
  }
  if (!allowPartial && (missingPlatforms.length > 0 || missingInstallers.length > 0)) {
    console.error("Error: incomplete release artifacts (pass --allow-partial only for dev tests):");
    for (const m of missingInstallers) console.error(`  - missing installer: ${m}`);
    for (const m of missingPlatforms) console.error(`  - missing updater:   ${m}`);
    console.error("Existing latest.json (if any) was NOT modified.");
    process.exit(1);
  }

  fs.mkdirSync(downloadsDir, { recursive: true });
  fs.mkdirSync(updatesDir, { recursive: true });

  const envLines = [];
  for (const { spec, src, destName } of stagedInstallers) {
    copyFile(src, path.join(downloadsDir, destName));
    const url = `${origin}/client-dist/downloads/${destName}`;
    envLines.push(`${spec.env}=${url}`);
    console.log(`[ok] installer ${spec.env} -> ${destName}`);
  }

  const platforms = {};
  for (const { spec, src, sigSrc, signature, fileName } of stagedPlatforms) {
    copyFile(src, path.join(updatesDir, fileName));
    copyFile(sigSrc, path.join(updatesDir, `${fileName}.sig`));
    platforms[spec.key] = {
      signature,
      url: `${origin}/client-dist/updates/stable/${fileName}`,
    };
    console.log(`[ok] updater ${spec.key} -> ${fileName}`);
  }

  const manifest = {
    version,
    notes,
    pub_date: new Date().toISOString(),
    platforms,
  };
  const manifestPath = path.join(updatesDir, "latest.json");
  atomicWriteUTF8(manifestPath, `${JSON.stringify(manifest, null, 2)}\n`);
  console.log(`\n[done] Atomically wrote ${manifestPath} (${Object.keys(platforms).length} platform(s))`);

  if (envLines.length > 0) {
    console.log("\nPaste into deploy/.env (then run `docker compose up -d web`):");
    for (const line of envLines) console.log(line);
  }
}

main();
