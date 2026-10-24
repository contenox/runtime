
use contenox_e2e::{Acp, Instance, Script, ToolCall, Turn, Verdict};
use serde_json::{Value, json};
use std::time::Duration;

fn editor(label: &str, script: &Script) -> Instance {
    let cx = Instance::named(label).expect("scratch instance");
    cx.init().ok();
    cx.scripted(script).expect("scripted-test backend");
    cx
}

fn write_note(content: &str) -> Vec<Turn> {
    vec![
        Turn::new().text("Writing the note.").call(
            ToolCall::new("write_file")
                .arg("path", "notes.txt")
                .arg("content", content),
        ),
        Turn::new().text("That is done."),
    ]
}

fn picker<'a>(init: &'a Value, id: &str) -> &'a Value {
    init.get("_meta")
        .and_then(|meta| meta.get("contenox.workspaceConfigOptions"))
        .and_then(Value::as_array)
        .expect("initialize advertises the workspace config options")
        .iter()
        .find(|option| option["id"] == id)
        .unwrap_or_else(|| panic!("no {id:?} picker in {init:#}"))
}

fn handshake(cx: &Instance, argv: &[&str]) -> (Acp, Value) {
    let mut acp = cx.acp(argv).expect("spawn the ACP surface");
    let init = acp.initialize().expect("initialize");
    (acp, init)
}


#[test]
fn acp_speaks_the_protocol_over_stdio_and_answers_a_prompt() {
    let cx = editor(
        "acp-stdio",
        &Script::new().route("general").turn("Two files changed."),
    );
    let (mut acp, init) = handshake(&cx, &["acp"]);

    assert_eq!(init["protocolVersion"], json!(1));
    assert_eq!(init["agentInfo"]["name"], json!("contenox"));
    let capabilities = &init["agentCapabilities"];
    assert_eq!(capabilities["loadSession"], json!(true));
    assert!(
        capabilities["sessionCapabilities"]["list"].is_object(),
        "session/list must be advertised: {capabilities:#}"
    );

    let session = acp.new_session(cx.work()).expect("session/new");
    assert!(!session.is_empty(), "session/new must return a session id");

    let turn = acp
        .prompt(&session, "what changed?")
        .expect("session/prompt");
    assert_eq!(turn.stop_reason, "end_turn");
    assert_eq!(turn.text(), "Two files changed.");
}

#[test]
fn acpx_speaks_the_same_protocol_for_a_headless_driver() {
    let cx = editor("acpx-stdio", &Script::new().turn("Reporting in."));
    let (mut acp, init) = handshake(&cx, &["acpx"]);

    assert_eq!(init["protocolVersion"], json!(1));
    assert_eq!(init["agentInfo"]["name"], json!("contenox"));

    let session = acp.new_session(cx.work()).expect("session/new");
    let turn = acp.prompt(&session, "report").expect("session/prompt");
    assert_eq!(turn.stop_reason, "end_turn");
    assert_eq!(turn.text(), "Reporting in.");
}

#[test]
fn a_prompt_streams_the_answer_with_the_usage_and_title_a_client_renders() {
    let cx = editor(
        "acp-streaming",
        &Script::new().route("general").turn("Two files changed."),
    );
    let (mut acp, _) = handshake(&cx, &["acp"]);
    let session = acp.new_session(cx.work()).expect("session/new");

    let turn = acp
        .prompt(&session, "what changed?")
        .expect("session/prompt");

    let kinds = turn.kinds();
    assert!(
        kinds
            .iter()
            .filter(|k| **k == "agent_message_chunk")
            .count()
            > 1,
        "the answer arrives in chunks, not one blob: {kinds:?}"
    );
    assert!(
        kinds.contains(&"usage_update"),
        "the client is told what the turn cost: {kinds:?}"
    );
    let info = turn
        .of_kind("session_info_update")
        .first()
        .copied()
        .cloned()
        .unwrap_or_else(|| panic!("no session_info_update in {kinds:?}"));
    assert_eq!(
        info["title"],
        json!("what changed?"),
        "the first user message titles the session"
    );
}

#[test]
fn a_gated_write_asks_the_editors_permission_ui_and_shows_the_diff() {
    let cx = editor(
        "acp-permission",
        &Script::new()
            .route("coding")
            .turns(write_note("approved\n")),
    );
    let (mut acp, _) = handshake(&cx, &["acp"]);
    let session = acp.new_session(cx.work()).expect("session/new");

    let turn = acp
        .prompt(&session, "write a note")
        .expect("session/prompt");

    let ask = turn
        .permissions
        .first()
        .unwrap_or_else(|| panic!("the gated write must reach the client's permission UI"));
    assert_eq!(ask["sessionId"], json!(session));
    assert_eq!(
        ask["toolCall"]["title"],
        json!("local_fs.write_file: notes.txt")
    );
    assert_eq!(ask["toolCall"]["rawInput"]["path"], json!("notes.txt"));
    let kinds: Vec<&str> = ask["options"]
        .as_array()
        .expect("the ask offers options")
        .iter()
        .filter_map(|option| option["kind"].as_str())
        .collect();
    assert!(
        kinds.contains(&"allow_once") && kinds.contains(&"reject_once"),
        "the editor is offered allow and deny, got {kinds:?}"
    );

    let completed = turn
        .tool_call_updates()
        .into_iter()
        .find(|update| update["status"] == json!("completed"))
        .unwrap_or_else(|| panic!("the allowed call must complete: {:#?}", turn.updates));
    let diff = &completed["content"][0];
    assert_eq!(diff["type"], json!("diff"));
    assert_eq!(
        diff["path"],
        json!(cx.work().join("notes.txt").display().to_string())
    );
    assert_eq!(diff["newText"], json!("approved\n"));

    assert_eq!(turn.stop_reason, "end_turn");
    assert_eq!(cx.read_file("notes.txt").expect("the note"), "approved\n");
}

#[test]
fn the_session_works_in_the_directory_the_editor_opened() {
    let cx = editor(
        "acp-cwd",
        &Script::new()
            .route("coding")
            .turns(write_note("in project\n")),
    );
    let project = cx.work().join("project");
    std::fs::create_dir_all(&project).expect("the project directory");

    let (mut acp, _) = handshake(&cx, &["acp"]);
    let session = acp.new_session(&project).expect("session/new");

    let listed = acp.session_list().expect("session/list");
    assert_eq!(
        listed[0]["cwd"],
        json!(project.display().to_string()),
        "session/list reports the cwd the editor sent"
    );

    let turn = acp
        .prompt(&session, "write a note")
        .expect("session/prompt");

    assert_eq!(
        turn.written_paths(),
        vec![project.join("notes.txt").display().to_string()],
        "a relative path resolves against the editor's cwd, not the agent's"
    );
    assert!(
        project.join("notes.txt").is_file(),
        "the file lands in the project the editor has open"
    );
    assert!(
        !cx.work().join("notes.txt").exists(),
        "and not in the directory the agent process was started in"
    );
}
#[test]
fn the_hardened_profile_never_offers_mission() {
    let cx = editor("acpx-commands", &Script::new().turn("hello"));
    let (mut acp, _) = handshake(&cx, &["acpx"]);
    acp.new_session(cx.work()).expect("session/new");

    assert!(
        !acp.offers("mission"),
        "an untrusted driver may not fire missions: {:?}",
        acp.commands()
    );
    assert!(
        !acp.offers("plan"),
        "and /plan dispatches the same way: {:?}",
        acp.commands()
    );
    assert!(
        acp.offers("policy") && acp.offers("help"),
        "the rest of the menu is still served: {:?}",
        acp.commands()
    );
}


fn plant_flat_chain(cx: &Instance, at: std::path::PathBuf) -> std::path::PathBuf {
    let source = cx.home_file(".generated/chain-agent-acpx.json");
    if let Some(parent) = at.parent() {
        std::fs::create_dir_all(parent).expect("the destination directory");
    }
    std::fs::copy(&source, &at)
        .unwrap_or_else(|err| panic!("copy {} to {}: {err}", source.display(), at.display()));
    at
}

fn ran_the_flat_chain(cx: &Instance) {
    let steps = cx.executed_tasks().expect("contenox-cli state show");
    assert_eq!(
        steps[0].task, "chain-acpx-agent",
        "the planted chain is the one that ran, got {steps:?}"
    );
}
#[test]
fn a_named_chain_path_that_is_missing_is_a_hard_error() {
    let cx = editor("acp-chain-missing", &Script::new().turn("never reached"));

    cx.cmd(["acp"])
        .env("CONTENOX_ACP_CHAIN_PATH", cx.work().join("nowhere.json"))
        .stdin("")
        .timeout(Duration::from_secs(60))
        .output()
        .expect("contenox-cli acp")
        .expect_code(1)
        .expect_stderr("nowhere.json\" not found")
        .expect_stderr("CONTENOX_ACP_CHAIN_PATH");
}


#[test]
fn an_editor_session_transcript_is_readable_from_the_cli_afterwards() {
    let cx = editor(
        "acp-transcript",
        &Script::new().route("general").turn("Two files changed."),
    );
    let (mut acp, _) = handshake(&cx, &["acp"]);
    let session = acp.new_session(cx.work()).expect("session/new");
    acp.prompt(&session, "what changed?")
        .expect("session/prompt");
    acp.close()
        .expect("the agent exits when its client hangs up");

    let rows = cx.sessions_all().expect("contenox-cli session list --all");
    let row = rows
        .iter()
        .find(|row| row.name == session)
        .unwrap_or_else(|| panic!("the editor's session is missing from {rows:?}"));
    assert_eq!(row.identity, "acp-client");
    assert_eq!(row.messages, "2");

    let shown = cx.session_show(&row.id).ok();
    assert!(
        shown.stdout_has("what changed?") && shown.stdout_has("Two files changed."),
        "the whole exchange is readable back:\n{}",
        shown.render()
    );
}
