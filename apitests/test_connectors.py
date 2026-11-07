import os
import pathlib
import sys
import tempfile
import time

from helpers import api_url, assert_status_code, assert_status_in, unique_name

ECHO_SERVER = str(pathlib.Path(__file__).parent / "fixtures" / "mcp_echo_server.py")


def _create_mcp(api, base_url, payload):
    response = api.post(api_url(base_url, "mcp-servers"), json=payload, timeout=15)
    assert_status_code(response, 201)
    return response.json()


def _delete_mcp(api, base_url, server_id):
    response = api.delete(api_url(base_url, "mcp-servers", server_id), timeout=15)
    assert_status_code(response, 200)


def _check_until(api, base_url, name, status, timeout_s=30):
    deadline = time.monotonic() + timeout_s
    body = None
    while time.monotonic() < deadline:
        response = api.post(api_url(base_url, "connectors", name, "check"), timeout=40)
        assert_status_code(response, 200)
        body = response.json()
        if body["status"] == status:
            return body
        time.sleep(0.5)
    raise AssertionError(f"connector {name} never reached {status}: {body}")


def _connectors(api, base_url):
    response = api.get(api_url(base_url, "connectors"), timeout=60)
    assert_status_code(response, 200)
    return {item["name"]: item for item in response.json()}


def test_connectors_list_includes_builtin_tools(api, base_url):
    items = _connectors(api, base_url)
    builtin = [c for c in items.values() if c["kind"] == "builtin"]
    assert builtin, items
    for c in builtin:
        assert c["scope"] == "everywhere"
        assert c["status"] in ("ready", "unavailable", "needs_sign_in")
        assert isinstance(c["tools"], list)


def test_stdio_connector_gets_env_and_reports_its_tools(api, base_url):
    name = unique_name("apitest-echo")
    server = _create_mcp(api, base_url, {
        "name": name,
        "transport": "stdio",
        "command": sys.executable,
        "args": [ECHO_SERVER],
        "env": {"ECHO_TOOL_NAME": "shout"},
        "connectTimeoutSeconds": 20,
    })
    try:
        assert server["env"] == {"ECHO_TOOL_NAME": "shout"}
        checked = _check_until(api, base_url, name, "ready")
        assert checked["kind"] == "mcp"
        assert checked["transport"] == "stdio"
        assert checked["scope"] == "everywhere"
        assert [t["name"] for t in checked["tools"]] == ["shout"]

        listed = _connectors(api, base_url)[name]
        assert listed["status"] == "ready"
        assert listed["id"] == server["id"]
    finally:
        _delete_mcp(api, base_url, server["id"])

    assert name not in _connectors(api, base_url)


def test_workspace_connector_is_scoped_to_its_folder(api, base_url):
    name = unique_name("apitest-ws")
    with tempfile.TemporaryDirectory() as folder:
        server = _create_mcp(api, base_url, {
            "name": name,
            "transport": "stdio",
            "command": sys.executable,
            "args": [ECHO_SERVER],
            "workspace": folder,
            "connectTimeoutSeconds": 20,
        })
        try:
            assert os.path.normcase(server["workspace"]) == os.path.normcase(os.path.abspath(folder))
            listed = _check_until(api, base_url, name, "ready")
            assert listed["scope"] == "workspace"
            assert os.path.normcase(listed["workspace"]) == os.path.normcase(os.path.abspath(folder))
        finally:
            _delete_mcp(api, base_url, server["id"])


def test_workspace_must_be_an_existing_absolute_folder(api, base_url):
    for workspace in ("relative/folder", os.path.join(tempfile.gettempdir(), unique_name("missing"))):
        response = api.post(api_url(base_url, "mcp-servers"), json={
            "name": unique_name("apitest-badws"),
            "transport": "stdio",
            "command": sys.executable,
            "workspace": workspace,
        }, timeout=15)
        assert_status_code(response, 422)


def test_invalid_env_name_is_rejected(api, base_url):
    response = api.post(api_url(base_url, "mcp-servers"), json={
        "name": unique_name("apitest-badenv"),
        "transport": "stdio",
        "command": sys.executable,
        "env": {"BAD NAME": "x"},
    }, timeout=15)
    assert_status_code(response, 422)


def test_unreachable_remote_connector_reports_a_problem(api, base_url):
    name = unique_name("apitest-remote")
    server = _create_mcp(api, base_url, {
        "name": name,
        "transport": "http",
        "url": "http://127.0.0.1:9/mcp",
        "headers": {"X-Api-Key": "apitest"},
        "connectTimeoutSeconds": 3,
    })
    try:
        checked = _check_until(api, base_url, name, "unavailable")
        assert checked["transport"] == "http"
        assert checked["problem"]
        assert checked["tools"] == []
    finally:
        _delete_mcp(api, base_url, server["id"])


def test_check_unknown_connector_is_not_found(api, base_url):
    response = api.post(api_url(base_url, "connectors", unique_name("nope"), "check"), timeout=15)
    assert_status_code(response, 404)


def test_remote_tools_keep_their_workspace(api, base_url):
    name = unique_name("apitest-openapi")
    with tempfile.TemporaryDirectory() as folder:
        response = api.post(api_url(base_url, "tools", "remote"), json={
            "name": name,
            "endpointUrl": "http://127.0.0.1:9",
            "timeoutMs": 1000,
            "workspace": folder,
        }, timeout=15)
        assert_status_code(response, 201)
        tool_id = response.json()["id"]
        try:
            response = api.get(api_url(base_url, "tools", "remote", tool_id), timeout=15)
            assert_status_code(response, 200)
            assert os.path.normcase(response.json()["workspace"]) == os.path.normcase(os.path.abspath(folder))
        finally:
            response = api.delete(api_url(base_url, "tools", "remote", tool_id), timeout=15)
            assert_status_in(response, (200, 204))


def _hidden(api, base_url):
    response = api.get(api_url(base_url, "connectors", "hidden-tools"), timeout=15)
    assert_status_code(response, 200)
    return response.json()


def test_hidden_tools_round_trip_everywhere_and_per_folder(api, base_url):
    response = api.put(api_url(base_url, "connectors", "local_fs", "hidden-tools"), json={"tools": ["write_file", "local_fs.sed"]}, timeout=15)
    assert_status_code(response, 200)
    try:
        body = response.json()
        assert "local_fs.write_file" in body["everywhere"]
        assert "local_fs.sed" in body["everywhere"]
        local_fs = _connectors(api, base_url)["local_fs"]
        assert sorted(local_fs["hidden"][""]) == ["sed", "write_file"]

        with tempfile.TemporaryDirectory() as folder:
            response = api.put(api_url(base_url, "connectors", "local_shell", "hidden-tools"), json={"workspace": folder, "tools": ["local_shell"]}, timeout=15)
            assert_status_code(response, 200)
            workspaces = response.json()["workspaces"]
            assert any(os.path.normcase(k) == os.path.normcase(os.path.abspath(folder)) for k in workspaces)
            response = api.put(api_url(base_url, "connectors", "local_shell", "hidden-tools"), json={"workspace": folder, "tools": []}, timeout=15)
            assert_status_code(response, 200)
            assert response.json()["workspaces"] == {}
    finally:
        response = api.put(api_url(base_url, "connectors", "local_fs", "hidden-tools"), json={"tools": []}, timeout=15)
        assert_status_code(response, 200)
    assert not any(n.startswith("local_fs.") for n in _hidden(api, base_url)["everywhere"])


def test_hidden_tools_reject_bad_scope_and_names(api, base_url):
    response = api.put(api_url(base_url, "connectors", "local_fs", "hidden-tools"), json={"workspace": "relative", "tools": ["x"]}, timeout=15)
    assert_status_code(response, 422)
    response = api.put(api_url(base_url, "connectors", "bad.name", "hidden-tools"), json={"tools": ["x"]}, timeout=15)
    assert_status_code(response, 422)


def test_remote_tools_keep_inject_params_properties_and_login(api, base_url):
    name = unique_name("apitest-inject")
    payload = {
        "name": name,
        "endpointUrl": "http://127.0.0.1:9",
        "timeoutMs": 1000,
        "injectParams": {"tenant": "acme"},
        "properties": {"name": "X-Region", "value": "eu", "in": "header"},
        "authFlow": {
            "type": "http_handshake",
            "loginMethod": "POST",
            "loginUrl": "http://127.0.0.1:9/login",
            "loginBody": "{}",
            "extractJsonPath": "$.token",
            "injectHeader": "Authorization",
            "injectFormat": "Bearer %s",
        },
        "insecureSkipVerify": True,
    }
    response = api.post(api_url(base_url, "tools", "remote"), json=payload, timeout=15)
    assert_status_code(response, 201)
    tool_id = response.json()["id"]
    try:
        body = api.get(api_url(base_url, "tools", "remote", tool_id), timeout=15).json()
        assert body["injectParams"] == {"tenant": "acme"}
        assert body["properties"] == {"name": "X-Region", "value": "eu", "in": "header"}
        assert body["authFlow"]["extractJsonPath"] == "$.token"
        assert body["insecureSkipVerify"] is True
    finally:
        response = api.delete(api_url(base_url, "tools", "remote", tool_id), timeout=15)
        assert_status_in(response, (200, 204))


def test_remote_tools_reject_unknown_property_location(api, base_url):
    response = api.post(api_url(base_url, "tools", "remote"), json={
        "name": unique_name("apitest-badprop"),
        "endpointUrl": "http://127.0.0.1:9",
        "timeoutMs": 1000,
        "properties": {"name": "sid", "value": "x", "in": "cookie"},
    }, timeout=15)
    assert_status_code(response, 422)
