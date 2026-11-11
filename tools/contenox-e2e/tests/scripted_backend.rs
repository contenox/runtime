use contenox_e2e::{Instance, Script};

fn started(label: &str) -> Instance {
    let cx = Instance::named(label).expect("scratch instance");
    cx.init().ok();
    cx
}

#[test]
fn doctor_reports_a_dialog_deleted_after_registration_as_the_backends_error() {
    let cx = started("scripted-doctor-gone");
    let dialog = cx
        .scripted(&Script::new().turn("hello"))
        .expect("scripted backend");
    std::fs::remove_file(&dialog).expect("delete the dialog");

    let out = cx.doctor().ok();
    assert!(
        out.stdout_has("Reachable backends:    0"),
        "a backend whose dialog is gone is not reachable:\n{}",
        out.render()
    );
    assert!(
        out.stdout_has(&format!(
            "Error: scripted-test script \"{}\" cannot be read",
            dialog.display()
        )),
        "the error names the dialog file:\n{}",
        out.render()
    );
}

#[test]
fn editing_the_dialog_rewinds_it_to_turn_one_inside_a_live_process() {
    let cx = started("scripted-rewind");
    let dialog = cx
        .scripted(&Script::new().route("general").turn("the first answer"))
        .expect("scripted backend");

    let mut acp = cx.acp(["acp"]).expect("spawn the ACP surface");
    acp.initialize().expect("initialize");
    let session = acp.new_session(cx.work()).expect("session/new");

    let first = acp
        .prompt(&session, "what changed?")
        .expect("session/prompt");
    assert_eq!(first.text(), "the first answer");

    std::fs::write(
        &dialog,
        Script::new()
            .route("general")
            .turn("the second answer, written while the process was live")
            .to_json(),
    )
    .expect("rewrite the dialog");

    let second = acp
        .prompt(&session, "and now?")
        .expect("a rewound dialog answers again rather than reporting itself exhausted");
    assert_eq!(
        second.text(),
        "the second answer, written while the process was live",
        "the edit rewound the dialog to turn 1"
    );
}
