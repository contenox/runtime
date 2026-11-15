mod common;

use common::{Served, serve_with, wait_until};
use contenox_e2e::Script;
use serde_json::{Value, json};
use std::time::Duration;

const NOOP_AGENT: &str = "agent-apitest-noop";
const NOOP_DECLARATION: &str = include_str!("../../../apitests/fixtures/agent-apitest-noop.json");
const ENVELOPE: &str = "hitl-policy-default.json";

fn with_noop_agent() -> Served {
    let served = serve_with(
        "triggers-mission",
        &Script::new()
            .model("scripted-e2e")
            .route("general")
            .turn("Nothing to do."),
        |cx| {
            let dir = cx.work().join(".contenox");
            std::fs::create_dir_all(&dir).expect("the control-plane directory");
            std::fs::write(dir.join(format!("{NOOP_AGENT}.json")), NOOP_DECLARATION)
                .expect("declare the no-model agent");
        },
    );
    let (status, body) = served
        .request("GET", &format!("/api/agents/by-name/{NOOP_AGENT}"), None)
        .expect("look up the agent");
    assert_eq!(status, 200, "the no-model agent is declared: {body}");
    served
}

fn firings(served: &Served, trigger_id: &str) -> Vec<Value> {
    served.json("GET", "/api/workspace/triggers/firings", None, 200)["firings"]
        .as_array()
        .cloned()
        .unwrap_or_default()
        .into_iter()
        .filter(|f| f["triggerId"] == trigger_id)
        .collect()
}

#[test]
fn a_burst_of_test_edits_starts_one_mission_under_the_named_envelope() {
    let served = with_noop_agent();
    let trigger = served.json(
        "POST",
        "/api/workspace/triggers",
        Some(&json!({
            "name": "tests changed",
            "events": ["fs.modified", "fs.created"],
            "pathGlob": "**/*_test.go",
            "debounceMs": 500,
            "agentName": NOOP_AGENT,
            "hitlPolicyName": ENVELOPE,
            "prompt": "Run the changed tests and report what failed.",
            "enabled": true,
        })),
        201,
    );
    let trigger_id = trigger["id"].as_str().expect("a trigger id").to_string();
    wait_until("a dispatcher to hold the root", || {
        let listing = served.json("GET", "/api/workspace/triggers", None, 200);
        (listing["active"] == true).then_some(())
    });
    std::thread::sleep(Duration::from_millis(500));

    let work = served.cx.work();
    std::fs::create_dir_all(work.join("pkg")).expect("mkdir");
    std::fs::write(work.join("pkg/a_test.go"), "package pkg\n").expect("write a test");
    std::fs::write(work.join("pkg/a.go"), "package pkg\n").expect("write code");
    std::fs::write(work.join("b_test.go"), "package main\n").expect("write another test");

    let mut firing = None;
    for _ in 0..150 {
        let all = firings(&served, &trigger_id);
        if let Some(first) = all.first()
            && (first["status"] == "error" || first["missionId"].is_string())
        {
            firing = Some(first.clone());
            break;
        }
        std::thread::sleep(Duration::from_millis(100));
    }
    let Some(firing) = firing else {
        let mut served = served;
        panic!(
            "the trigger never fired; serve said:
{}",
            served.logs()
        );
    };
    assert_ne!(firing["status"], "error", "the mission started: {firing}");
    assert_eq!(firing["triggerName"], "tests changed");
    let mission_id = firing["missionId"].as_str().expect("a mission id");

    let mission = served.json("GET", &format!("/api/missions/{mission_id}"), None, 200);
    assert_eq!(mission["agentName"], NOOP_AGENT);
    assert_eq!(
        mission["hitlPolicyName"], ENVELOPE,
        "the mission runs under the envelope the trigger named"
    );
    let intent = mission["intent"].as_str().expect("an intent");
    assert!(
        intent.starts_with("Run the changed tests and report what failed."),
        "{intent}"
    );
    assert!(
        intent.contains("pkg/a_test.go") && intent.contains("b_test.go"),
        "{intent}"
    );
    assert!(
        !intent.contains("pkg/a.go,") && !intent.ends_with("pkg/a.go.)"),
        "only matching paths: {intent}"
    );

    std::fs::write(
        work.join("pkg/a_test.go"),
        "package pkg\n\nfunc TestX() {}\n",
    )
    .expect("edit again");
    std::thread::sleep(Duration::from_secs(3));
    assert_eq!(
        firings(&served, &trigger_id).len(),
        1,
        "a change while the triggered mission runs does not fire again"
    );
}

#[test]
fn a_scheduled_topic_wakes_its_mission_once_and_replay_is_a_new_event() {
    let served = with_noop_agent();
    let rule = served.json("POST", "/api/workspace/triggers", Some(&json!({
        "name":"nightly reviewer","events":["event.fired"],"topicGlob":"review.due",
        "agentName":NOOP_AGENT,"hitlPolicyName":ENVELOPE,"prompt":"Review the requested document."
    })),201);
    let preview = served.json(
        "GET",
        "/api/workspace/triggers/preview?type=event.fired&subject=review.due",
        None,
        200,
    );
    assert_eq!(preview["triggers"][0]["id"], rule["id"]);
    let timer = served.json(
        "POST",
        "/api/workspace/schedules",
        Some(&json!({"topic":"review.due","every":"2s","payload":{"file":"REVIEW.md"}})),
        201,
    );
    let id = rule["id"].as_str().unwrap();
    let firing = wait_until("scheduled mission", || {
        firings(&served, id)
            .into_iter()
            .find(|f| f["missionId"].is_string())
    });
    served.json(
        "PUT",
        &format!("/api/workspace/schedules/{}", timer["id"].as_str().unwrap()),
        Some(&json!({"enabled":false})),
        200,
    );
    let mission = served.json(
        "GET",
        &format!("/api/missions/{}", firing["missionId"].as_str().unwrap()),
        None,
        200,
    );
    assert!(mission["intent"].as_str().unwrap().contains("REVIEW.md"));
    assert_eq!(mission["hitlPolicyName"], ENVELOPE);
    let replay = served.json(
        "POST",
        &format!("/api/workspace/events/{}/replay", firing["nid"]),
        None,
        201,
    );
    assert_eq!(replay["replayedFrom"], firing["nid"]);
    assert_eq!(replay["data"]["file"], "REVIEW.md");
    assert!(replay["nid"].as_i64() > firing["nid"].as_i64());
}
