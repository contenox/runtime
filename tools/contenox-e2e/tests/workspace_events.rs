mod common;

use common::{PATIENCE, Served, serve, wait_until};
use contenox_e2e::{Script, ToolCall, Turn};
use serde_json::{Value, json};
use std::io::{BufRead, BufReader, Write};
use std::net::TcpStream;
use std::sync::mpsc::{Receiver, channel};
use std::time::{Duration, Instant};

impl Served {
    fn events_except_disk(&self) -> Vec<Value> {
        self.events()
            .into_iter()
            .filter(|e| e["source"] != "watcher")
            .collect()
    }

    fn wait_for_disk(&self, subject: &str) -> Value {
        let deadline = Instant::now() + PATIENCE;
        loop {
            if let Some(found) = self
                .events()
                .into_iter()
                .find(|e| e["source"] == "watcher" && e["subject"] == subject)
            {
                return found;
            }
            assert!(
                Instant::now() < deadline,
                "the watcher never reported {subject}"
            );
            std::thread::sleep(Duration::from_millis(100));
        }
    }

    fn prompt(&self, text: &str) -> Value {
        self.json(
            "POST",
            "/v1/chat/completions",
            Some(
                &json!({"model": "scripted-e2e", "messages": [{"role": "user", "content": text}]}),
            ),
            200,
        )
    }

    fn follow(&self) -> (Receiver<()>, Receiver<Value>) {
        let (ready_tx, ready_rx) = channel();
        let (event_tx, event_rx) = channel();
        let port = self.port;
        std::thread::spawn(move || {
            let Ok(mut stream) = TcpStream::connect(("127.0.0.1", port)) else {
                return;
            };
            let _ = stream.set_read_timeout(Some(PATIENCE));
            if stream
                .write_all(b"GET /api/workspace/events/stream HTTP/1.0\r\nHost: 127.0.0.1\r\nAccept: text/event-stream\r\n\r\n")
                .is_err()
            {
                return;
            }
            let mut announced = false;
            for line in BufReader::new(stream).lines() {
                let Ok(line) = line else { return };
                if !announced && line.starts_with(": connected") {
                    announced = true;
                    let _ = ready_tx.send(());
                }
                if let Some(payload) = line.strip_prefix("data: ")
                    && let Ok(event) = serde_json::from_str::<Value>(payload)
                    && event_tx.send(event).is_err()
                {
                    return;
                }
            }
        });
        (ready_rx, event_rx)
    }
}

fn next_from(live: &Receiver<Value>, source: &str) -> Value {
    let deadline = Instant::now() + PATIENCE;
    loop {
        let left = deadline.saturating_duration_since(Instant::now());
        let event = live
            .recv_timeout(left)
            .unwrap_or_else(|_| panic!("no {source} event reached the live stream"));
        if event["source"] == source {
            return event;
        }
    }
}

fn writes_a_note() -> Script {
    Script::new()
        .model("scripted-e2e")
        .route("coding")
        .turn(
            Turn::new().text("Writing the note.").call(
                ToolCall::new("local_fs.write_file")
                    .arg("path", "notes/todo.md")
                    .arg("content", "ship the event spine\n"),
            ),
        )
        .turn("The note is written.")
}

fn touches_a_marker() -> Script {
    Script::new()
        .model("scripted-e2e")
        .route("coding")
        .turn(
            Turn::new().text("Leaving a marker.").call(
                ToolCall::new("local_shell.local_shell")
                    .arg("command", "touch")
                    .arg("args", json!(["approved-marker.txt"])),
            ),
        )
        .turn("The marker is in place.")
}

#[test]
fn a_scripted_write_reaches_a_live_stream_and_the_cursor_log() {
    let served = serve("events-write", &writes_a_note());
    assert!(
        served.events().is_empty(),
        "a fresh workspace has no events"
    );

    let (ready, live) = served.follow();
    ready
        .recv_timeout(PATIENCE)
        .expect("the stream never announced itself");

    let answer = served.prompt("write the note");
    assert_eq!(
        answer["choices"][0]["message"]["content"], "The note is written.",
        "the scripted turn completed: {answer}"
    );

    let streamed = next_from(&live, "tool");
    assert_eq!(streamed["type"], "tool.wrote");
    assert_eq!(streamed["source"], "tool");
    assert_eq!(streamed["subject"], "notes/todo.md");
    assert_eq!(streamed["data"]["tool"], "local_fs.write_file");

    assert_eq!(
        served
            .cx
            .read_file("notes/todo.md")
            .expect("the note on disk"),
        "ship the event spine\n"
    );

    let seen = served.wait_for_disk("notes/todo.md");
    assert!(
        seen["type"] == "fs.created" || seen["type"] == "fs.modified",
        "the disk saw the agent's file: {seen}"
    );
    assert_eq!(
        seen["data"]["size"],
        "ship the event spine
"
        .len()
    );
    let streamed_disk = next_from(&live, "watcher");
    assert!(
        streamed_disk["subject"] == "notes" || streamed_disk["subject"] == "notes/todo.md",
        "the disk events reach the live stream too: {streamed_disk}"
    );

    let logged = served.events_except_disk();
    assert_eq!(logged.len(), 1, "one tool event for one write: {logged:?}");
    assert_eq!(logged[0]["nid"], streamed["nid"]);
    let disk: Vec<Value> = served
        .events()
        .into_iter()
        .filter(|e| e["source"] == "watcher")
        .collect();
    assert!(
        disk.iter().all(|e| !e["subject"]
            .as_str()
            .unwrap_or("")
            .contains(".contenox-tmp-")),
        "the atomic save's temp file stays out of the stream: {disk:?}"
    );

    let saved = served.json(
        "POST",
        "/api/workspace/events",
        Some(&json!({"type": "editor.saved", "subject": "notes/todo.md", "data": {"etag": "e2e"}})),
        201,
    );
    assert_eq!(saved["source"], "editor");
    assert!(
        saved["nid"].as_i64() > streamed["nid"].as_i64(),
        "the editor's event is ordered after the agent's"
    );
    let follow_up = next_from(&live, "editor");
    assert_eq!(follow_up["nid"], saved["nid"]);

    let since = streamed["nid"].as_i64().expect("a numeric cursor");
    let tail = served.json(
        "GET",
        &format!("/api/workspace/events?since={since}"),
        None,
        200,
    );
    let tail_events = tail["events"].as_array().expect("an events array");
    let after: Vec<&Value> = tail_events
        .iter()
        .filter(|e| e["source"] != "watcher")
        .collect();
    assert_eq!(after.len(), 1, "only the editor's event follows: {tail}");
    assert_eq!(after[0]["nid"], saved["nid"]);
    assert_eq!(tail["cursor"], tail_events.last().expect("a tail")["nid"]);

    let refused = served
        .request(
            "POST",
            "/api/workspace/events",
            Some(&json!({"type": "tool.wrote", "subject": "notes/todo.md"})),
        )
        .expect("post a forged tool event");
    assert_eq!(
        refused.0, 422,
        "an editor may not speak for the agent: {}",
        refused.1
    );
}

#[test]
fn an_approved_command_is_recorded_with_its_decision() {
    let served = serve("events-approval", &touches_a_marker());

    std::thread::scope(|scope| {
        let asking = scope.spawn(|| served.prompt("leave a marker"));

        let deadline = Instant::now() + PATIENCE;
        let approval = loop {
            let pending = served.json("GET", "/api/approvals", None, 200);
            if let Some(first) = pending.as_array().and_then(|rows| rows.first()) {
                break first.clone();
            }
            assert!(
                Instant::now() < deadline,
                "the command never asked for approval"
            );
            std::thread::sleep(Duration::from_millis(200));
        };
        assert_eq!(approval["toolsName"], "local_shell");
        let id = approval["id"].as_str().expect("an approval id").to_string();

        assert!(
            served.events().is_empty(),
            "nothing is recorded while the command waits for a person"
        );
        let (status, body) = served
            .request(
                "POST",
                &format!("/api/approvals/{id}"),
                Some(&json!({"approved": true})),
            )
            .expect("answer the approval");
        assert_eq!(status, 200, "approve: {body}");

        let answer = asking.join().expect("the prompt thread");
        assert_eq!(
            answer["choices"][0]["message"]["content"],
            "The marker is in place."
        );

        let events = served.events_except_disk();
        let kinds: Vec<&str> = events.iter().filter_map(|e| e["type"].as_str()).collect();
        assert_eq!(
            kinds,
            ["approval.decided", "tool.ran"],
            "decision first, then the run: {events:?}"
        );
        assert_eq!(events[0]["subject"], id.as_str());
        assert_eq!(events[0]["data"]["decision"], "approved");
        assert_eq!(events[0]["data"]["tool"], "local_shell.local_shell");
        assert_eq!(events[1]["subject"], "touch");
        assert_eq!(events[1]["data"]["exitCode"], 0);
    });

    assert!(
        served.cx.work().join("approved-marker.txt").is_file(),
        "the approved command ran in the workspace"
    );
    let seen = served.wait_for_disk("approved-marker.txt");
    assert_eq!(
        seen["type"], "fs.created",
        "the disk saw the marker: {seen}"
    );
}

fn idle() -> Script {
    Script::new()
        .model("scripted-e2e")
        .route("general")
        .turn("Nothing to do.")
}

fn watching(served: &Served, query: &str, dir: &std::path::Path) -> i64 {
    let mut probe = 0;
    wait_until("the watcher to report a probe file", || {
        std::fs::write(dir.join(format!("probe-{probe}.txt")), "probe").expect("write a probe");
        probe += 1;
        std::thread::sleep(Duration::from_millis(400));
        let seen = served.disk_events(query, 0, "probe-");
        seen.last().and_then(|e| e["nid"].as_i64())
    })
}

fn settled(served: &Served, query: &str, since: i64, prefix: &str, count: usize) -> Vec<Value> {
    wait_until(&format!("{count} disk events under {prefix}"), || {
        let found = served.disk_events(query, since, prefix);
        (found.len() >= count).then_some(())
    });
    std::thread::sleep(Duration::from_millis(1500));
    served.disk_events(query, since, prefix)
}

fn kinds(events: &[Value]) -> Vec<String> {
    events
        .iter()
        .map(|e| {
            format!(
                "{} {}",
                e["type"].as_str().unwrap_or(""),
                e["subject"].as_str().unwrap_or("")
            )
        })
        .collect()
}

#[test]
fn edits_on_disk_reach_the_stream_as_file_events() {
    let served = serve("events-disk", &idle());
    let work = served.cx.work().to_path_buf();
    let since = watching(&served, "", &work);

    std::fs::create_dir_all(work.join("disk")).expect("mkdir");
    settled(&served, "", since, "disk", 1);
    std::fs::write(work.join("disk/a.txt"), "first\n").expect("create");
    settled(&served, "", since, "disk", 2);
    std::fs::write(work.join("disk/a.txt"), "first\nsecond\n").expect("modify");
    settled(&served, "", since, "disk", 3);
    std::fs::create_dir_all(work.join("disk/node_modules/pkg")).expect("mkdir ignored");
    std::fs::write(work.join("disk/node_modules/pkg/index.js"), "x").expect("write ignored");
    std::fs::rename(work.join("disk/a.txt"), work.join("disk/b.txt")).expect("rename");
    settled(&served, "", since, "disk", 4);
    std::fs::remove_file(work.join("disk/b.txt")).expect("delete");
    let events = settled(&served, "", since, "disk", 5);

    assert_eq!(
        kinds(&events),
        [
            "fs.created disk",
            "fs.created disk/a.txt",
            "fs.modified disk/a.txt",
            "fs.moved disk/b.txt",
            "fs.deleted disk/b.txt",
        ],
        "one event per change, the ignored folder silent: {events:?}"
    );
    assert_eq!(events[0]["data"]["isDir"], true);
    assert_eq!(events[1]["data"]["size"], "first\n".len());
    assert_eq!(events[2]["data"]["size"], "first\nsecond\n".len());
    assert_eq!(events[3]["data"]["from"], "disk/a.txt");
    assert_eq!(events[4]["data"], json!({"isDir": false}));
}

#[test]
fn a_root_granted_while_serving_is_watched_on_its_own_stream() {
    let served = serve("events-granted", &idle());
    let default_since = watching(&served, "", served.cx.work());

    let second = served.cx.root().join("second-project");
    std::fs::create_dir_all(&second).expect("the second project");
    let granted = served.json(
        "POST",
        "/api/workspace/roots",
        Some(&json!({"path": second.display().to_string(), "name": "second"})),
        200,
    );
    let root = granted["roots"]
        .as_array()
        .expect("roots")
        .iter()
        .filter_map(|r| r["path"].as_str())
        .find(|p| p.ends_with("second-project"))
        .expect("the granted root")
        .to_string();
    let query = format!("root={root}");

    let since = watching(&served, &query, &second);
    std::fs::write(second.join("real.txt"), "watched").expect("write in the second root");
    let events = settled(&served, &query, since, "real", 1);
    assert_eq!(kinds(&events), ["fs.created real.txt"]);

    let leaked = served.disk_events("", default_since, "real");
    assert!(
        leaked.is_empty(),
        "the default root's stream never carries another root's files: {leaked:?}"
    );
}
