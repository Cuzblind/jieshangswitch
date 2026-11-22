// Narrowed from CC Switch v4.0.20's Tauri bootstrap. All provider, proxy,
// import and arbitrary filesystem/process handlers are removed. Updates use
// only this project's signed releases through the small owned update module.
mod engine;
mod policy;
mod update;

use engine::Engine;
use policy::{Action, DeepLink};
use serde_json::{json, Value};
use std::sync::{Arc, Mutex};

#[derive(Default)]
struct PendingLinks(Mutex<Vec<Value>>);
use tauri::{Manager, State};
use tauri_plugin_clipboard_manager::ClipboardExt;
use tauri_plugin_deep_link::DeepLinkExt;
use tauri_plugin_dialog::DialogExt;

#[tauri::command]
fn desktop_state(engine: State<'_, Arc<Engine>>) -> Value {
    engine.state()
}

/// Opens the system picker for a skill zip. None when the user cancels.
async fn pick_skill_zip(app: &tauri::AppHandle) -> Option<String> {
    let (tx, rx) = tokio::sync::oneshot::channel();
    app.dialog()
        .file()
        .add_filter("Skill 压缩包", &["zip"])
        .pick_file(move |path| {
            let _ = tx.send(path);
        });
    let path = rx.await.ok()??.into_path().ok()?;
    path.to_str().map(str::to_owned)
}

#[tauri::command]
async fn desktop_action(
    app: tauri::AppHandle,
    engine: State<'_, Arc<Engine>>,
    action: Action,
    agent: Option<String>,
    confirm: bool,
    prompt_id: Option<String>,
    name: Option<String>,
    content: Option<String>,
    mcp: Option<policy::McpServer>,
    skill_dir: Option<String>,
    tier: Option<String>,
) -> Result<Value, String> {
    if agent
        .as_deref()
        .is_some_and(|value| !policy::valid_agent(value))
        || (action.requires_agent() && agent.is_none())
    {
        return Err("请选择 Claude Code 或 Codex".into());
    }
    let field = match action {
        Action::CopyQq => Some("qq"),
        Action::CopyEmail => Some("email"),
        Action::CopyReport => Some("report"),
        _ => None,
    };
    if let Some(field) = field {
        let state = engine.state();
        let text = state
            .get(field)
            .and_then(Value::as_str)
            .filter(|value| !value.is_empty())
            .ok_or("暂时没有可复制的内容")?;
        app.clipboard()
            .write_text(text.to_string())
            .map_err(|_| "没能复制，请手动选中并复制。")?;
        return Ok(state);
    }
    // The engine reads one JSON line of at most 64 KB, so keep prompt text well below it.
    let (prompt_id, name, content) = (
        prompt_id.unwrap_or_default(),
        name.unwrap_or_default(),
        content.unwrap_or_default(),
    );
    if prompt_id.len() > 64 || name.chars().count() > 80 || content.len() > 32 * 1024 {
        return Err("提示词名称或内容过长".into());
    }
    let skill_dir = skill_dir.unwrap_or_default();
    if !policy::skill_dir_fits(&skill_dir) {
        return Err("Skill 文件夹名太长".into());
    }
    let skill_zip = if action.picks_skill_zip() {
        match pick_skill_zip(&app).await {
            Some(path) => path,
            None => return Ok(engine.state()),
        }
    } else {
        String::new()
    };
    if mcp.as_ref().is_some_and(policy::McpServer::too_large) {
        return Err("MCP 服务的内容过长".into());
    }
    let tier = tier.unwrap_or_default();
    if tier.len() > 32 {
        return Err("线路名称无效".into());
    }
    engine
        .request(json!({
            "action": action.name(),
            "agent": agent.unwrap_or_default(),
            "confirm": confirm,
            "promptId": prompt_id,
            "name": name,
            "content": content,
            "mcp": mcp,
            "skillDir": skill_dir,
            "skillZip": skill_zip,
            "tier": tier,
        }))
        .await
}

fn focus(app: &tauri::AppHandle) {
    if let Some(window) = app.get_webview_window("main") {
        let _ = window.show();
        let _ = window.unminimize();
        let _ = window.set_focus();
    }
}

fn handle_link(app: &tauri::AppHandle, raw: &str) {
    focus(app);
    let request = match policy::parse_deep_link(raw) {
        Ok(DeepLink::Open) => json!({"action":"home"}),
        Ok(DeepLink::Install { ticket, agent }) => {
            json!({"action":"connect-ticket", "ticket":ticket, "agent":agent})
        }
        Err(_) => return, // Never log a URL containing a ticket.
    };
    if let Some(engine) = app.try_state::<Arc<Engine>>() {
        let engine = engine.inner().clone();
        tauri::async_runtime::spawn(async move {
            let _ = engine.request(request).await;
        });
    } else {
        let queue = app.state::<PendingLinks>();
        let mut queue = queue.inner().0.lock().unwrap();
        if queue.len() < 8 {
            queue.push(request);
        }
    }
}

pub fn run() {
    let app = tauri::Builder::default()
        .manage(PendingLinks::default())
        .manage(Arc::new(update::UpdateControl::default()))
        .plugin(tauri_plugin_single_instance::init(|app, args, _| {
            focus(app);
            for arg in args {
                if arg.starts_with("jieshangai:") {
                    handle_link(app, &arg);
                }
            }
        }))
        .plugin(tauri_plugin_deep_link::init())
        .plugin(tauri_plugin_clipboard_manager::init())
        .plugin(tauri_plugin_dialog::init())
        .plugin(tauri_plugin_updater::Builder::new().build())
        .plugin(tauri_plugin_window_state::Builder::default().build())
        .invoke_handler(tauri::generate_handler![
            desktop_state,
            desktop_action,
            update::check_app_update,
            update::apply_app_update,
            update::cancel_app_update
        ])
        .setup(|app| {
            let engine = Engine::new(app.handle().clone());
            engine.start();
            app.manage(engine.clone());
            let queued =
                std::mem::take(&mut *app.state::<PendingLinks>().inner().0.lock().unwrap());
            tauri::async_runtime::spawn(async move {
                for request in queued {
                    let _ = engine.request(request).await;
                }
            });
            #[cfg(any(target_os = "windows", target_os = "linux"))]
            {
                if cfg!(debug_assertions) {
                    let _ = app.deep_link().register_all();
                }
            }
            let handle = app.handle().clone();
            app.deep_link().on_open_url(move |event| {
                for url in event.urls() {
                    handle_link(&handle, url.as_str());
                }
            });
            if let Ok(Some(urls)) = app.deep_link().get_current() {
                for url in urls {
                    handle_link(app.handle(), url.as_str());
                }
            }
            Ok(())
        })
        .on_page_load(|webview, _| {
            let _ = webview.window().show();
        })
        .build(tauri::generate_context!())
        .expect("接上AI启动失败");
    app.run(|app, event| {
        if matches!(event, tauri::RunEvent::Exit) {
            app.state::<Arc<Engine>>().stop();
        }
    });
}
