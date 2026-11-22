import assert from "node:assert/strict";
import { execFileSync, spawn } from "node:child_process";
import { createServer } from "node:http";
import { chmodSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, delimiter, join, resolve } from "node:path";
import { createInterface } from "node:readline";
import { fileURLToPath } from "node:url";

const desktopRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const runtimeDir = resolve(desktopRoot, "../runtime");
const isWin = process.platform === "win32";

const TIERS = [
  { name: "economy", label: "省钱" },
  { name: "balanced", label: "均衡", recommended: true },
  { name: "stable", label: "稳定" },
];

const state = {
  tiersByKey: {
    "sk-smoke-claude": "balanced",
    "sk-smoke-codex": "balanced",
  },
  claims: 0,
  checks: 0,
  tierSwitches: 0,
};

function readBody(req) {
  return new Promise((resolveBody, reject) => {
    const chunks = [];
    req.on("data", (c) => chunks.push(c));
    req.on("end", () => {
      const raw = Buffer.concat(chunks).toString("utf8");
      try {
        resolveBody(raw ? JSON.parse(raw) : {});
      } catch {
        resolveBody({});
      }
    });
    req.on("error", reject);
  });
}

function sendJson(res, status, body) {
  res.writeHead(status, { "Content-Type": "application/json" });
  res.end(JSON.stringify(body));
}

const server = createServer(async (req, res) => {
  const url = new URL(req.url || "/", "http://127.0.0.1");
  const auth = (req.headers.authorization || "").replace(/^Bearer\s+/i, "").trim();

  if (req.method === "GET" && url.pathname === "/api/status") {
    return sendJson(res, 200, { success: true, data: {} });
  }
  if (req.method === "POST" && url.pathname === "/api/install/claim") {
    const body = await readBody(req);
    state.claims++;
    const isCodex = String(body.token || "").includes("codex");
    const secret = isCodex ? "sk-smoke-codex" : "sk-smoke-claude";
    const agent = isCodex ? "codex" : "claude";
    return sendJson(res, 200, {
      success: true,
      data: { key: secret, client_auth: "smoke-client-auth-token", agent },
    });
  }
  if (req.method === "POST" && url.pathname === "/api/install/progress") {
    return sendJson(res, 200, { success: true });
  }
  if (req.method === "GET" && url.pathname === "/v1/models") {
    if (!state.tiersByKey[auth]) {
      return sendJson(res, 401, { success: false, message: "unauthorized" });
    }
    state.checks++;
    return sendJson(res, 200, {
      object: "list",
      data: [
        { id: "claude-sonnet-4-5" },
        { id: "gpt-5" },
        { id: "deepseek-r1" },
      ],
    });
  }
  if (req.method === "GET" && url.pathname === "/api/install/balance") {
    const tier = state.tiersByKey[auth];
    if (!tier) {
      return sendJson(res, 401, { success: false, message: "unauthorized" });
    }
    return sendJson(res, 200, {
      success: true,
      data: {
        balance_cny: "35.00",
        balance_low: false,
        tier,
        tiers: TIERS,
      },
    });
  }
  if (req.method === "PUT" && url.pathname === "/api/install/tier") {
    if (!state.tiersByKey[auth]) {
      return sendJson(res, 401, { success: false, message: "unauthorized" });
    }
    const body = await readBody(req);
    const nextGroup = String(body.group || "").trim();
    if (!TIERS.some((t) => t.name === nextGroup)) {
      return sendJson(res, 400, { success: false, message: "invalid tier" });
    }
    state.tiersByKey[auth] = nextGroup;
    state.tierSwitches++;
    return sendJson(res, 200, {
      success: true,
      data: { tier: nextGroup, tiers: TIERS },
    });
  }
  if (req.method === "GET" && url.pathname === "/api/install/catalog") {
    const tier = state.tiersByKey[auth];
    if (!tier) {
      return sendJson(res, 401, { success: false, message: "unauthorized" });
    }
    return sendJson(res, 200, {
      success: true,
      data: {
        enabled: true,
        version: 1,
        agent: auth === "sk-smoke-codex" ? "codex" : "claude_code",
        tier,
        default_model: auth === "sk-smoke-codex" ? "gpt-5" : "claude-sonnet-4-5",
        models: [
          { id: "claude-sonnet-4-5", display_name: "Claude Sonnet 4.5", vendor: "Anthropic" },
          { id: "gpt-5", display_name: "GPT-5", vendor: "OpenAI" },
          { id: "deepseek-r1", display_name: "DeepSeek R1", vendor: "DeepSeek" },
        ],
      },
    });
  }
  return sendJson(res, 404, { success: false, message: `not found: ${url.pathname}` });
});

const sandbox = mkdtempSync(join(tmpdir(), "jieshangai-engine-smoke-"));

async function main() {
  await new Promise((r) => server.listen(0, "127.0.0.1", r));
  const { port } = server.address();
  const origin = `http://127.0.0.1:${port}`;
  console.log(`[smoke] mock gateway listening on ${origin}`);

  // 1) Build isolated test engine binary (never modifies src-tauri/binaries)
  const engineBin = join(sandbox, isWin ? "jieshangai-engine.exe" : "jieshangai-engine");
  const ldflags = `-X=main.version=0.2.0-smoke -X=jieshangai/client/runtime.TestOrigin=${origin}`;
  execFileSync(
    "go",
    ["build", "-buildvcs=false", "-ldflags", ldflags, "-o", engineBin, "./cmd/jieshangai-engine"],
    { cwd: runtimeDir, stdio: "inherit", env: { ...process.env, CGO_ENABLED: "0" } },
  );
  console.log(`[smoke] built isolated engine binary: ${engineBin}`);

  // 2) Prepare sandbox home + stub CLIs + pre-existing user config files
  const binDir = join(sandbox, "bin");
  mkdirSync(binDir, { recursive: true });
  for (const tool of ["claude", "codex"]) {
    if (isWin) {
      writeFileSync(join(binDir, `${tool}.cmd`), "@echo 1.0.0-smoke\r\n");
    } else {
      const p = join(binDir, tool);
      writeFileSync(p, "#!/bin/sh\necho 1.0.0-smoke\n");
      chmodSync(p, 0o755);
    }
  }

  const claudeSettingsPath = join(sandbox, ".claude", "settings.json");
  const codexConfigPath = join(sandbox, ".codex", "config.toml");
  const origClaude = '{\n  "theme": "dark",\n  "env": {\n    "USER_CUSTOM": "keep"\n  }\n}\n';
  const origCodex = '[profiles.work]\napproval_policy = "on-request"\n';
  mkdirSync(dirname(claudeSettingsPath), { recursive: true });
  mkdirSync(dirname(codexConfigPath), { recursive: true });
  writeFileSync(claudeSettingsPath, origClaude);
  writeFileSync(codexConfigPath, origCodex);

  // 3) Spawn real engine over JSON Lines stdin/stdout pipes
  const child = spawn(engineBin, [], {
    stdio: ["pipe", "pipe", "pipe"],
    env: {
      ...process.env,
      HOME: sandbox,
      RELAY_HOME: sandbox,
      USERPROFILE: sandbox,
      XDG_CONFIG_HOME: join(sandbox, ".config"),
      APPDATA: join(sandbox, "AppData", "Roaming"),
      LOCALAPPDATA: join(sandbox, "AppData", "Local"),
      PATH: `${binDir}${delimiter}${process.env.PATH || ""}`,
    },
  });

  const pending = new Map();
  let seq = 0;
  const rl = createInterface({ input: child.stdout });
  rl.on("line", (line) => {
    if (!line.trim()) return;
    let msg;
    try {
      msg = JSON.parse(line);
    } catch {
      return;
    }
    if (msg.id && pending.has(msg.id)) {
      const { resolve: resReq, reject: rejReq } = pending.get(msg.id);
      pending.delete(msg.id);
      if (msg.error) rejReq(new Error(msg.error));
      else resReq(msg.state);
    }
  });
  let stderr = "";
  child.stderr.on("data", (d) => {
    stderr += d.toString("utf8");
  });

  function request(payload) {
    const id = `req-${++seq}`;
    return new Promise((resReq, rejReq) => {
      const timer = setTimeout(() => {
        pending.delete(id);
        rejReq(new Error(`timeout waiting for ${payload.action} (stderr: ${stderr})`));
      }, 20_000);
      pending.set(id, {
        resolve: (st) => {
          clearTimeout(timer);
          resReq(st);
        },
        reject: (err) => {
          clearTimeout(timer);
          rejReq(err);
        },
      });
      child.stdin.write(JSON.stringify({ id, ...payload }) + "\n");
    });
  }

  try {
    // Connect Claude Code
    const stClaude = await request({ action: "connect-ticket", agent: "claude", ticket: "smoke_claude_ticket_001" });
    const claudeStatus = stClaude.statuses.find((s) => s.agent === "claude");
    assert.equal(claudeStatus?.state, "ok", `expected claude ok, got ${JSON.stringify(claudeStatus)}`);
    assert.equal(claudeStatus?.tier, "balanced");
    assert.equal(claudeStatus?.tiers?.length, 3);
    assert.equal(stClaude.balance?.text, "余额 ¥35.00");

    const writtenClaude = JSON.parse(readFileSync(claudeSettingsPath, "utf8"));
    assert.equal(writtenClaude.theme, "dark", "must preserve user theme");
    assert.equal(writtenClaude.env.USER_CUSTOM, "keep", "must preserve user custom env");
    assert.equal(writtenClaude.env.ANTHROPIC_BASE_URL, origin);
    assert.equal(writtenClaude.env.ANTHROPIC_AUTH_TOKEN, "sk-smoke-claude");
    console.log("[smoke] PASS 1/5: Claude Code connected, balance & 3 tiers loaded, user config preserved");

    // Connect Codex
    const stCodex = await request({ action: "connect-ticket", agent: "codex", ticket: "smoke_codex_ticket_002" });
    const codexStatus = stCodex.statuses.find((s) => s.agent === "codex");
    assert.equal(codexStatus?.state, "ok", `expected codex ok, got ${JSON.stringify(codexStatus)}`);
    assert.equal(codexStatus?.tier, "balanced");
    const writtenCodex = readFileSync(codexConfigPath, "utf8");
    assert.ok(writtenCodex.includes("[profiles.work]"), "must preserve existing Codex profiles");
    assert.ok(writtenCodex.includes(`${origin}/v1`), "must write gateway base_url");
    console.log("[smoke] PASS 2/5: Codex connected, existing [profiles.work] preserved");

    // Switch Claude tier inline -> economy; Codex must remain balanced
    const stSwitched = await request({ action: "switch-tier", agent: "claude", tier: "economy" });
    assert.equal(stSwitched.statuses.find((s) => s.agent === "claude")?.tier, "economy");
    assert.equal(stSwitched.statuses.find((s) => s.agent === "codex")?.tier, "balanced");
    assert.ok(stSwitched.view?.notice?.includes("省钱"), `expected switch notice, got ${stSwitched.view?.notice}`);
    assert.equal(state.tierSwitches, 1);
    console.log("[smoke] PASS 3/5: Inline tier switch updated Claude -> economy while Codex stayed balanced");

    // Strict /v1/models connectivity check
    const stCheck = await request({ action: "check", agent: "claude" });
    assert.equal(stCheck.view?.notice, "连接检查通过。");
    assert.ok(state.checks >= 1);
    console.log("[smoke] PASS 4/5: Free /v1/models connection check passed with strict body validation");

    // Global restore -> confirm -> verify original configs cleanly restored
    const stConfirm = await request({ action: "restore" });
    assert.equal(stConfirm.view?.screen, "confirm");
    assert.equal(stConfirm.view?.action, "restore-confirm");
    const stRestored = await request({ action: "restore-confirm", confirm: true });
    assert.ok(stRestored.view?.notice?.includes("恢复"));

    assert.equal(readFileSync(claudeSettingsPath, "utf8"), origClaude, "Claude settings must restore byte-identically");
    assert.equal(readFileSync(codexConfigPath, "utf8"), origCodex, "Codex config must restore byte-identically");
    console.log("[smoke] PASS 5/5: Full restore cleanly reverted both assistants to original user configs");
  } finally {
    child.stdin.end();
    child.kill();
  }
}

try {
  await main();
  console.log("[smoke] ALL 5 REAL-ENGINE E2E CHECKS PASSED");
} finally {
  server.close();
  rmSync(sandbox, { recursive: true, force: true });
}
