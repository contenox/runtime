#![allow(dead_code)]

use contenox_e2e::{Instance, Running, Script};
use serde_json::Value;
use std::io::{Read, Write};
use std::net::{TcpListener, TcpStream};
use std::time::{Duration, Instant};

pub const PATIENCE: Duration = Duration::from_secs(120);

pub struct Served {
    pub cx: Instance,
    pub port: u16,
    server: Option<Running>,
}

fn free_port() -> u16 {
    TcpListener::bind("127.0.0.1:0")
        .expect("bind an ephemeral port")
        .local_addr()
        .expect("read the bound address")
        .port()
}

pub fn serve(label: &str, script: &Script) -> Served {
    serve_with(label, script, |_| {})
}

pub fn serve_with(label: &str, script: &Script, prepare: impl FnOnce(&Instance)) -> Served {
    let cx = Instance::named(label).expect("scratch instance");
    cx.init().ok();
    cx.scripted(script).expect("scripted-test backend");
    prepare(&cx);
    let port = free_port();
    let server = cx
        .cmd(["serve"])
        .env("ADDR", "127.0.0.1")
        .env("PORT", port.to_string())
        .env("TOKEN", "")
        .start()
        .expect("start contenox-runtime serve");
    let served = Served {
        cx,
        port,
        server: Some(server),
    };
    let mut served = served;
    let deadline = Instant::now() + PATIENCE;
    loop {
        if let Ok((200, _)) = served.request("GET", "/health", None) {
            return served;
        }
        if Instant::now() >= deadline {
            panic!(
                "serve never answered /health on port {port}; it said:\n{}",
                served.logs()
            );
        }
        std::thread::sleep(Duration::from_millis(200));
    }
}

impl Served {
    pub fn request(
        &self,
        method: &str,
        path: &str,
        body: Option<&Value>,
    ) -> std::io::Result<(u16, String)> {
        self.request_with(method, path, body, &[])
            .map(|(status, _, body)| (status, body))
    }

    pub fn request_with(
        &self,
        method: &str,
        path: &str,
        body: Option<&Value>,
        extra: &[(&str, &str)],
    ) -> std::io::Result<(u16, String, String)> {
        let mut stream = TcpStream::connect(("127.0.0.1", self.port))?;
        stream.set_read_timeout(Some(PATIENCE))?;
        let payload = body.map(|b| b.to_string()).unwrap_or_default();
        let mut head = format!("{method} {path} HTTP/1.0\r\nHost: 127.0.0.1\r\n");
        if body.is_some() {
            head.push_str("Content-Type: application/json\r\n");
            head.push_str(&format!("Content-Length: {}\r\n", payload.len()));
        }
        for (name, value) in extra {
            head.push_str(&format!("{name}: {value}\r\n"));
        }
        head.push_str("\r\n");
        stream.write_all(head.as_bytes())?;
        stream.write_all(payload.as_bytes())?;
        let mut raw = String::new();
        stream.read_to_string(&mut raw)?;
        let (headers, body) = raw.split_once("\r\n\r\n").unwrap_or((raw.as_str(), ""));
        let status = headers
            .split_whitespace()
            .nth(1)
            .and_then(|code| code.parse().ok())
            .unwrap_or(0);
        Ok((status, headers.to_string(), body.to_string()))
    }

    pub fn json(&self, method: &str, path: &str, body: Option<&Value>, want: u16) -> Value {
        let (status, text) = self
            .request(method, path, body)
            .unwrap_or_else(|err| panic!("{method} {path}: {err}"));
        assert_eq!(status, want, "{method} {path} answered {status}: {text}");
        serde_json::from_str(&text)
            .unwrap_or_else(|err| panic!("{method} {path} is not JSON ({err}): {text}"))
    }

    pub fn events(&self) -> Vec<Value> {
        self.json("GET", "/api/workspace/events", None, 200)["events"]
            .as_array()
            .cloned()
            .expect("an events array")
    }

    pub fn logs(&mut self) -> String {
        let Some(server) = self.server.take() else {
            return String::new();
        };
        server.interrupt();
        match server.wait_timeout(Duration::from_secs(20)) {
            Ok(out) => format!("{}{}", out.stdout, out.stderr),
            Err(err) => format!("serve did not stop: {err}"),
        }
    }

    pub fn events_after(&self, query: &str, since: i64) -> Vec<Value> {
        let mut out = Vec::new();
        let mut cursor = since;
        loop {
            let sep = if query.is_empty() { "" } else { "&" };
            let page = self.json(
                "GET",
                &format!("/api/workspace/events?{query}{sep}since={cursor}"),
                None,
                200,
            );
            let events = page["events"].as_array().cloned().unwrap_or_default();
            if events.is_empty() {
                return out;
            }
            cursor = page["cursor"].as_i64().expect("a numeric cursor");
            out.extend(events);
        }
    }

    pub fn disk_events(&self, query: &str, since: i64, prefix: &str) -> Vec<Value> {
        self.events_after(query, since)
            .into_iter()
            .filter(|e| {
                e["source"] == "watcher" && e["subject"].as_str().unwrap_or("").starts_with(prefix)
            })
            .collect()
    }
}

pub fn wait_until<T>(what: &str, mut probe: impl FnMut() -> Option<T>) -> T {
    let deadline = Instant::now() + PATIENCE;
    loop {
        if let Some(found) = probe() {
            return found;
        }
        assert!(Instant::now() < deadline, "gave up waiting: {what}");
        std::thread::sleep(Duration::from_millis(100));
    }
}
