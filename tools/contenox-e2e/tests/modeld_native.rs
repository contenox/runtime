#![cfg(target_os = "linux")]

use contenox_e2e::Instance;
use std::fs;
use std::os::unix::fs::PermissionsExt;
use std::path::{Path, PathBuf};
use std::process::{Child, Command, Stdio};
use std::time::{Duration, Instant};

// The current CLI sends its built-in tool schemas on every agent chat turn.
const CHAT_CONTEXT: &str = "8192";

struct Worker(Child);

impl Worker {
    fn start(binary: &Path, root: &Path, log: &Path, mode: &str) -> Self {
        let mut command = Command::new(binary);
        if mode == "owner" {
            // Request the same native trace from the owner's child processes.
            let child_launcher = root.join("traced-modeld");
            fs::write(
                &child_launcher,
                "#!/bin/sh\nexec \"$TEST_MODELD_LAUNCHER\" \"$@\" --trace\n",
            )
            .unwrap();
            fs::set_permissions(&child_launcher, fs::Permissions::from_mode(0o700)).unwrap();
            command
                .env("CONTENOX_MODELD_BIN", child_launcher)
                .env("TEST_MODELD_LAUNCHER", binary);
        }
        let child = command
            .args([mode, "--trace", "--idle-ttl", "5m", "--data-root"])
            .arg(root)
            .env("CONTENOX_MODELD_BACKEND", "llama")
            .env("CONTENOX_LLAMA_GPU_LAYERS", "0")
            .env("CONTENOX_LLAMA_CTX", CHAT_CONTEXT)
            .env_remove("CONTENOX_WARM_SNAPSHOT_DISABLE")
            .env_remove("CONTENOX_WARM_SNAPSHOT_DIR")
            .stdout(Stdio::null())
            .stderr(fs::File::create(log).unwrap())
            .spawn()
            .unwrap();
        let mut worker = Self(child);
        let deadline = Instant::now() + Duration::from_secs(20);
        loop {
            assert!(
                worker.0.try_wait().unwrap().is_none(),
                "worker exited: {}",
                fs::read_to_string(log).unwrap()
            );
            let status = Command::new(binary)
                .args(["status", "--json", "--data-root"])
                .arg(root)
                .output()
                .unwrap();
            let record: serde_json::Value =
                serde_json::from_slice(&status.stdout).unwrap_or_default();
            if status.status.success()
                && record["pid"].as_u64() == Some(worker.0.id() as u64)
                && record["expired"].as_bool() == Some(false)
                && record["meta"]["endpoint"]
                    .as_str()
                    .is_some_and(|s| !s.is_empty())
            {
                break;
            }
            assert!(Instant::now() < deadline, "worker did not become ready");
            std::thread::sleep(Duration::from_millis(100));
        }
        worker
    }

    fn stop(&mut self) {
        assert_eq!(unsafe { libc::kill(self.0.id() as i32, libc::SIGTERM) }, 0);
        let deadline = Instant::now() + Duration::from_secs(25);
        loop {
            if let Some(status) = self.0.try_wait().unwrap() {
                assert!(status.success(), "worker shutdown: {status}");
                break;
            }
            assert!(
                Instant::now() < deadline,
                "worker did not shut down gracefully"
            );
            std::thread::sleep(Duration::from_millis(100));
        }
    }
}

impl Drop for Worker {
    fn drop(&mut self) {
        if self.0.try_wait().ok().flatten().is_none() {
            let _ = self.0.kill();
            let _ = self.0.wait();
        }
    }
}

#[test]
#[ignore = "native opt-in: requires CONTENOX_NATIVE_MODELD_BIN and CONTENOX_LLAMA_TINY_GGUF; no downloads"]
fn native_chat_restores_after_unload_and_worker_restart() {
    native_chat_restores("serve");
}

#[test]
#[ignore = "native opt-in: requires CONTENOX_NATIVE_MODELD_BIN and CONTENOX_LLAMA_TINY_GGUF; no downloads"]
fn shared_owner_restores_after_worker_replacement_and_restart() {
    native_chat_restores("owner");
}

fn native_chat_restores(mode: &str) {
    let binary = PathBuf::from(
        std::env::var_os("CONTENOX_NATIVE_MODELD_BIN").expect("native worker binary"),
    );
    let model = PathBuf::from(std::env::var_os("CONTENOX_LLAMA_TINY_GGUF").expect("local GGUF"));
    assert!(binary.is_file() && model.is_file());
    let mut cx = Instance::named("native-snapshots").unwrap();
    let root = cx.home_file("");
    cx.set_env("CONTENOX_DATA_ROOT", &root);
    cx.set_env("CONTENOX_MODELD_BIN", &binary);
    cx.init().ok();
    let model_dir = root.join("models/native-fixture");
    fs::create_dir_all(&model_dir).unwrap();
    std::os::unix::fs::symlink(&model, model_dir.join("model.gguf")).unwrap();
    let log = cx.root().join("worker.log");
    let mut worker = Worker::start(&binary, &root, &log, mode);
    cx.run(["backend", "add", "native", "--type", "modeld"])
        .ok();
    let chat = || {
        cx.cmd([
            "chat",
            "Say hello.",
            "--provider",
            "modeld",
            "--model",
            "native-fixture",
            "--context",
            CHAT_CONTEXT,
            "--max-tokens",
            "32",
            "--think",
            "off",
        ])
        .timeout(Duration::from_secs(120))
        .output()
        .unwrap()
        .ok()
    };
    let first = chat();
    assert!(!first.stdout.trim().is_empty(), "{}", first.render());
    cx.run(["model", "stop", "native-fixture"]).ok();
    let cache = root.join("modeld-snapshots/llama/slot-v1");
    assert!(fs::read_dir(&cache).unwrap().any(|entry| {
        entry
            .unwrap()
            .path()
            .extension()
            .is_some_and(|ext| ext == "snap")
    }));
    let second = chat();
    assert!(!second.stdout.trim().is_empty(), "{}", second.render());
    assert!(
        fs::read_to_string(&log)
            .unwrap()
            .contains("snapshot_restored")
    );
    let captures = fs::read_to_string(&log)
        .unwrap()
        .matches("snapshot_captured")
        .count();
    worker.stop();
    assert!(
        fs::read_to_string(&log)
            .unwrap()
            .matches("snapshot_captured")
            .count()
            > captures,
        "graceful shutdown did not capture the current session"
    );
    let restart_log = cx.root().join("worker-restart.log");
    let mut restarted = Worker::start(&binary, &root, &restart_log, mode);
    let third = chat();
    assert!(!third.stdout.trim().is_empty(), "{}", third.render());
    assert!(
        fs::read_to_string(&restart_log)
            .unwrap()
            .contains("snapshot_restored")
    );
    if mode == "owner" {
        cx.run(["model", "stop", "native-fixture"]).ok();
        let offset = fs::metadata(&restart_log).unwrap().len() as usize;
        cx.run(["session", "new", "isolated-native-chat"]).ok();
        let other = chat();
        assert!(!other.stdout.trim().is_empty(), "{}", other.render());
        let log = fs::read_to_string(&restart_log).unwrap();
        let first_open = log[offset..]
            .lines()
            .find(|line| {
                line.contains("subject=snapshot_cold_open")
                    || line.contains("subject=snapshot_restored")
            })
            .expect("the replacement worker reports its first open");
        assert!(first_open.contains("snapshot_cold_open"), "{first_open}");
    }
    restarted.stop();
}
