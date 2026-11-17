mod common;

use common::serve_with;
use contenox_e2e::Script;

#[test]
fn a_saved_modeld_connection_survives_init_and_server_startup() {
    let name = "project-native";
    let endpoint = "127.0.0.1:1";
    let server = serve_with("modeld-registration", &Script::new().turn("hello"), |cx| {
        cx.run([
            "backend", "add", name, "--type", "modeld", "--url", endpoint,
        ])
        .ok();
        cx.run(["init", "modeld"]).ok();
        cx.run(["backend", "list"]).ok().expect_stdout(name);
    });
    let connections = server.json("GET", "/api/backends", None, 200);
    assert!(
        connections.as_array().unwrap().iter().any(|connection| {
            connection["name"] == name
                && connection["type"] == "modeld"
                && connection["baseUrl"] == endpoint
        }),
        "saved Modeld connection missing after server startup: {connections}"
    );
}
