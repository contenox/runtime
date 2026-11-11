mod common;

use common::{Served, serve};
use contenox_e2e::{Script, ToolCall, Turn};
use serde_json::{Value, json};

fn reads_then_writes() -> Script {
    Script::new()
        .model("scripted-e2e")
        .route("coding")
        .turn(
            Turn::new()
                .text("Reading the note.")
                .call(ToolCall::new("local_fs.read_file").arg("path", "notes.md")),
        )
        .turn("I have read it.")
        .route("coding")
        .turn(
            Turn::new().text("Rewriting the note.").call(
                ToolCall::new("local_fs.write_file")
                    .arg("path", "notes.md")
                    .arg("content", "the agent's version\n"),
            ),
        )
        .turn("I tried to rewrite it.")
}

fn read(served: &Served, path: &str) -> Value {
    served.json("GET", &format!("/api/files/content?path={path}"), None, 200)
}

fn save(served: &Served, path: &str, content: &str, etag: &str) -> (u16, Value) {
    let if_match = format!("\"{etag}\"");
    let (status, _, body) = served
        .request_with(
            "PUT",
            "/api/files",
            Some(&json!({"path": path, "content": content})),
            &[("If-Match", if_match.as_str())],
        )
        .expect("save through the file API");
    let parsed = serde_json::from_str(&body).unwrap_or_else(|_| json!({"raw": body}));
    (status, parsed)
}

#[test]
fn the_agent_guard_and_the_editor_refuse_the_same_stale_write() {
    let served = serve("editor-contract", &reads_then_writes());
    std::fs::write(served.cx.work().join("notes.md"), "v1\n").expect("seed the note");

    let opened = read(&served, "notes.md");
    let seen = opened["etag"].as_str().expect("an etag").to_string();
    assert_eq!(opened["content"], "v1\n");

    let mut acp = served.cx.acp(["acp"]).expect("spawn the ACP surface");
    acp.initialize().expect("initialize");
    let session = acp.new_session(served.cx.work()).expect("session/new");

    let first = acp
        .prompt(&session, "read the note")
        .expect("the reading turn");
    assert!(
        first.error.is_none(),
        "the read turn failed: {:?}",
        first.error
    );

    let (status, saved) = save(&served, "notes.md", "the editor's version\n", &seen);
    assert_eq!(
        status, 200,
        "the editor saves on top of what it opened: {saved}"
    );
    let current = saved["etag"].as_str().expect("the new etag").to_string();
    assert_ne!(current, seen);

    let second = acp
        .prompt(&session, "rewrite the note")
        .expect("the writing turn");
    let transcript = serde_json::to_string(&second.updates).expect("updates as JSON");
    assert!(
        transcript.contains("changed since you read it"),
        "the agent's guard refuses a write based on the version it read: {transcript}"
    );
    assert!(
        second.written_paths().is_empty(),
        "the refused write never reached the client: {:?}",
        second.client_calls
    );

    let (status, refused) = save(&served, "notes.md", "an editor buffer from before\n", &seen);
    assert_eq!(
        status, 409,
        "the editor refuses the same stale base: {refused}"
    );
    assert_eq!(refused["error"]["code"], "file_changed");
    assert_eq!(refused["error"]["currentEtag"], current.as_str());

    assert_eq!(
        served.cx.read_file("notes.md").expect("the note on disk"),
        "the editor's version\n",
        "both refusals left the accepted save in place"
    );
    assert_eq!(read(&served, "notes.md")["etag"], current.as_str());
}
