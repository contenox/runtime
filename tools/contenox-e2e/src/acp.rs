use crate::cmd::Cmd;
use anyhow::{Context, Result, bail};
use serde_json::{Value, json};
use std::io::{BufRead, BufReader, Write};
use std::path::Path;
use std::process::{Child, ChildStdin, Command, Stdio};
use std::sync::mpsc::{Receiver, RecvTimeoutError, channel};
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Verdict {
    Allow,
    Deny,
    Defer,
    Cancel,
}

#[derive(Debug, Clone)]
pub struct Reply {
    pub result: Option<Value>,
    pub error: Option<Value>,
    pub notes: Vec<Value>,
}

impl Reply {
    pub fn ok(self) -> Result<Value> {
        if let Some(error) = self.error {
            bail!("the agent answered with a JSON-RPC error: {error}");
        }
        self.result
            .context("a JSON-RPC reply with neither result nor error")
    }

    pub fn error_message(&self) -> String {
        self.error
            .as_ref()
            .and_then(|e| e.get("message"))
            .and_then(Value::as_str)
            .unwrap_or_default()
            .to_string()
    }

    pub fn error_code(&self) -> i64 {
        self.error
            .as_ref()
            .and_then(|e| e.get("code"))
            .and_then(Value::as_i64)
            .unwrap_or_default()
    }
}

#[derive(Debug, Clone, Default)]
pub struct PromptTurn {
    pub stop_reason: String,
    pub error: Option<Value>,
    pub updates: Vec<Value>,
    pub permissions: Vec<Value>,
    pub client_calls: Vec<(String, Value)>,
}

impl PromptTurn {
    pub fn kinds(&self) -> Vec<&str> {
        self.updates
            .iter()
            .filter_map(|u| u.get("sessionUpdate").and_then(Value::as_str))
            .collect()
    }

    pub fn of_kind(&self, kind: &str) -> Vec<&Value> {
        self.updates
            .iter()
            .filter(|u| u.get("sessionUpdate").and_then(Value::as_str) == Some(kind))
            .collect()
    }

    pub fn text(&self) -> String {
        self.of_kind("agent_message_chunk")
            .iter()
            .filter_map(|u| u.pointer("/content/text").and_then(Value::as_str))
            .collect()
    }

    pub fn tool_calls(&self) -> Vec<&Value> {
        self.of_kind("tool_call")
    }

    pub fn tool_call_updates(&self) -> Vec<&Value> {
        self.of_kind("tool_call_update")
    }

    pub fn tool_outputs(&self) -> String {
        let mut out = String::new();
        for update in self.updates.iter() {
            for key in ["rawOutput"] {
                if let Some(value) = update.get(key) {
                    out.push_str(&render(value));
                    out.push('\n');
                }
            }
            if let Some(error) = update.pointer("/_meta/error") {
                out.push_str(&render(error));
                out.push('\n');
            }
        }
        out
    }

    pub fn read_paths(&self) -> Vec<String> {
        self.client_calls
            .iter()
            .filter(|(method, _)| method == "fs/read_text_file")
            .filter_map(|(_, params)| params.get("path").and_then(Value::as_str))
            .map(str::to_string)
            .collect()
    }

    pub fn terminal_commands(&self) -> Vec<String> {
        self.client_calls
            .iter()
            .filter(|(method, _)| method == "terminal/create")
            .map(|(_, params)| {
                let command = params
                    .get("command")
                    .and_then(Value::as_str)
                    .unwrap_or_default();
                let args: Vec<&str> = params
                    .get("args")
                    .and_then(Value::as_array)
                    .map(|list| list.iter().filter_map(Value::as_str).collect())
                    .unwrap_or_default();
                if args.is_empty() {
                    command.to_string()
                } else {
                    format!("{command} {}", args.join(" "))
                }
            })
            .collect()
    }

    pub fn written_paths(&self) -> Vec<String> {
        self.client_calls
            .iter()
            .filter(|(method, _)| method == "fs/write_text_file")
            .filter_map(|(_, params)| params.get("path").and_then(Value::as_str))
            .map(str::to_string)
            .collect()
    }

    pub fn methods(&self) -> Vec<&str> {
        self.client_calls
            .iter()
            .map(|(method, _)| method.as_str())
            .collect()
    }

    pub fn asked_permission(&self) -> bool {
        !self.permissions.is_empty()
    }
}

fn render(value: &Value) -> String {
    match value {
        Value::String(text) => text.clone(),
        other => other.to_string(),
    }
}

pub struct Acp {
    argv: Vec<String>,
    child: Child,
    stdin: Option<ChildStdin>,
    incoming: Receiver<Value>,
    stderr: Arc<Mutex<String>>,
    next_id: i64,
    timeout: Duration,
    verdict: Verdict,
    commands: Vec<Value>,
    permissions: Vec<Value>,
    client_calls: Vec<(String, Value)>,
    performs_writes: bool,
    unreadable: Vec<String>,
    terminals: Vec<Terminal>,
}

#[derive(Debug, Clone)]
struct Terminal {
    id: String,
    output: String,
    exit_code: i32,
}

impl Acp {
    pub fn spawn(cmd: Cmd) -> Result<Acp> {
        let argv = cmd.argv();
        let mut command = Command::new(cmd.program());
        command.args(cmd.arg_list()).current_dir(cmd.dir());
        for (key, value) in cmd.envs() {
            match value {
                Some(value) => command.env(key, value),
                None => command.env_remove(key),
            };
        }
        command
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .stderr(Stdio::piped());
        let mut child = command
            .spawn()
            .with_context(|| format!("spawn {}", argv.join(" ")))?;

        let stdin = child.stdin.take().context("the child kept no stdin")?;
        let stdout = child.stdout.take().context("the child kept no stdout")?;
        let (tx, incoming) = channel();
        std::thread::spawn(move || {
            for line in BufReader::new(stdout).lines() {
                let Ok(line) = line else { break };
                if line.trim().is_empty() {
                    continue;
                }
                let parsed = serde_json::from_str::<Value>(&line)
                    .unwrap_or_else(|_| json!({ "__unparsed__": line }));
                if tx.send(parsed).is_err() {
                    break;
                }
            }
        });

        let stderr = Arc::new(Mutex::new(String::new()));
        if let Some(handle) = child.stderr.take() {
            let sink = Arc::clone(&stderr);
            std::thread::spawn(move || {
                for line in BufReader::new(handle).lines() {
                    let Ok(line) = line else { break };
                    let mut buffer = sink.lock().expect("stderr buffer");
                    buffer.push_str(&line);
                    buffer.push('\n');
                }
            });
        }

        Ok(Acp {
            argv,
            child,
            stdin: Some(stdin),
            incoming,
            stderr,
            next_id: 0,
            timeout: Duration::from_secs(120),
            verdict: Verdict::Allow,
            commands: Vec::new(),
            permissions: Vec::new(),
            client_calls: Vec::new(),
            performs_writes: true,
            unreadable: Vec::new(),
            terminals: Vec::new(),
        })
    }

    pub fn timeout(&mut self, timeout: Duration) -> &mut Self {
        self.timeout = timeout;
        self
    }

    pub fn answers(&mut self, verdict: Verdict) -> &mut Self {
        self.verdict = verdict;
        self
    }

    pub fn performs_writes(&mut self, performs: bool) -> &mut Self {
        self.performs_writes = performs;
        self
    }

    pub fn cannot_read(&mut self, path: impl Into<String>) -> &mut Self {
        self.unreadable.push(path.into());
        self
    }

    pub fn stderr(&self) -> String {
        self.stderr.lock().expect("stderr buffer").clone()
    }

    pub fn notices(&self) -> String {
        self.stderr()
            .lines()
            .filter(|line| !(line.starts_with("time=") && line.contains(" level=")))
            .collect::<Vec<_>>()
            .join("\n")
    }

    pub fn commands(&self) -> Vec<String> {
        let mut names: Vec<String> = self
            .commands
            .iter()
            .filter_map(|c| c.get("name").and_then(Value::as_str))
            .map(str::to_string)
            .collect();
        names.sort();
        names
    }

    pub fn offers(&self, command: &str) -> bool {
        self.commands().iter().any(|name| name == command)
    }

    pub fn call(&mut self, method: &str, params: Value) -> Result<Reply> {
        self.next_id += 1;
        let id = self.next_id;
        self.write(&json!({"jsonrpc": "2.0", "id": id, "method": method, "params": params}))?;
        let deadline = Instant::now() + self.timeout;
        let mut notes = Vec::new();
        loop {
            let message = self.read(deadline, method)?;
            if message.get("method").is_some() {
                if message.get("id").is_some() {
                    self.serve(&message)?;
                } else {
                    notes.push(message);
                }
                continue;
            }
            if message.get("id").and_then(Value::as_i64) == Some(id) {
                return Ok(Reply {
                    result: message.get("result").cloned(),
                    error: message.get("error").cloned(),
                    notes,
                });
            }
        }
    }

    pub fn initialize(&mut self) -> Result<Value> {
        self.initialize_with(json!({
            "fs": {"readTextFile": true, "writeTextFile": true},
            "terminal": true,
        }))
    }

    pub fn initialize_with(&mut self, client_capabilities: Value) -> Result<Value> {
        self.call(
            "initialize",
            json!({
                "protocolVersion": 1,
                "clientCapabilities": client_capabilities,
                "clientInfo": {"name": "contenox-e2e", "version": "0"},
            }),
        )?
        .ok()
    }

    pub fn new_session(&mut self, cwd: impl AsRef<Path>) -> Result<String> {
        let reply = self.call(
            "session/new",
            json!({"cwd": cwd.as_ref().display().to_string(), "mcpServers": []}),
        )?;
        let deferred = reply.notes.clone();
        let result = reply.ok()?;
        let session = result
            .get("sessionId")
            .and_then(Value::as_str)
            .context("session/new returned no sessionId")?
            .to_string();

        self.commands.clear();
        let mut seen = deferred;
        seen.extend(self.drain(1, Duration::from_secs(30))?);
        for note in seen {
            let update = note
                .pointer("/params/update")
                .cloned()
                .unwrap_or(Value::Null);
            if update.get("sessionUpdate").and_then(Value::as_str)
                == Some("available_commands_update")
            {
                if let Some(list) = update.get("availableCommands").and_then(Value::as_array) {
                    self.commands = list.clone();
                }
            }
        }
        Ok(session)
    }

    pub fn prompt(&mut self, session: &str, text: &str) -> Result<PromptTurn> {
        let permissions_before = self.permissions.len();
        let calls_before = self.client_calls.len();
        let reply = self.call(
            "session/prompt",
            json!({"sessionId": session, "prompt": [{"type": "text", "text": text}]}),
        )?;

        let trailing = self.drain_idle(Duration::from_millis(250), Duration::from_secs(5))?;

        let mut turn = PromptTurn {
            updates: reply
                .notes
                .iter()
                .chain(trailing.iter())
                .filter_map(|note| note.pointer("/params/update").cloned())
                .collect(),
            permissions: self.permissions[permissions_before..].to_vec(),
            client_calls: self.client_calls[calls_before..].to_vec(),
            error: reply.error.clone(),
            ..PromptTurn::default()
        };
        if let Some(result) = reply.result {
            turn.stop_reason = result
                .get("stopReason")
                .and_then(Value::as_str)
                .unwrap_or_default()
                .to_string();
        }
        Ok(turn)
    }

    pub fn session_list(&mut self) -> Result<Vec<Value>> {
        let result = self.call("session/list", json!({}))?.ok()?;
        Ok(result
            .get("sessions")
            .and_then(Value::as_array)
            .cloned()
            .unwrap_or_default())
    }

    pub fn drain_idle(&mut self, idle: Duration, cap: Duration) -> Result<Vec<Value>> {
        let deadline = Instant::now() + cap;
        let mut notes = Vec::new();
        loop {
            let window = idle.min(remaining(deadline));
            if window.is_zero() {
                return Ok(notes);
            }
            let message = match self.incoming.recv_timeout(window) {
                Ok(message) => message,
                Err(RecvTimeoutError::Timeout) => return Ok(notes),
                Err(RecvTimeoutError::Disconnected) => return Ok(notes),
            };
            if message.get("method").is_some() {
                if message.get("id").is_some() {
                    self.serve(&message)?;
                } else {
                    notes.push(message);
                }
            }
        }
    }

    pub fn drain(&mut self, want: usize, timeout: Duration) -> Result<Vec<Value>> {
        let deadline = Instant::now() + timeout;
        let mut notes = Vec::new();
        while notes.len() < want {
            let message = match self.incoming.recv_timeout(remaining(deadline)) {
                Ok(message) => message,
                Err(RecvTimeoutError::Timeout) => break,
                Err(RecvTimeoutError::Disconnected) => break,
            };
            if message.get("method").is_some() {
                if message.get("id").is_some() {
                    self.serve(&message)?;
                } else {
                    notes.push(message);
                }
            }
        }
        Ok(notes)
    }

    pub fn close(&mut self) -> Result<Option<i32>> {
        self.stdin.take();
        let deadline = Instant::now() + Duration::from_secs(30);
        loop {
            if let Some(status) = self.child.try_wait()? {
                return Ok(status.code());
            }
            if Instant::now() >= deadline {
                let _ = self.child.kill();
                bail!(
                    "{} did not exit after its stdin closed\n{}",
                    self.argv.join(" "),
                    self.notices()
                );
            }
            std::thread::sleep(Duration::from_millis(20));
        }
    }

    fn read(&mut self, deadline: Instant, method: &str) -> Result<Value> {
        match self.incoming.recv_timeout(remaining(deadline)) {
            Ok(message) => Ok(message),
            Err(RecvTimeoutError::Timeout) => bail!(
                "{} never answered {method}\n--- stderr (log lines dropped) ---\n{}\n",
                self.argv.join(" "),
                self.notices()
            ),
            Err(RecvTimeoutError::Disconnected) => bail!(
                "{} closed the stream while {method} was in flight\n--- stderr (log lines dropped) ---\n{}\n",
                self.argv.join(" "),
                self.notices()
            ),
        }
    }

    fn serve(&mut self, request: &Value) -> Result<()> {
        let id = request.get("id").cloned().unwrap_or(Value::Null);
        let method = request
            .get("method")
            .and_then(Value::as_str)
            .unwrap_or_default()
            .to_string();
        let params = request.get("params").cloned().unwrap_or(Value::Null);

        match method.as_str() {
            "session/request_permission" => {
                self.permissions.push(params.clone());
                if self.verdict == Verdict::Defer {
                    return Ok(());
                }
                let option = self.choose(&params);
                let outcome = match option {
                    Some(option) => json!({"outcome": {"outcome": "selected", "optionId": option}}),
                    None => json!({"outcome": {"outcome": "cancelled"}}),
                };
                self.reply(id, outcome)
            }
            "fs/read_text_file" => {
                self.client_calls.push((method, params.clone()));
                let path = params
                    .get("path")
                    .and_then(Value::as_str)
                    .unwrap_or_default()
                    .to_string();
                if self.unreadable.iter().any(|p| path.ends_with(p.as_str())) {
                    return self.fail(
                        id,
                        &format!("{path}: this client cannot hand that file over"),
                    );
                }
                match std::fs::read_to_string(&path) {
                    Ok(content) => self.reply(id, json!({"content": content})),
                    Err(err) => self.fail(id, &format!("{path}: {err}")),
                }
            }
            "fs/write_text_file" => {
                self.client_calls.push((method, params.clone()));
                let path = params
                    .get("path")
                    .and_then(Value::as_str)
                    .unwrap_or_default()
                    .to_string();
                if !self.performs_writes {
                    return self.fail(
                        id,
                        &format!("{path}: this client declined to write that file"),
                    );
                }
                let content = params
                    .get("content")
                    .and_then(Value::as_str)
                    .unwrap_or_default();
                match std::fs::write(&path, content) {
                    Ok(()) => self.reply(id, json!({})),
                    Err(err) => self.fail(id, &format!("{path}: {err}")),
                }
            }
            "terminal/create" => {
                self.client_calls.push((method, params.clone()));
                let terminal = self.open_terminal(&params);
                let terminal_id = terminal.id.clone();
                self.terminals.push(terminal);
                self.reply(id, json!({"terminalId": terminal_id}))
            }
            "terminal/wait_for_exit" => {
                self.client_calls.push((method, params.clone()));
                match self.terminal(&params) {
                    Some(terminal) => {
                        let code = terminal.exit_code;
                        self.reply(id, json!({"exitCode": code}))
                    }
                    None => self.fail(id, "no such terminal"),
                }
            }
            "terminal/output" => {
                self.client_calls.push((method, params.clone()));
                match self.terminal(&params) {
                    Some(terminal) => {
                        let output = terminal.output.clone();
                        let code = terminal.exit_code;
                        self.reply(
                            id,
                            json!({
                                "output": output,
                                "truncated": false,
                                "exitStatus": {"exitCode": code},
                            }),
                        )
                    }
                    None => self.fail(id, "no such terminal"),
                }
            }
            "terminal/kill" => {
                self.client_calls.push((method, params.clone()));
                self.reply(id, json!({}))
            }
            "terminal/release" => {
                self.client_calls.push((method, params.clone()));
                let wanted = params
                    .get("terminalId")
                    .and_then(Value::as_str)
                    .unwrap_or("");
                self.terminals.retain(|terminal| terminal.id != wanted);
                self.reply(id, json!({}))
            }
            other => {
                self.client_calls.push((method.clone(), params));
                self.fail(id, &format!("contenox-e2e serves no {other}"))
            }
        }
    }

    fn open_terminal(&mut self, params: &Value) -> Terminal {
        let id = format!("term-{}", self.terminals.len() + 1);
        let command = params
            .get("command")
            .and_then(Value::as_str)
            .unwrap_or_default();
        let args: Vec<String> = params
            .get("args")
            .and_then(Value::as_array)
            .map(|list| {
                list.iter()
                    .filter_map(Value::as_str)
                    .map(str::to_string)
                    .collect()
            })
            .unwrap_or_default();

        let mut child = Command::new(command);
        child.args(&args);
        if let Some(cwd) = params.get("cwd").and_then(Value::as_str) {
            child.current_dir(cwd);
        }
        match child.output() {
            Ok(done) => {
                let mut output = String::from_utf8_lossy(&done.stdout).into_owned();
                output.push_str(&String::from_utf8_lossy(&done.stderr));
                Terminal {
                    id,
                    output,
                    exit_code: done.status.code().unwrap_or(-1),
                }
            }
            Err(err) => Terminal {
                id,
                output: format!("{command}: {err}"),
                exit_code: 127,
            },
        }
    }

    fn terminal(&self, params: &Value) -> Option<&Terminal> {
        let wanted = params.get("terminalId").and_then(Value::as_str)?;
        self.terminals.iter().find(|terminal| terminal.id == wanted)
    }

    fn choose(&self, params: &Value) -> Option<String> {
        let options = params.get("options")?.as_array()?;
        let wanted: &[&str] = match self.verdict {
            Verdict::Allow => &["allow_once", "allow_always"],
            Verdict::Deny => &["reject_once", "reject_always"],
            Verdict::Defer | Verdict::Cancel => return None,
        };
        for kind in wanted {
            for option in options {
                if option.get("kind").and_then(Value::as_str) == Some(kind) {
                    return option
                        .get("optionId")
                        .and_then(Value::as_str)
                        .map(str::to_string);
                }
            }
        }
        None
    }

    fn reply(&mut self, id: Value, result: Value) -> Result<()> {
        self.write(&json!({"jsonrpc": "2.0", "id": id, "result": result}))
    }

    fn fail(&mut self, id: Value, message: &str) -> Result<()> {
        self.write(&json!({
            "jsonrpc": "2.0",
            "id": id,
            "error": {"code": -32603, "message": message},
        }))
    }

    fn write(&mut self, message: &Value) -> Result<()> {
        let handle = self
            .stdin
            .as_mut()
            .context("this client already closed the connection")?;
        handle.write_all(message.to_string().as_bytes())?;
        handle.write_all(b"\n")?;
        handle.flush()?;
        Ok(())
    }
}

impl Drop for Acp {
    fn drop(&mut self) {
        self.stdin.take();
        let _ = self.child.kill();
        let _ = self.child.wait();
    }
}

fn remaining(deadline: Instant) -> Duration {
    deadline.saturating_duration_since(Instant::now())
}
