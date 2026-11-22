use serde_json::{json, Value};
use std::{
    collections::HashMap,
    io::{BufRead, BufReader, Read, Write},
    net::TcpListener,
    process::{Child, ChildStdin, Command, Stdio},
    sync::{
        atomic::{AtomicBool, AtomicU64, Ordering},
        Arc, Mutex,
    },
    time::Duration,
};
use tauri::{AppHandle, Emitter};
use tokio::sync::oneshot;

type Reply = oneshot::Sender<Result<Value, String>>;

pub struct Engine {
    app: AppHandle,
    cache: Mutex<Value>,
    input: Mutex<Option<ChildStdin>>,
    child: Mutex<Option<Child>>,
    pending: Mutex<HashMap<String, Reply>>,
    sequence: AtomicU64,
    revision: AtomicU64,
    alive: AtomicBool,
    ready: AtomicBool,
}

impl Engine {
    pub fn new(app: AppHandle) -> Arc<Self> {
        Arc::new(Self {
            app,
            cache: Mutex::new(
                json!({"os":std::env::consts::OS,"view":{"screen":"home"}, "statuses":[], "version":env!("JIESHANGAI_COMPILED_VERSION"), "authorized":false, "support":false, "engineReady":false}),
            ),
            input: Mutex::new(None),
            child: Mutex::new(None),
            pending: Mutex::new(HashMap::new()),
            sequence: AtomicU64::new(1),
            revision: AtomicU64::new(1),
            alive: AtomicBool::new(false),
            ready: AtomicBool::new(false),
        })
    }

    pub fn state(&self) -> Value {
        self.cache.lock().unwrap().clone()
    }

    fn fail(&self, message: &str) {
        self.ready.store(false, Ordering::SeqCst);
        let mut cache = self.cache.lock().unwrap();
        cache["revision"] = json!(self.revision.fetch_add(1, Ordering::SeqCst));
        cache["engineReady"] = json!(false);
        cache["engineError"] = json!(message);
        let _ = self.app.emit("desktop_progress", cache.clone());
        drop(cache);
        for (_, reply) in self.pending.lock().unwrap().drain() {
            let _ = reply.send(Err(message.to_string()));
        }
    }

    pub fn start(self: &Arc<Self>) {
        // The web page's presence hint only. Without the port (an old client or any
        // other program) deep links and every local feature still work.
        let listener = bind_probe("127.0.0.1:47391");
        let name = if cfg!(windows) {
            "jieshangai-engine.exe"
        } else {
            "jieshangai-engine"
        };
        let packaged = match std::env::current_exe()
            .ok()
            .and_then(|p| p.parent().map(|dir| dir.join(name)))
        {
            Some(path) => path,
            None => {
                self.fail("找不到客户端服务，请重新安装接上AI。");
                return;
            }
        };
        let binary = if packaged.is_file() {
            packaged
        } else if cfg!(debug_assertions) {
            let suffix = if cfg!(windows) { ".exe" } else { "" };
            std::path::Path::new(env!("CARGO_MANIFEST_DIR"))
                .join("binaries")
                .join(format!(
                    "jieshangai-engine-{}{}",
                    env!("JIESHANGAI_TARGET"),
                    suffix
                ))
        } else {
            packaged
        };
        let mut command = Command::new(binary);
        command
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .stderr(Stdio::null());
        #[cfg(windows)]
        {
            use std::os::windows::process::CommandExt;
            command.creation_flags(0x08000000);
        }
        let mut child = match command.spawn() {
            Ok(child) => child,
            Err(_) => {
                self.fail("客户端服务没能启动，请重新安装或联系客服。");
                return;
            }
        };
        let stdout = child.stdout.take().unwrap();
        *self.input.lock().unwrap() = child.stdin.take();
        *self.child.lock().unwrap() = Some(child);
        self.alive.store(true, Ordering::SeqCst);
        if let Some(listener) = listener {
            self.start_probe(listener);
        }
        let engine = Arc::clone(self);
        std::thread::spawn(move || {
            let mut reader = BufReader::new(stdout);
            loop {
                // Bound a protocol frame before allocating; never forward raw output.
                let mut bytes = Vec::new();
                let read = reader
                    .by_ref()
                    .take(512 * 1024)
                    .read_until(b'\n', &mut bytes);
                if !matches!(read, Ok(n) if n > 0) || bytes.last() != Some(&b'\n') {
                    break;
                }
                let reply: Value = match serde_json::from_slice(&bytes) {
                    Ok(value) => value,
                    Err(_) => break,
                };
                if let Some(state) = reply.get("state").filter(|s| s.is_object()) {
                    let mut state = state.clone();
                    state["revision"] = json!(engine.revision.fetch_add(1, Ordering::SeqCst));
                    state["engineReady"] = json!(true);
                    *engine.cache.lock().unwrap() = state.clone();
                    engine.ready.store(true, Ordering::SeqCst);
                    let _ = engine.app.emit("desktop_progress", state);
                }
                if let Some(id) = reply.get("id").and_then(Value::as_str) {
                    if let Some(sender) = engine.pending.lock().unwrap().remove(id) {
                        let error = reply.get("error").and_then(Value::as_str).unwrap_or("");
                        let result = if error.is_empty() {
                            Ok(engine.state())
                        } else {
                            Err(error.to_string())
                        };
                        let _ = sender.send(result);
                    }
                }
            }
            engine.alive.store(false, Ordering::SeqCst);
            engine.fail("客户端服务已停止。请关闭接上AI后重新打开；未完成的操作不会自动重试。");
        });
    }

    fn start_probe(self: &Arc<Self>, listener: TcpListener) {
        let engine = Arc::clone(self);
        let _ = listener.set_nonblocking(true);
        std::thread::spawn(move || {
            while engine.alive.load(Ordering::SeqCst) {
                if let Ok((mut stream, _)) = listener.accept() {
                    let _ = stream.set_read_timeout(Some(Duration::from_secs(1)));
                    let _ = stream.set_write_timeout(Some(Duration::from_secs(1)));
                    let mut buf = [0; 4096];
                    if let Ok(n) = stream.read(&mut buf) {
                        let request = String::from_utf8_lossy(&buf[..n]);
                        let (status, cors) = probe_response(
                            &request,
                            env!("JIESHANGAI_COMPILED_ORIGIN"),
                            engine.ready.load(Ordering::SeqCst),
                        );
                        let _ = write!(stream,"HTTP/1.1 {status}\r\n{cors}Cache-Control: no-store\r\nContent-Length: 0\r\nConnection: close\r\n\r\n");
                    }
                } else {
                    std::thread::sleep(Duration::from_millis(40));
                }
            }
        });
    }

    pub async fn request(&self, mut request: Value) -> Result<Value, String> {
        if !self.alive.load(Ordering::SeqCst) {
            return Err("客户端服务不可用，请关闭后重新打开。".into());
        }
        let id = self.sequence.fetch_add(1, Ordering::Relaxed).to_string();
        request["id"] = json!(id);
        let (sender, receiver) = oneshot::channel();
        self.pending.lock().unwrap().insert(id.clone(), sender);
        let mut frame = serde_json::to_vec(&request).map_err(|_| "请求无效")?;
        frame.push(b'\n');
        let written = self
            .input
            .lock()
            .unwrap()
            .as_mut()
            .map(|input| input.write_all(&frame));
        if !matches!(written, Some(Ok(()))) {
            self.pending.lock().unwrap().remove(&id);
            return Err("客户端服务不可用，请关闭后重新打开。".into());
        }
        match tokio::time::timeout(Duration::from_secs(12 * 60), receiver).await {
            Ok(Ok(result)) => result,
            _ => {
                self.pending.lock().unwrap().remove(&id);
                Err("操作没有完成，请检查助手状态后再试。".into())
            }
        }
    }

    pub fn stop(&self) {
        self.alive.store(false, Ordering::SeqCst);
        self.input.lock().unwrap().take();
        if let Some(mut child) = self.child.lock().unwrap().take() {
            // Let the helper cancel and reap its own installer process groups.
            for _ in 0..40 {
                if matches!(child.try_wait(), Ok(Some(_))) {
                    return;
                }
                std::thread::sleep(Duration::from_millis(50));
            }
            let _ = child.kill();
            let _ = child.wait();
        }
    }
}

fn bind_probe(address: &str) -> Option<TcpListener> {
    TcpListener::bind(address).ok()
}

fn probe_response(request: &str, origin: &str, ready: bool) -> (&'static str, String) {
    let first = request.lines().next().unwrap_or("");
    let headers: Vec<_> = request
        .lines()
        .skip(1)
        .filter_map(|line| line.split_once(':'))
        .collect();
    let host = headers
        .iter()
        .find(|(key, _)| key.eq_ignore_ascii_case("host"))
        .map(|(_, value)| value.trim());
    if !matches!(host, Some("127.0.0.1:47391" | "localhost:47391")) {
        return ("403 Forbidden", String::new());
    }
    if !matches!(first, "GET /hello HTTP/1.1" | "OPTIONS /hello HTTP/1.1") {
        return ("404 Not Found", String::new());
    }
    let supplied = headers
        .iter()
        .find(|(key, _)| key.eq_ignore_ascii_case("origin"))
        .map(|(_, value)| value.trim());
    let allowed = supplied.filter(|value| {
        *value == origin
            || (origin == "https://jieshangai.com" && *value == "https://www.jieshangai.com")
    });
    if supplied.is_some() && allowed.is_none() {
        return ("403 Forbidden", String::new());
    }
    let cors = allowed.map(|value| format!("Access-Control-Allow-Origin: {value}\r\nAccess-Control-Allow-Methods: GET, OPTIONS\r\nAccess-Control-Allow-Private-Network: true\r\nVary: Origin\r\n")).unwrap_or_default();
    (
        if ready {
            "204 No Content"
        } else {
            "503 Service Unavailable"
        },
        cors,
    )
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn occupied_probe_port_is_not_fatal() {
        let held = TcpListener::bind("127.0.0.1:0").unwrap();
        let address = held.local_addr().unwrap().to_string();
        assert!(bind_probe(&address).is_none());
    }

    #[test]
    fn probe_has_no_actions_or_foreign_origins() {
        assert_eq!(
            probe_response(
                "GET /hello HTTP/1.1\r\nHost: 127.0.0.1:47391\r\nOrigin: https://evil.test\r\n",
                "https://jieshangai.com",
                true
            )
            .0,
            "403 Forbidden"
        );
        assert_eq!(
            probe_response(
                "POST /api/act HTTP/1.1\r\nHost: 127.0.0.1:47391\r\n",
                "https://jieshangai.com",
                true
            )
            .0,
            "404 Not Found"
        );
        let good =
            "GET /hello HTTP/1.1\r\nHost: 127.0.0.1:47391\r\nOrigin: https://jieshangai.com\r\n";
        assert_eq!(
            probe_response(good, "https://jieshangai.com", true).0,
            "204 No Content"
        );
        assert_eq!(
            probe_response(good, "https://jieshangai.com", false).0,
            "503 Service Unavailable"
        );
    }
}
