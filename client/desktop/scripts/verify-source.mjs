import { readFileSync } from "node:fs";
import { createHash } from "node:crypto";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const metadata = JSON.parse(
  readFileSync(resolve(root, "upstream.json"), "utf8"),
);
for (const file of metadata.imported_files) {
  if (metadata.adapted_files.includes(file.path)) continue;
  const hash = createHash("sha256")
    // Hashes describe LF upstream text; a Windows checkout may hold CRLF.
    .update(readFileSync(resolve(root, file.path), "utf8").replace(/\r\n/g, "\n"))
    .digest("hex");
  if (hash !== file.sha256)
    throw new Error(
      `Upstream file changed without provenance update: ${file.path}`,
    );
}
const config = JSON.parse(
  readFileSync(resolve(root, "src-tauri/tauri.conf.json"), "utf8"),
);
if (
  config.identifier !== "com.jieshangai.client" ||
  config.bundle.createUpdaterArtifacts ||
  !config.plugins.updater?.pubkey ||
  config.plugins.updater.endpoints.some(
    (url) => !url.startsWith("https://jieshangai.com/client-dist/"),
  ) ||
  JSON.stringify(config.plugins["deep-link"]).includes("ccswitch")
)
  throw new Error("Desktop identity or update policy is invalid");
console.log(
  `Verified CC Switch ${metadata.tag} source provenance and desktop policy`,
);
