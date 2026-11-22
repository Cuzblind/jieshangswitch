use serde::{Deserialize, Serialize};
use std::collections::BTreeMap;
use url::Url;

#[derive(Clone, Copy, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum Action {
    CopyQq,
    CopyEmail,
    CopyReport,
    Refresh,
    Home,
    Connect,
    Install,
    InstallSuite,
    UpdateTool,
    CheckUpdates,
    LaunchGui,
    CancelInstall,
    Launch,
    Check,
    SwitchTier,
    Repair,
    Restore,
    RestoreConfirm,
    LogoutConfirm,
    Support,
    Recharge,
    OpenWeb,
    PromptsList,
    PromptSave,
    PromptEnable,
    PromptDelete,
    McpList,
    McpSave,
    McpEnable,
    McpDisable,
    McpDelete,
    SkillsList,
    SkillImport,
    SkillImportZip,
    SkillEnable,
    SkillDisable,
    SkillDelete,
}

impl Action {
    pub fn name(self) -> &'static str {
        match self {
            Self::CopyQq => "copy-qq",
            Self::CopyEmail => "copy-email",
            Self::CopyReport => "copy-report",
            Self::Refresh => "refresh",
            Self::Home => "home",
            Self::Connect => "connect",
            Self::Install => "install",
            Self::InstallSuite => "install-suite",
            Self::UpdateTool => "update-tool",
            Self::CheckUpdates => "check-updates",
            Self::LaunchGui => "launch-gui",
            Self::CancelInstall => "cancel-install",
            Self::Launch => "launch",
            Self::Check => "check",
            Self::SwitchTier => "switch-tier",
            Self::Repair => "repair",
            Self::Restore => "restore",
            Self::RestoreConfirm => "restore-confirm",
            Self::LogoutConfirm => "logout-confirm",
            Self::Support => "support",
            Self::Recharge => "recharge",
            Self::OpenWeb => "open-web",
            Self::PromptsList => "prompts-list",
            Self::PromptSave => "prompt-save",
            Self::PromptEnable => "prompt-enable",
            Self::PromptDelete => "prompt-delete",
            Self::McpList => "mcp-list",
            Self::McpSave => "mcp-save",
            Self::McpEnable => "mcp-enable",
            Self::McpDisable => "mcp-disable",
            Self::McpDelete => "mcp-delete",
            Self::SkillsList => "skills-list",
            Self::SkillImport => "skill-import",
            Self::SkillImportZip => "skill-import-zip",
            Self::SkillEnable => "skill-enable",
            Self::SkillDisable => "skill-disable",
            Self::SkillDelete => "skill-delete",
        }
    }
    /// The zip path comes only from the native picker, never from the page.
    pub fn picks_skill_zip(self) -> bool {
        matches!(self, Self::SkillImportZip)
    }
    pub fn requires_agent(self) -> bool {
        matches!(
            self,
            Self::Connect
                | Self::Install
                | Self::InstallSuite
                | Self::UpdateTool
                | Self::LaunchGui
                | Self::Launch
                | Self::Check
                | Self::SwitchTier
                | Self::Repair
                | Self::LogoutConfirm
                | Self::PromptsList
                | Self::PromptSave
                | Self::PromptEnable
                | Self::PromptDelete
                | Self::McpList
                | Self::McpSave
                | Self::McpEnable
                | Self::McpDisable
                | Self::McpDelete
                | Self::SkillsList
                | Self::SkillImport
                | Self::SkillImportZip
                | Self::SkillEnable
                | Self::SkillDisable
                | Self::SkillDelete
        )
    }
}

/// One MCP server as the form sends it. Unknown fields are refused so the
/// page cannot smuggle anything else through to the engine.
#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct McpServer {
    #[serde(default)]
    pub id: String,
    pub name: String,
    #[serde(rename = "type")]
    pub kind: String,
    #[serde(default)]
    pub command: String,
    #[serde(default)]
    pub args: Vec<String>,
    #[serde(default)]
    pub env: BTreeMap<String, String>,
    #[serde(default)]
    pub url: String,
    #[serde(default)]
    pub headers: BTreeMap<String, String>,
    #[serde(default)]
    pub cwd: String,
}

impl McpServer {
    pub fn too_large(&self) -> bool {
        self.id.len() > 64
            || self.name.len() > 40
            || self.command.len() > 1024
            || self.url.len() > 2048
            || self.args.len() > 64
            || self.args.iter().any(|a| a.len() > 2048)
            || self.env.len() > 32
            || self.env.iter().any(|(k, v)| k.len() > 64 || v.len() > 4096)
            || self.cwd.len() > 1024
            || self.headers.len() > 32
            || self
                .headers
                .iter()
                .any(|(k, v)| k.len() > 64 || v.len() > 4096)
    }
}

/// Skill folders are named by people, often in Chinese: count characters, and
/// cap bytes so one JSON line stays far below the engine limit.
pub fn skill_dir_fits(dir: &str) -> bool {
    dir.chars().count() <= 64 && dir.len() <= 256
}

pub fn valid_agent(agent: &str) -> bool {
    matches!(agent, "claude" | "codex")
}

#[derive(Debug, PartialEq)]
pub enum DeepLink {
    Open,
    Install { ticket: String, agent: String },
}

pub fn parse_deep_link(raw: &str) -> Result<DeepLink, &'static str> {
    if raw.len() > 2048 {
        return Err("连接地址无效，请回网页重新接上");
    }
    let normalized = raw.replacen("jieshangai:install?", "jieshangai://install?", 1);
    let url = Url::parse(&normalized).map_err(|_| "连接地址无效，请回网页重新接上")?;
    if url.scheme() != "jieshangai"
        || !url.username().is_empty()
        || url.password().is_some()
        || url.port().is_some()
        || url.fragment().is_some()
        || !matches!(url.path(), "" | "/")
    {
        return Err("连接地址无效，请回网页重新接上");
    }
    if url.host_str() == Some("open") && url.query().is_none() {
        return Ok(DeepLink::Open);
    }
    if url.host_str() != Some("install") {
        return Err("连接地址无效，请回网页重新接上");
    }
    let mut ticket = None;
    let mut agent = None;
    for (key, value) in url.query_pairs() {
        match key.as_ref() {
            "ticket" if ticket.is_none() => ticket = Some(value.into_owned()),
            "agent" if agent.is_none() => agent = Some(value.into_owned()),
            _ => return Err("连接地址无效，请回网页重新接上"),
        }
    }
    let ticket = ticket.ok_or("缺少连接票据，请回网页重新接上")?;
    let agent = agent.ok_or("请选择 Claude Code 或 Codex")?;
    if !valid_agent(&agent)
        || !(8..=128).contains(&ticket.len())
        || !ticket
            .bytes()
            .all(|b| b.is_ascii_alphanumeric() || b == b'_' || b == b'-')
    {
        return Err("连接地址无效，请回网页重新接上");
    }
    Ok(DeepLink::Install { ticket, agent })
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn rejects_provider_imports_and_origin_injection() {
        for raw in [
            "ccswitch://v1/import?resource=provider",
            "jieshangai://install/evil?ticket=abcdefgh&agent=claude",
            "jieshangai://install?ticket=abcdefgh&agent=claude&origin=https://evil.test",
            "jieshangai://install?ticket=abcdefgh&agent=claude&agent=codex",
            "jieshangai://install?ticket=abcdefgh&agent=gemini",
            "jieshangai://user@install?ticket=abcdefgh&agent=claude",
        ] {
            assert!(parse_deep_link(raw).is_err());
        }
        assert_eq!(parse_deep_link("jieshangai://open"), Ok(DeepLink::Open));
        assert!(matches!(
            parse_deep_link("jieshangai://install?ticket=abcdefgh&agent=claude"),
            Ok(DeepLink::Install { .. })
        ));
    }
    #[test]
    fn skill_zip_import_is_an_agent_action_without_a_path() {
        let action: Action = serde_json::from_value(serde_json::json!("skill-import-zip")).unwrap();
        assert_eq!(action.name(), "skill-import-zip");
        assert!(action.requires_agent());
        assert!(action.picks_skill_zip());
        let other: Action = serde_json::from_value(serde_json::json!("skill-import")).unwrap();
        assert!(!other.picks_skill_zip());
    }

    #[test]
    fn skill_folder_names_fit_by_characters() {
        assert!(skill_dir_fits(&"长".repeat(64)));
        assert!(!skill_dir_fits(&"长".repeat(65)));
        assert!(skill_dir_fits(&"\u{10000}".repeat(64)));
        assert!(skill_dir_fits("paper"));
    }

    #[test]
    fn mcp_payload_carries_headers_and_cwd_within_limits() {
        let sse = serde_json::json!({"name":"events","type":"sse","url":"https://x/sse","headers":{"X-Api-Key":"k"}});
        let server = serde_json::from_value::<McpServer>(sse).unwrap();
        assert!(!server.too_large());
        let stdio = serde_json::json!({"name":"fs","type":"stdio","command":"npx","cwd":"C:/work"});
        assert!(serde_json::from_value::<McpServer>(stdio).is_ok());
        let mut many = serde_json::Map::new();
        for i in 0..33 {
            many.insert(format!("H{i}"), serde_json::json!("v"));
        }
        let big = serde_json::json!({"name":"api","type":"http","url":"https://x","headers":many});
        assert!(serde_json::from_value::<McpServer>(big)
            .unwrap()
            .too_large());
    }

    #[test]
    fn mcp_payload_refuses_extra_fields() {
        let ok = serde_json::json!({"name":"fs","type":"stdio","command":"npx","args":["-y"]});
        assert!(serde_json::from_value::<McpServer>(ok).is_ok());
        let extra = serde_json::json!({"name":"fs","type":"stdio","command":"npx","origin":"https://evil.test"});
        assert!(serde_json::from_value::<McpServer>(extra).is_err());
    }
    #[test]
    fn frontend_cannot_call_internal_or_generic_commands() {
        for action in [
            "connect-ticket",
            "add-provider",
            "switch-provider",
            "shell",
            "open-url",
        ] {
            assert!(serde_json::from_value::<Action>(serde_json::json!(action)).is_err());
        }
    }
}
