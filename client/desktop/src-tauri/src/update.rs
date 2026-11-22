use crate::engine::Engine;
use serde_json::{json, Value};
use std::{
    future::Future,
    sync::{
        atomic::{AtomicBool, Ordering},
        Arc,
    },
    time::Duration,
};
use tauri::{Emitter, Manager};
use tauri_plugin_updater::UpdaterExt;

#[derive(Default)]
pub struct UpdateControl {
    running: AtomicBool,
    cancelled: AtomicBool,
}
struct UpdateGuard(Arc<UpdateControl>);
impl Drop for UpdateGuard {
    fn drop(&mut self) {
        self.0.running.store(false, Ordering::SeqCst);
    }
}

async fn cancellable<F: Future>(control: &UpdateControl, future: F) -> Result<F::Output, String> {
    let mut future = Box::pin(future);
    loop {
        if control.cancelled.load(Ordering::SeqCst) {
            return Err("更新已取消，当前版本已保留".into());
        }
        if let Ok(result) = tokio::time::timeout(Duration::from_millis(100), &mut future).await {
            return Ok(result);
        }
    }
}

#[tauri::command]
pub fn cancel_app_update(app: tauri::AppHandle) {
    app.state::<Arc<UpdateControl>>()
        .cancelled
        .store(true, Ordering::SeqCst);
}

fn update_endpoint() -> Result<url::Url, String> {
    let origin = env!("JIESHANGAI_COMPILED_ORIGIN");
    let channel = if env!("JIESHANGAI_COMPILED_VERSION").contains("-test") {
        "beta"
    } else {
        "stable"
    };
    url::Url::parse(&format!(
        "{origin}/client-dist/updates/{channel}/latest.json"
    ))
    .map_err(|_| "更新地址无效".into())
}

#[tauri::command]
pub async fn check_app_update(app: tauri::AppHandle) -> Result<Value, String> {
    let updater = app
        .updater_builder()
        .endpoints(vec![update_endpoint()?])
        .map_err(|_| "更新配置无效")?
        .build()
        .map_err(|_| "更新配置无效")?;
    let update = tokio::time::timeout(Duration::from_secs(20), updater.check())
        .await
        .map_err(|_| "更新检查超时，当前版本仍可使用")?
        .map_err(|_| "更新服务暂不可用，当前版本仍可使用")?;
    Ok(match update {
        Some(update) => json!({"available":true,"version":update.version,"notes":update.body}),
        None => json!({"available":false}),
    })
}

#[tauri::command]
pub async fn apply_app_update(
    app: tauri::AppHandle,
    version: String,
    confirm: bool,
) -> Result<(), String> {
    if !confirm {
        return Err("请先确认更新接上AI".into());
    }
    let control = app.state::<Arc<UpdateControl>>().inner().clone();
    if control.running.swap(true, Ordering::SeqCst) {
        return Err("更新正在进行，请稍后再试".into());
    }
    control.cancelled.store(false, Ordering::SeqCst);
    let _guard = UpdateGuard(control.clone());
    let engine = app.state::<Arc<Engine>>();
    if engine.state()["installTasks"]
        .as_array()
        .is_some_and(|tasks| {
            tasks
                .iter()
                .any(|task| task["busy"].as_bool() == Some(true))
        })
    {
        return Err("请先完成或取消正在进行的助手安装".into());
    }
    let updater = app
        .updater_builder()
        .timeout(Duration::from_secs(600))
        .endpoints(vec![update_endpoint()?])
        .map_err(|_| "更新配置无效")?
        .build()
        .map_err(|_| "更新配置无效")?;
    let update = cancellable(
        &control,
        tokio::time::timeout(Duration::from_secs(20), updater.check()),
    )
    .await?
    .map_err(|_| "更新检查超时")?
    .map_err(|_| "更新服务暂不可用")?
    .ok_or("当前没有可安装的更新")?;
    if update.version != version {
        return Err("更新版本已变化，请重新检查".into());
    }
    let events = app.clone();
    let mut received = 0u64;
    // The updater verifies its embedded public key signature before returning.
    let download = update.download(
        move |n, total| {
            received += n as u64;
            let _ = events.emit(
                "app-update-progress",
                json!({"received":received,"total":total}),
            );
        },
        || {},
    );
    let bytes = cancellable(
        &control,
        tokio::time::timeout(Duration::from_secs(600), download),
    )
    .await?
    .map_err(|_| "更新下载超时，当前版本已保留")?
    .map_err(|_| "更新下载或签名校验失败，当前版本已保留")?;
    if control.cancelled.load(Ordering::SeqCst) {
        return Err("更新已取消，当前版本已保留".into());
    }
    #[cfg(windows)]
    engine.stop();
    update
        .install(bytes)
        .map_err(|_| "更新安装失败，请重新打开接上AI")?;
    #[cfg(not(windows))]
    {
        engine.stop();
        app.restart();
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn cancellation_drops_pending_update() {
        let control = UpdateControl::default();
        control.cancelled.store(true, Ordering::SeqCst);
        let result =
            tauri::async_runtime::block_on(cancellable(&control, std::future::pending::<()>()));
        assert!(result.unwrap_err().contains("已取消"));
    }
    #[test]
    fn guard_releases_update_slot_after_error() {
        let control = Arc::new(UpdateControl::default());
        control.running.store(true, Ordering::SeqCst);
        {
            let _guard = UpdateGuard(control.clone());
        }
        assert!(!control.running.load(Ordering::SeqCst));
    }
}
