import { invoke } from "@tauri-apps/api/core";
import { listen } from "@tauri-apps/api/event";

export type Agent = "claude" | "codex";
export type Action =
  | "copy-qq"
  | "copy-email"
  | "copy-report"
  | "copy-first-prompt"
  | "refresh"
  | "home"
  | "connect"
  | "install"
  | "install-suite"
  | "update-tool"
  | "check-updates"
  | "launch-gui"
  | "cancel-install"
  | "launch"
  | "check"
  | "test-chat"
  | "switch-tier"
  | "select-model"
  | "repair"
  | "restore"
  | "restore-confirm"
  | "logout-confirm"
  | "support"
  | "recharge"
  | "open-pricing"
  | "open-web"
  | "switch-account"
  | "pick-work-dir"
  | "set-work-dir"
  | "usage-load"
  | "catalog-load"
  | "prompts-list"
  | "prompt-save"
  | "prompt-enable"
  | "prompt-delete"
  | "mcp-list"
  | "mcp-save"
  | "mcp-enable"
  | "mcp-disable"
  | "mcp-delete"
  | "skills-list"
  | "skills-catalog-load"
  | "skill-import"
  | "skill-import-zip"
  | "skill-install-remote"
  | "skill-enable"
  | "skill-disable"
  | "skill-delete";
export interface Prompt {
  id: string;
  name: string;
  content: string;
  enabled: boolean;
  updatedAt: string;
}
export interface McpServer {
  id: string;
  name: string;
  type: "stdio" | "http" | "sse";
  command?: string;
  args?: string[];
  // Values are always blank in state; the engine keeps the stored secret.
  env?: Record<string, string>;
  cwd?: string;
  url?: string;
  // Header values are blank in state too, like env values.
  headers?: Record<string, string>;
  enabled: boolean;
  conflict?: boolean;
  external?: boolean;
}
export interface Skill {
  dir: string;
  name: string;
  description?: string;
  origin?: Agent;
  enabled: boolean;
  managed: boolean;
  conflict?: boolean;
  // Why an unmanaged folder cannot be taken under management.
  unsupported?: string;
}
export interface RemoteSkillCategory {
  id: string;
  label: string;
}
export interface RemoteSkillEntitlement {
  authenticated: boolean;
  paid_fen: number;
  threshold_fen: number;
  is_team_member: boolean;
  premium_unlocked: boolean;
}
export interface RemoteSkillItem {
  id: number;
  slug: string;
  title: string;
  category: string;
  tier: "free" | "premium" | string;
  summary: string;
  supported_agents: string[];
  install_command?: string;
  has_install_command?: boolean;
  has_zip: boolean;
  zip_size_bytes: number;
  zip_sha256: string;
  has_preset: boolean;
  unlocked: boolean;
  install_count?: number;
  sort_order: number;
  created_at?: number;
  updated_at: number;
}
export interface RemoteSkillsCatalog {
  categories: RemoteSkillCategory[];
  items: RemoteSkillItem[];
  entitlement: RemoteSkillEntitlement;
}
export type McpInput = Pick<McpServer, "id" | "name" | "type" | "command" | "args" | "env" | "cwd" | "url" | "headers">;
export interface View {
  screen: string;
  agent?: Agent;
  line?: string;
  extra?: string;
  note?: string;
  button?: string;
  action?: string;
  second?: string;
  secondAction?: string;
  busy?: boolean;
  cancellable?: boolean;
  // One-line success message shown as a self-dismissing toast, never a dialog.
  notice?: string;
}
export interface TierOption {
  name: string;
  label: string;
  recommended?: boolean;
}
export interface Assistant {
  agent: Agent;
  name: string;
  state: string;
  badge: string;
  note?: string;
  installed?: boolean;
  configured?: boolean;
  running?: boolean;
  tier?: string;
  tiers?: TierOption[];
}
export type UsageRange = "24h" | "7d" | "30d" | "90d";
export interface WorkspaceInfo {
  kind: "personal" | "team" | string;
  label: string;
  teamId?: number;
}
export interface TodayUsageSummary {
  costCny: string;
  requests: number;
  tokens: number;
}
export interface UsageHeatmapDay {
  date: string;
  requests: number;
  tokens: number;
  costCny: string;
}
export interface UsageLogRow {
  id: number;
  createdAt: number;
  model: string;
  promptTokens: number;
  completionTokens: number;
  costCny: string;
  useTimeMs: number;
}
export interface UsageSnapshot {
  range: UsageRange;
  costCny: string;
  requests: number;
  tokens: number;
  promptTokens: number;
  completionTokens: number;
  heatmap: UsageHeatmapDay[];
  logs: UsageLogRow[];
}
export interface CatalogModel {
  id: string;
  display_name?: string;
  vendor?: string;
  context_window?: number;
  visibility?: "list" | "hide" | string;
  is_latest?: boolean;
  is_recommended?: boolean;
  is_cn_alternative?: boolean;
  role_label?: string;
}
export interface ClaudeSlots {
  primary: string;
  sonnet: string;
  opus: string;
  haiku: string;
}
export interface CatalogSnapshot {
  enabled: boolean;
  source?: string;
  version: number;
  agent: string;
  tier: string;
  default_model: string;
  active_model?: string;
  claude_mode?: "native" | "cn" | string;
  claude_slots?: ClaudeSlots;
  cn_alternatives?: CatalogModel[];
  models: CatalogModel[];
}
export interface DesktopState {
  view: View;
  statuses: Assistant[];
  version: string;
  os: string;
  authorized: boolean;
  support: boolean;
  qq?: string;
  email?: string;
  report?: string;
  balance?: { text: string; low: boolean; hint?: string; red?: boolean };
  accountLabel?: string;
  workspace?: WorkspaceInfo;
  workDir?: string;
  recentDirs?: string[];
  today?: TodayUsageSummary;
  usage?: UsageSnapshot;
  walletEpoch?: number;
  catalogs?: Partial<Record<Agent, CatalogSnapshot>>;
  engineReady?: boolean;
  engineError?: string;
  revision?: number;
  tools?: Array<{
    agent: Agent;
    cliInstalled: boolean;
    guiInstalled: boolean;
    cliVersion?: string;
    guiVersion?: string;
    latestCli?: string;
    latestGui?: string;
    guiAvailable: boolean;
    updateAvailable?: boolean;
    // False until the engine finished its first probe; nothing is "missing" before that.
    inspected?: boolean;
    // False until the desktop-app check finished; the CLI may be known earlier.
    guiInspected?: boolean;
    note?: string;
    cliInstall?: {status: string; source?: string; phase?: string; message?: string; failure?: {code: string; nativeCode?: string; message: string; retryable?: boolean}};
    guiInstall?: {status: string; source?: string; phase?: string; message?: string; failure?: {code: string; nativeCode?: string; message: string; retryable?: boolean}};
  }>;
  installTasks?: Array<{
    agent: Agent;
    line: string;
    busy: boolean;
    error?: string;
    cancelled?: boolean;
  }>;
  toolUpdates?: { checking: boolean; error?: string; checkedAt?: string };
  prompts?: Partial<Record<Agent, Prompt[]>>;
  mcp?: Partial<Record<Agent, McpServer[]>>;
  skills?: Partial<Record<Agent, Skill[]>>;
  skillCatalog?: RemoteSkillsCatalog;
}

// Display labels alone are not an identity: two teams or rotated connections
// can have the same label. The engine generation contains no credential data.
export function desktopWalletIdentity(state?: DesktopState): string {
  return JSON.stringify([
    state?.walletEpoch ?? 0,
    state?.workspace?.kind ?? "",
    state?.workspace?.teamId ?? 0,
    state?.workspace?.label ?? "",
    state?.accountLabel ?? "",
  ]);
}
export const desktopState = () => invoke<DesktopState>("desktop_state");
export const desktopAction = (action: Action, agent?: Agent, confirm = false) =>
  invoke<DesktopState>("desktop_action", { action, agent, confirm });
export const onDesktopState = (callback: (state: DesktopState) => void) =>
  listen<DesktopState>("desktop_progress", ({ payload }) => callback(payload));
export interface AppUpdate {
  available: boolean;
  version?: string;
  notes?: string;
}
export const checkAppUpdate = () => invoke<AppUpdate>("check_app_update");
export const applyAppUpdate = (version: string) =>
  invoke<void>("apply_app_update", { version, confirm: true });
export const cancelAppUpdate = () => invoke<void>("cancel_app_update");
export const onAppUpdateProgress = (
  callback: (progress: { received: number; total?: number }) => void,
) =>
  listen<{ received: number; total?: number }>(
    "app-update-progress",
    ({ payload }) => callback(payload),
  );

export interface PromptArgs {
  promptId?: string;
  name?: string;
  content?: string;
}
export const desktopPromptAction = (
  action: Extract<Action, `prompt${string}`>,
  agent: Agent,
  args: PromptArgs = {},
) => invoke<DesktopState>("desktop_action", { action, agent, confirm: false, ...args });

export const desktopMcpAction = (
  action: Extract<Action, `mcp-${string}`>,
  agent: Agent,
  args: { promptId?: string; mcp?: McpInput } = {},
) => invoke<DesktopState>("desktop_action", { action, agent, confirm: false, ...args });

export const desktopSkillAction = (
  action: Extract<Action, `skill${string}`>,
  agent: Agent,
  dir = "",
) => invoke<DesktopState>("desktop_action", { action, agent, confirm: false, skillDir: dir });

export const desktopSkillsCatalog = (agent?: Agent) =>
  invoke<DesktopState>("desktop_action", { action: "skills-catalog-load", agent, confirm: false });

export const desktopSwitchTier = (agent: Agent, tier: string) =>
  invoke<DesktopState>("desktop_action", { action: "switch-tier", agent, confirm: false, tier });

export const desktopSelectModel = (
  agent: Agent,
  opts: { model?: string; mode?: "native" | "cn" },
) => invoke<DesktopState>("desktop_action", { action: "select-model", agent, confirm: false, ...opts });

export const desktopUsage = (range: UsageRange) =>
  invoke<DesktopState>("desktop_action", { action: "usage-load", confirm: false, range });

export const desktopCatalog = (agent: Agent) =>
  invoke<DesktopState>("desktop_action", { action: "catalog-load", agent, confirm: false });

export const desktopSetWorkDir = (workDir: string) =>
  invoke<DesktopState>("desktop_action", { action: "set-work-dir", confirm: false, workDir });

// The native action enum is the boundary; legacy screen names are mapped here.
export function viewAction(action?: string): Action {
  switch (action) {
    case "connect":
    case "home":
    case "open-web":
    case "restore-confirm":
    case "logout-confirm":
    case "launch":
    case "launch-gui":
    case "install-suite":
      return action;
    case "launch-try":
      return "launch";
    case "fix-login":
    case "fix":
    case "retry":
    case "fix-stale":
      return "repair";
    case "install-cli":
    case "install-cli-run":
      return "install-suite";
    default:
      return "home";
  }
}
