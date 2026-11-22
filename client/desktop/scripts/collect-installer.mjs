import {
  readdirSync,
  copyFileSync,
  mkdirSync,
  readFileSync,
  writeFileSync,
} from "node:fs";
import { createHash } from "node:crypto";
import { resolve } from "node:path";
const target = process.env.CARGO_BUILD_TARGET;
const name = process.env.DESKTOP_ARTIFACT_NAME;
if (!target || !/^jieshangai-(win|mac)-(x64|arm64)$/.test(name || ""))
  throw new Error("Missing installer target or artifact name");
const extension = target.includes("windows") ? ".exe" : ".dmg";
const directory = resolve(
  "src-tauri/target",
  target,
  "release/bundle",
  extension === ".exe" ? "nsis" : "dmg",
);
const files = readdirSync(directory).filter((file) => file.endsWith(extension));
if (files.length !== 1) throw new Error("Expected exactly one installer");
mkdirSync("artifacts", { recursive: true });
const output = `${name}${extension}`;
copyFileSync(resolve(directory, files[0]), resolve("artifacts", output));
const hash = createHash("sha256")
  .update(readFileSync(resolve("artifacts", output)))
  .digest("hex");
writeFileSync(resolve("artifacts", "SHA256SUMS.txt"), `${hash}  ${output}\n`);

// A signed update payload is separate from the interactive DMG installer.
// Keep one manifest fragment per architecture; publishing merges these fragments.
const signedDirectory =
  extension === ".exe"
    ? directory
    : resolve("src-tauri/target", target, "release/bundle/macos");
const payloads = readdirSync(signedDirectory).filter((file) =>
  extension === ".exe"
    ? file.endsWith(".exe.sig")
    : file.endsWith(".app.tar.gz.sig"),
);
if (payloads.length === 1) {
  const version = JSON.parse(readFileSync("package.json", "utf8")).version;
  const payloadName = extension === ".exe" ? output : `${name}.app.tar.gz`;
  const source = resolve(signedDirectory, payloads[0].slice(0, -4));
  if (extension !== ".exe")
    copyFileSync(source, resolve("artifacts", payloadName));
  copyFileSync(
    resolve(signedDirectory, payloads[0]),
    resolve("artifacts", `${payloadName}.sig`),
  );
  const arch = target.startsWith("aarch64") ? "aarch64" : "x86_64";
  const platform = `${extension === ".exe" ? "windows" : "darwin"}-${arch}`;
  writeFileSync(
    resolve("artifacts", "latest.fragment.json"),
    JSON.stringify(
      {
        version,
        notes: process.env.RELEASE_NOTES || "安装与连接体验更新",
        pub_date: new Date().toISOString(),
        platforms: {
          [platform]: {
            signature: readFileSync(
              resolve(signedDirectory, payloads[0]),
              "utf8",
            ).trim(),
            url: `https://jieshangai.com/client-dist/updates/stable/${version}/${payloadName}`,
          },
        },
      },
      null,
      2,
    ),
  );
}
