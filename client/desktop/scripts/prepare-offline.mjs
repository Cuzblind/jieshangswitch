import { execFileSync } from "node:child_process";
import { readFileSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";

const metadata = JSON.parse(
  readFileSync("src-tauri/binaries/build.json", "utf8"),
);
const windows = metadata.target.includes("windows");
const native = windows
  ? process.platform === "win32"
  : process.platform === "darwin";
if (
  !native ||
  (metadata.target.startsWith("aarch64") ? "arm64" : "x64") !== process.arch
)
  throw new Error(
    "Offline resources must be prepared on the matching native runner",
  );
const helper = resolve(
  `src-tauri/binaries/jieshangai-engine-${metadata.target}${windows ? ".exe" : ""}`,
);
execFileSync(helper, ["--prepare-offline", resolve("offline")], {
  stdio: "inherit",
  timeout: 7200000,
});
writeFileSync(
  "tauri.offline.conf.json",
  JSON.stringify(
    { bundle: { resources: { "../offline/": "jieshangai-offline/" } } },
    null,
    2,
  ),
);
console.log(
  "Offline resources ready; build with --config tauri.offline.conf.json",
);
