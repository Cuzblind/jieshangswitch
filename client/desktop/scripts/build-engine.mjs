import { execFileSync } from "node:child_process";
import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const arg = process.argv.indexOf("--target");
const target =
  (arg >= 0 && process.argv[arg + 1]) ||
  process.env.CARGO_BUILD_TARGET ||
  process.env.TAURI_ENV_TARGET_TRIPLE ||
  execFileSync("rustc", ["--print", "host-tuple"], { encoding: "utf8" }).trim();
const targets = {
  "x86_64-unknown-linux-gnu": ["linux", "amd64"],
  "aarch64-unknown-linux-gnu": ["linux", "arm64"],
  "x86_64-pc-windows-msvc": ["windows", "amd64"],
  "aarch64-pc-windows-msvc": ["windows", "arm64"],
  "x86_64-apple-darwin": ["darwin", "amd64"],
  "aarch64-apple-darwin": ["darwin", "arm64"],
};
if (!targets[target]) throw new Error("Unsupported desktop target");
const [GOOS, GOARCH] = targets[target];
const testOrigin = process.env.JIESHANGAI_TEST_ORIGIN || "";
if (testOrigin) {
  const u = new URL(testOrigin);
  if (
    !["http:", "https:"].includes(u.protocol) ||
    u.username ||
    u.password ||
    u.search ||
    u.hash ||
    u.pathname !== "/" ||
    /\s/.test(testOrigin)
  )
    throw new Error(
      "Test origin must be an HTTP(S) origin without credentials, query or path",
    );
}
const origin = testOrigin
  ? new URL(testOrigin).origin
  : "https://jieshangai.com";
const version =
  JSON.parse(readFileSync(resolve(root, "package.json"), "utf8")).version +
  (testOrigin ? "-test" : "");
const binary = resolve(
  root,
  "src-tauri/binaries",
  `jieshangai-engine-${target}${GOOS === "windows" ? ".exe" : ""}`,
);
mkdirSync(dirname(binary), { recursive: true });
const flags = [`-s`, `-w`, `-X=main.version=${version}`];
if (testOrigin) flags.push(`-X=jieshangai/client/runtime.TestOrigin=${origin}`);
execFileSync(
  "go",
  [
    "build",
    "-buildvcs=false",
    "-trimpath",
    "-ldflags",
    flags.join(" "),
    "-o",
    binary,
    "./cmd/jieshangai-engine",
  ],
  {
    cwd: resolve(root, "../runtime"),
    stdio: "inherit",
    env: { ...process.env, GOOS, GOARCH, CGO_ENABLED: "0" },
  },
);
writeFileSync(
  resolve(root, "src-tauri/binaries/build.json"),
  JSON.stringify({ target, origin, version }) + "\n",
);
console.log(`Built ${target} ${version}${testOrigin ? " (TEST BUILD)" : ""}`);
