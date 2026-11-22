import { invoke } from "@tauri-apps/api/core";
import { listen } from "@tauri-apps/api/event";

export type Agent = "claude" | "codex";
export type Action =
  | "copy-qq"
  | "copy-email"
  | "copy-report"
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
  | "repair"
  | "restore"
  | "restore-confirm"
  | "logout-confirm"
  | "support"
  | "recharge"
  | "open-web"
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
  | "skill-import"
  | "skill-import-zip"
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

export const desktopSwitchTier = (agent: Agent, tier: string) =>
  invoke<DesktopState>("desktop_action", { action: "switch-tier", agent, confirm: false, tier });

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
