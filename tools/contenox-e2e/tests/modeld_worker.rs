#![cfg(target_os = "linux")]

//! The worker reads against a real modeld owner.
//!
//! The live contract target holds the worker operations on a machine with no
//! local execution: there they answer the refusal this runtime gives when no
//! owner is reachable, which is not the answer a user with local inference
//! reads. This witness supplies the other half. It needs a modeld built with a
//! backend, because a build that selects no backend (`backend=none`) starts and
//! holds the lease while being able to serve nothing:
//!
//! ```text
//! make -n build-modeld MODELD_HAVE_OPENVINO=0      # prints the command
//! bin/modeld owner --data-root <scratch> --listen 127.0.0.1:0
//! ```
//!
//! `owner` is the mode the runtime's worker reads address: it serves the shared
//! owner the probe requires (`backend=shared`) and spawns the backend children
//! itself. A `serve` owner advertises `backend=llama` and is what a *slot*
//! answers on; the runtime's shared client refuses it by name.
//!
//! A resident worker is what `_contenox/workers/inspect` inspects, and a worker
//! exists only once a model is loaded. This witness therefore holds the device
//! inventory and the owner snapshot — the empty worker list with a live owner is
//! the shape a machine with local inference but nothing resident reports — and
//! asserts the inspection refusal names the pool state rather than claiming a
//! payload no worker produced. Holding a successful inspection needs a model:
//! set `CONTENOX_LLAMA_TINY_GGUF` to a local GGUF and load it through a role
//! before this witness can be extended to it. It is not acquired here, because
//! which model an installation loads is the operator's configuration.

use contenox_e2e::{Acp, Instance};
use serde_json::{Value, json};
use std::fs;
use std::path::{Path, PathBuf};
use std::process::{Child, Command, Stdio};
use std::time::{Duration, Instant};

/// Owner runs one shared modeld owner for the scratch instance, and takes it
/// down with the test.
struct Owner(Child);

impl Owner {
    fn start(binary: &Path, root: &Path, log: &Path) -> Self {
        let child = Command::new(binary)
            .arg("owner")
            .arg("--data-root")
            .arg(root)
            .arg("--listen")
            .arg("127.0.0.1:0")
            .env("CONTENOX_MODELD_BACKEND", "llama")
            .stdout(Stdio::null())
            .stderr(fs::File::create(log).expect("owner log"))
            .spawn()
            .expect("spawn the modeld owner");
        let mut owner = Self(child);
        let deadline = Instant::now() + Duration::from_secs(30);
        loop {
            assert!(
                owner.0.try_wait().expect("owner state").is_none(),
                "modeld owner exited: {}",
                fs::read_to_string(log).unwrap_or_default()
            );
            let status = Command::new(binary)
                .args(["status", "--json", "--data-root"])
                .arg(root)
                .output()
                .expect("modeld status");
            let record: Value = serde_json::from_slice(&status.stdout).unwrap_or_default();
            if status.status.success()
                && record["pid"].as_u64() == Some(owner.0.id() as u64)
                && record["meta"]["backend"] == json!("shared")
                && record["meta"]["endpoint"]
                    .as_str()
                    .is_some_and(|endpoint| !endpoint.is_empty())
            {
                break;
            }
            assert!(
                Instant::now() < deadline,
                "the owner did not take the shared lease: {}",
                fs::read_to_string(log).unwrap_or_default()
            );
            std::thread::sleep(Duration::from_millis(100));
        }
        owner
    }
}

impl Owner {
    /// holds reports whether this process still holds the shared lease: a live
    /// owner is what every read above was answered by, so the witness holds the
    /// lease rather than the text of a log line.
    fn holds(&self, binary: &Path, root: &Path) -> bool {
        let status = Command::new(binary)
            .args(["status", "--json", "--data-root"])
            .arg(root)
            .output()
            .expect("modeld status");
        let record: Value = serde_json::from_slice(&status.stdout).unwrap_or_default();
        status.status.success()
            && record["pid"].as_u64() == Some(self.0.id() as u64)
            && record["expired"].as_bool() == Some(false)
            && record["meta"]["backend"] == json!("shared")
    }
}

impl Drop for Owner {
    fn drop(&mut self) {
        if self.0.try_wait().ok().flatten().is_none() {
            let _ = self.0.kill();
            let _ = self.0.wait();
        }
    }
}

fn owner_binary() -> PathBuf {
    let binary = PathBuf::from(
        std::env::var_os("CONTENOX_NATIVE_MODELD_BIN")
            .expect("CONTENOX_NATIVE_MODELD_BIN names the modeld this witness holds"),
    );
    assert!(
        binary.is_file(),
        "CONTENOX_NATIVE_MODELD_BIN={} is not a file; build it with `make build-modeld`",
        binary.display()
    );
    binary
}

fn call(acp: &mut Acp, method: &str, params: Value) -> Value {
    let reply = acp
        .call(method, params)
        .unwrap_or_else(|err| panic!("{method}: {err:#}"));
    assert_eq!(
        reply.error_code(),
        0,
        "{method} answered {}: {}",
        reply.error_code(),
        reply.error_message()
    );
    reply.result.expect("a result")
}

#[test]
#[ignore = "native opt-in: requires CONTENOX_NATIVE_MODELD_BIN pointing at a modeld built with a backend"]
fn the_worker_reads_answer_with_a_live_owner() {
    let binary = owner_binary();
    let mut cx = Instance::named("modeld-worker").expect("scratch instance");
    let root = cx.home_file("");
    cx.init().ok();
    cx.set_env("CONTENOX_MODELD_BIN", binary.as_os_str());
    cx.set_env("CONTENOX_MODELD_BACKEND", "llama");
    cx.set_env("CONTENOX_DATA_ROOT", root.as_os_str());
    let log = cx.root().join("owner.log");
    let owner = Owner::start(&binary, &root, &log);

    let mut acp: Acp = cx.acp(["acp"]).expect("spawn the ACP surface");
    acp.initialize().expect("initialize");

    let inventory = call(&mut acp, "_contenox/workers/inventory", json!({}));
    assert!(
        inventory["host_threads"]
            .as_u64()
            .is_some_and(|threads| threads > 0),
        "the native inventory reports the host's usable threads: {inventory}"
    );
    let backends = inventory["backends"]
        .as_array()
        .unwrap_or_else(|| panic!("the inventory names the native backends: {inventory}"));
    assert!(
        !backends.is_empty(),
        "a modeld built with a backend reports that backend: {inventory}"
    );
    assert!(
        backends
            .iter()
            .any(|backend| backend["backend"] == json!("llama")),
        "the built backend is the one the inventory reports: {inventory}"
    );
    assert!(
        inventory["pools"]
            .as_array()
            .is_some_and(|pools| !pools.is_empty()),
        "the inventory names the pools a worker allocates from: {inventory}"
    );
    for backend in backends {
        assert!(
            !backend["backend"].as_str().unwrap_or_default().is_empty()
                && !backend["state"].as_str().unwrap_or_default().is_empty(),
            "every backend names itself and the state it is in rather than nothing: {inventory}"
        );
        assert!(
            backend["devices"].is_array(),
            "every backend answers a device list, empty when it places work on the host: {inventory}"
        );
    }
    let available = backends
        .iter()
        .find(|backend| backend["backend"] == json!("llama"))
        .expect("the compiled backend");
    assert_eq!(
        available["state"],
        json!("available"),
        "the backend this modeld was built with is available: {inventory}"
    );
    assert!(
        available["operations"]
            .as_array()
            .is_some_and(|operations| !operations.is_empty()),
        "an available backend names the operations it serves: {inventory}"
    );
    assert!(
        available["runtime_digest"]
            .as_str()
            .is_some_and(|digest| !digest.is_empty()),
        "an available backend reports the runtime it was linked against: {inventory}"
    );

    let snapshot = call(&mut acp, "_contenox/workers/status", json!({}));
    assert_eq!(
        snapshot["owner_state"],
        json!("ready"),
        "the shared owner holds the lease and answers, so the state a surface reads is ready: {snapshot}"
    );
    assert_eq!(
        snapshot["workers"],
        json!([]),
        "nothing is resident, and an empty list is the answer rather than an absent member: {snapshot}"
    );
    assert!(
        snapshot["owner_error"]
            .as_str()
            .unwrap_or_default()
            .is_empty(),
        "a live owner reports no error: {snapshot}"
    );
    assert!(
        !snapshot["options"]
            .as_object()
            .unwrap_or(&serde_json::Map::new())
            .is_empty(),
        "the status carries the reviewed options of the roles this installation has: {snapshot}"
    );

    let refused = acp
        .call(
            "_contenox/workers/inspect",
            json!({"instance": "not-resident"}),
        )
        .expect("a reply");
    assert_ne!(
        refused.error_code(),
        -32601,
        "_contenox/workers/inspect is declared and mounted: {refused:?}"
    );
    assert!(
        refused
            .error_message()
            .contains("no longer owned by this daemon"),
        "the live owner answers that the named worker is not in its pool, which is the state a successful inspection would replace: {}",
        refused.error_message()
    );

    acp.close().expect("the editor hangs up");
    assert!(
        owner.holds(&binary, &root),
        "the owner this witness held served the whole run: {}",
        fs::read_to_string(&log).unwrap_or_default()
    );
}
