
use contenox_e2e::{Instance, Pty, Script, SessionRow, Table, ToolCall, Turn, WorkspaceSessionRow};
use serde_json::Value;
use std::path::{Path, PathBuf};
use std::time::Duration;

const SLOW: Duration = Duration::from_secs(180);

fn instance(label: &str, answer: &str) -> Instance {
    let cx = Instance::named(label).expect("scratch instance");
    cx.init().ok();
    cx.scripted(&Script::new().turn(answer))
        .expect("scripted-test backend");
    cx
}

fn lands_reporting(summary: &str) -> Script {
    Script::new()
        .turn(
            Turn::new()
                .text("Filing what I found.")
                .call(ToolCall::mission_report("result", summary)),
        )
        .turn(Turn::new().call(ToolCall::mission_finish("landed")))
        .turn("Mission finished.")
}

fn rescript(cx: &Instance, script: &Script) {
    cx.write_script("scripted.json", script)
        .expect("rewrite the scripted dialog");
}

fn marked_project(cx: &Instance, name: &str) -> PathBuf {
    let dir = cx.work().join(name);
    std::fs::create_dir_all(&dir).expect("create the project directory");
    cx.cmd(["init", "--project"])
        .cwd(&dir)
        .output()
        .expect("contenox-cli init --project")
        .ok();
    dir
}

fn marker_id(project: &Path) -> String {
    let path = project.join(".contenox").join("workspace.id");
    let raw = std::fs::read_to_string(&path)
        .unwrap_or_else(|err| panic!("no marker at {}: {err}", path.display()));
    serde_json::from_str::<Value>(&raw)
        .ok()
        .and_then(|value| value.get("id").and_then(Value::as_str).map(str::to_string))
        .unwrap_or_else(|| raw.trim().to_string())
}

fn chat(cx: &Instance, dir: &Path, message: &str) {
    cx.cmd(["chat", message])
        .cwd(dir)
        .timeout(SLOW)
        .output()
        .expect("contenox-cli chat")
        .ok();
}

fn names(rows: &[SessionRow]) -> Vec<&str> {
    let mut out: Vec<&str> = rows.iter().map(|row| row.name.as_str()).collect();
    out.sort_unstable();
    out
}

fn active(rows: &[SessionRow]) -> Option<&str> {
    rows.iter()
        .find(|row| row.current)
        .map(|row| row.name.as_str())
}

fn messages_of(rows: &[SessionRow], name: &str) -> u32 {
    rows.iter()
        .find(|row| row.name == name)
        .unwrap_or_else(|| panic!("no session named {name:?} in {rows:?}"))
        .messages
}

fn row_named<'a>(rows: &'a [WorkspaceSessionRow], name: &str) -> &'a WorkspaceSessionRow {
    rows.iter()
        .find(|row| row.name == name)
        .unwrap_or_else(|| panic!("no session named {name:?} in {rows:?}"))
}

fn only_unit_session(rows: &[WorkspaceSessionRow]) -> &WorkspaceSessionRow {
    let units: Vec<&WorkspaceSessionRow> = rows
        .iter()
        .filter(|row| row.identity == "acp-client")
        .collect();
    match units.as_slice() {
        [only] => only,
        other => panic!("expected exactly one unit session, got {other:?}"),
    }
}

#[test]
fn a_new_session_becomes_the_active_one_and_the_star_marks_it() {
    let cx = Instance::named("session-new").expect("scratch instance");
    cx.init().ok();

    cx.run(["session", "new", "api"])
        .ok()
        .expect_stdout("Created session \"api\". Now active.");
    cx.run(["session", "new", "docs"]).ok();

    let rows = cx.sessions().expect("session list");
    assert_eq!(names(&rows), vec!["api", "docs"], "both sessions persist");
    assert_eq!(
        active(&rows),
        Some("docs"),
        "the star marks the session just created: {rows:?}"
    );
}
#[test]
fn deleting_the_active_session_leaves_none_active() {
    let cx = Instance::named("session-delete-active").expect("scratch instance");
    cx.init().ok();
    cx.run(["session", "new", "api"]).ok();

    cx.run(["session", "delete", "api"])
        .ok()
        .expect_stdout("was active");

    cx.run(["session", "show"])
        .expect_failure()
        .expect_stderr("no active session");
}


#[test]
fn a_plain_list_stays_in_this_workspace_while_list_all_reaches_the_database() {
    let cx = instance("session-scope", "noted");
    let api = marked_project(&cx, "api");
    let web = marked_project(&cx, "web");

    cx.cmd(["session", "new", "api-thread"])
        .cwd(&api)
        .output()
        .expect("session new")
        .ok();
    cx.cmd(["session", "new", "web-thread"])
        .cwd(&web)
        .output()
        .expect("session new")
        .ok();

    assert_eq!(
        names(&cx.sessions_in(&api).expect("session list")),
        vec!["api-thread"],
        "one instance serves exactly one workspace"
    );
    assert_eq!(
        names(&cx.sessions_in(&web).expect("session list")),
        vec!["web-thread"]
    );

    let all = cx.sessions_all().expect("session list --all");
    assert_eq!(row_named(&all, "api-thread").workspace, marker_id(&api));
    assert_eq!(row_named(&all, "web-thread").workspace, marker_id(&web));
}

#[test]
fn list_filters_the_whole_database_by_workspace_and_by_namespace() {
    let cx = instance("session-filters", "noted");
    let api = marked_project(&cx, "api");
    let web = marked_project(&cx, "web");

    cx.cmd(["session", "new", "api-thread"])
        .cwd(&api)
        .output()
        .expect("session new")
        .ok();
    cx.cmd(["session", "new", "web-thread"])
        .cwd(&web)
        .output()
        .expect("session new")
        .ok();

    cx.run(["session", "list", "--workspace", marker_id(&api).as_str()])
        .ok()
        .expect_stdout("api-thread")
        .refute_stdout("web-thread");

    cx.run(["session", "list", "--namespace", "web-thread"])
        .ok()
        .expect_stdout("web-thread")
        .refute_stdout("api-thread");

    cx.run([
        "session",
        "list",
        "--workspace",
        "00000000-0000-0000-0000-999999999999",
    ])
    .ok()
    .expect_stdout("No matching sessions.");
}

#[test]
fn the_workspace_is_the_marked_project_the_launch_directory_belongs_to() {
    let cx = instance("workspace-walk-up", "noted");
    let api = marked_project(&cx, "api");
    let deep = api.join("services").join("billing");
    std::fs::create_dir_all(&deep).expect("create the nested directory");

    cx.cmd(["session", "new", "api-thread"])
        .cwd(&deep)
        .output()
        .expect("session new")
        .ok();

    assert_eq!(
        names(&cx.sessions_in(&api).expect("session list")),
        vec!["api-thread"],
        "a session started deep inside the project is the project's"
    );
    assert_eq!(
        row_named(
            &cx.sessions_all().expect("session list --all"),
            "api-thread"
        )
        .workspace,
        marker_id(&api)
    );
    assert!(
        !deep.join(".contenox").exists(),
        "and no second workspace is minted on the way"
    );
}

#[test]
fn the_workspace_marker_travels_with_the_directory() {
    let cx = instance("workspace-travels", "noted");
    let api = marked_project(&cx, "api");
    cx.cmd(["session", "new", "api-thread"])
        .cwd(&api)
        .output()
        .expect("session new")
        .ok();
    let before = marker_id(&api);

    let moved = cx.root().join("relocated");
    std::fs::rename(&api, &moved).expect("move the project somewhere else");

    assert_eq!(
        marker_id(&moved),
        before,
        "the id belongs to the directory, not to its path"
    );
    assert_eq!(
        names(&cx.sessions_in(&moved).expect("session list")),
        vec!["api-thread"],
        "so the project's sessions are still the project's after the move"
    );
}
#[test]
fn the_control_plane_directory_is_not_a_workspace_of_its_own() {
    let cx = instance("workspace-control-plane", "noted");
    let api = marked_project(&cx, "api");
    cx.cmd(["session", "new", "api-thread"])
        .cwd(&api)
        .output()
        .expect("session new")
        .ok();

    let control_plane = api.join(".contenox");
    assert_eq!(
        names(&cx.sessions_in(&control_plane).expect("session list")),
        vec!["api-thread"],
        "standing in the control plane is still standing in the project it configures"
    );
    assert!(
        !control_plane.join(".contenox").exists(),
        "nothing mints a workspace inside the control plane"
    );
    assert_eq!(
        cx.sessions_all().expect("session list --all").len(),
        1,
        "and no second workspace appeared"
    );
}
