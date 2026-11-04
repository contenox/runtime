import json
import os
import shutil
import tempfile
from pathlib import Path

import pytest

from helpers import api_url, assert_status_code, unique_name


def _cfg(base_url, *parts, **query):
    return api_url(base_url, "agents-config", *parts, **query)


def _ws_query(folder):
    return {"scope": "workspace", "workspace": folder}


def _comment_lines(raw):
    return [line.strip() for line in raw.splitlines() if line.strip().startswith("#")]


@pytest.fixture()
def restore_home(api, base_url):
    before = api.get(_cfg(base_url), timeout=15)
    assert_status_code(before, 200)
    yield before.json()
    restored = api.put(_cfg(base_url), json={"raw": before.json()["raw"]}, timeout=15)
    assert_status_code(restored, 200)


@pytest.fixture()
def workspace_folder(api, base_url):
    folder = tempfile.mkdtemp(prefix="apitest-workspace-")
    response = api.post(api_url(base_url, "workspace/roots"), json={"path": folder, "name": "apitest"}, timeout=15)
    assert_status_code(response, 200)
    granted = next(r["path"] for r in response.json()["roots"] if os.path.samefile(r["path"], folder))
    yield granted
    api.delete(api_url(base_url, "workspace/roots", path=granted), timeout=15)
    shutil.rmtree(folder, ignore_errors=True)


def _envelope_names(api, base_url, **query):
    response = api.get(api_url(base_url, "envelopes", **query), timeout=15)
    assert_status_code(response, 200)
    return {e["name"]: e for e in response.json()}


def test_document_reports_the_file_its_layer_and_the_sections(api, base_url, restore_home):
    doc = restore_home
    assert doc["scope"] == "home"
    assert doc["exists"] is True
    assert doc["path"].endswith("agents.toml")
    assert len(doc["revision"]) == 64
    assert "[envelopes.strict]" in doc["raw"]
    assert doc["layer"]["envelopes"]["strict"]["default_action"] == "deny"
    assert doc["problems"] == []
    kinds = {s["name"]: s["kind"] for s in doc["sections"]}
    assert kinds["envelopes"] == "named"
    assert kinds["tools"] == "flat"
    assert kinds["chain"] == "singleton"


def test_envelope_create_get_update_delete_and_it_takes_effect_at_once(api, base_url, restore_home):
    name = unique_name("apitest-env").replace("-", "_")
    value = {
        "extends": "read_only",
        "description": "Created through the API.",
        "compute": {"max_tool_calls": 123},
        "shell": {"grant": "approve", "blacklist": ["mkfs"]},
    }

    response = api.post(_cfg(base_url, "envelopes"), json={"name": name, "value": value}, timeout=15)
    assert_status_code(response, 201)
    created = response.json()
    assert created["name"] == name and created["inLayer"] is True
    assert created["layer"]["compute"]["max_tool_calls"] == 123
    assert created["effective"]["description"] == "Created through the API."

    raw = api.get(_cfg(base_url), timeout=15).json()["raw"]
    assert "max_tool_calls = 123" in raw, "a JSON integer is written as a TOML integer"
    assert f"[envelopes.{name}]" in raw

    assert name in _envelope_names(api, base_url), "the envelope appears in the pickers without a restart"

    value["description"] = "Changed through the API."
    response = api.put(_cfg(base_url, "envelopes", name), json={"value": value}, timeout=15)
    assert_status_code(response, 200)
    assert response.json()["effective"]["description"] == "Changed through the API."
    assert _envelope_names(api, base_url)[name]["description"] == "Changed through the API."

    response = api.get(_cfg(base_url, "envelopes", name), timeout=15)
    assert_status_code(response, 200)
    assert response.json()["layer"]["shell"]["blacklist"] == ["mkfs"]

    response = api.delete(_cfg(base_url, "envelopes", name), timeout=15)
    assert_status_code(response, 200)
    assert response.json()["name"] == name

    assert_status_code(api.get(_cfg(base_url, "envelopes", name), timeout=15), 404)
    assert name not in _envelope_names(api, base_url)
    assert f"[envelopes.{name}" not in api.get(_cfg(base_url), timeout=15).json()["raw"]


def test_an_edit_leaves_every_other_comment_and_envelope_alone(api, base_url, restore_home):
    before = restore_home["raw"]
    name = unique_name("apitest-keep").replace("-", "_")

    assert_status_code(
        api.post(_cfg(base_url, "envelopes"), json={"name": name, "value": {"extends": "read_only"}}, timeout=15),
        201,
    )
    assert_status_code(
        api.put(_cfg(base_url, "envelopes", "strict"), json={"value": {"extends": "ask_always", "default_action": "deny", "description": "Edited."}}, timeout=15),
        200,
    )
    after = api.get(_cfg(base_url), timeout=15).json()

    assert after["raw"].startswith(before.split("[envelopes.strict]")[0].rsplit("\n\n", 1)[0]), "everything above the edit is byte for byte the same"
    kept = set(_comment_lines(after["raw"]))
    missing = [line for line in _comment_lines(before) if line not in kept]
    assert len(missing) < 12, f"only comments inside the replaced envelope may go, but {len(missing)} vanished: {missing[:5]}"
    assert "# The deliberate step up from default" in after["raw"], "the comment above the edited envelope stays"
    assert after["layer"]["envelopes"]["acpx"] == restore_home["layer"]["envelopes"]["acpx"]
    assert after["problems"] == []


def test_create_twice_conflicts_and_missing_items_are_not_found(api, base_url, restore_home):
    name = unique_name("apitest-dup").replace("-", "_")
    body = {"name": name, "value": {"extends": "read_only"}}
    assert_status_code(api.post(_cfg(base_url, "envelopes"), json=body, timeout=15), 201)
    assert_status_code(api.post(_cfg(base_url, "envelopes"), json=body, timeout=15), 409)

    ghost = unique_name("apitest-ghost").replace("-", "_")
    assert_status_code(api.put(_cfg(base_url, "envelopes", ghost), json={"value": {"extends": "read_only"}}, timeout=15), 404)
    assert_status_code(api.delete(_cfg(base_url, "envelopes", ghost), timeout=15), 404)
    assert_status_code(api.get(_cfg(base_url, "envelopes", ghost), timeout=15), 404)


def test_a_change_that_would_break_the_config_is_refused_and_the_file_is_untouched(api, base_url, restore_home):
    revision = restore_home["revision"]
    name = unique_name("apitest-bad").replace("-", "_")

    response = api.post(_cfg(base_url, "envelopes"), json={"name": name, "value": {"extends": "no_such_envelope"}}, timeout=15)
    assert_status_code(response, 422)
    assert name in response.text and "nothing was written" in response.text

    response = api.post(_cfg(base_url, "envelopes"), json={"name": name, "value": {"nonsense_key": True}}, timeout=15)
    assert_status_code(response, 422)

    for bad_name in ("has space", "dot.ted", "", "-lead"):
        response = api.post(_cfg(base_url, "envelopes"), json={"name": bad_name, "value": {}}, timeout=15)
        assert_status_code(response, 422)

    response = api.post(_cfg(base_url, "envelopes"), json={"name": name, "value": "not an object"}, timeout=15)
    assert_status_code(response, 422)
    assert_status_code(api.post(_cfg(base_url, "envelopes"), data="{not json", headers={"Content-Type": "application/json"}, timeout=15), 400)

    assert api.get(_cfg(base_url), timeout=15).json()["revision"] == revision, "no refused request changed the file"


def test_revisions_stop_two_editors_overwriting_each_other(api, base_url, restore_home):
    stale = restore_home["revision"]
    first = unique_name("apitest-rev").replace("-", "_")
    response = api.post(_cfg(base_url, "envelopes"), json={"name": first, "value": {"extends": "read_only"}, "revision": stale}, timeout=15)
    assert_status_code(response, 201)
    fresh = response.json()["revision"]
    assert fresh != stale

    second = unique_name("apitest-rev").replace("-", "_")
    response = api.post(_cfg(base_url, "envelopes"), json={"name": second, "value": {"extends": "read_only"}, "revision": stale}, timeout=15)
    assert_status_code(response, 409)
    assert "changed since you read it" in response.text

    response = api.post(
        _cfg(base_url, "envelopes"),
        json={"name": second, "value": {"extends": "read_only"}},
        headers={"If-Match": f'"{stale}"'},
        timeout=15,
    )
    assert_status_code(response, 409)

    response = api.post(
        _cfg(base_url, "envelopes"),
        json={"name": second, "value": {"extends": "read_only"}},
        headers={"If-Match": f'"{fresh}"'},
        timeout=15,
    )
    assert_status_code(response, 201)

    assert_status_code(api.delete(_cfg(base_url, "envelopes", first), headers={"If-Match": stale}, timeout=15), 409)


def test_flat_sections_edit_one_line_at_a_time(api, base_url, restore_home):
    before_lines = restore_home["raw"].count("\n")
    key = unique_name("ApiTool").replace("-", "_")

    response = api.post(_cfg(base_url, "tools"), json={"name": key, "value": "local_fs.read_file"}, timeout=15)
    assert_status_code(response, 201)
    assert response.json()["effective"] == "local_fs.read_file"
    raw = api.get(_cfg(base_url), timeout=15).json()["raw"]
    assert f'{key} = "local_fs.read_file"' in raw
    assert raw.count("\n") == before_lines + 1

    response = api.put(_cfg(base_url, "tools", key), json={"value": "local_fs.list_dir"}, timeout=15)
    assert_status_code(response, 200)
    assert response.json()["layer"] == "local_fs.list_dir"

    listing = api.get(_cfg(base_url, "tools"), timeout=15)
    assert_status_code(listing, 200)
    items = {item["name"]: item for item in listing.json()["items"]}
    assert items[key]["inLayer"] is True
    assert items["Read"]["effective"] == "local_fs.read_file", "neighbours are untouched"

    assert_status_code(api.post(_cfg(base_url, "tools"), json={"name": key, "value": "again"}, timeout=15), 409)
    assert_status_code(api.post(_cfg(base_url, "tools"), json={"name": unique_name("Obj"), "value": {"not": "text"}}, timeout=15), 422)
    assert_status_code(api.delete(_cfg(base_url, "tools", key), timeout=15), 200)
    assert f"\n{key} = " not in api.get(_cfg(base_url), timeout=15).json()["raw"]
    assert_status_code(api.post(_cfg(base_url, "tools"), json={"name": unique_name("Multi"), "value": "two\nlines"}, timeout=15), 422)


def test_singleton_sections_are_read_replaced_and_reset(api, base_url, restore_home):
    default_rounds = api.get(_cfg(base_url, "chain"), timeout=15).json()["effective"]["main_rounds"]
    wanted = default_rounds + 3

    response = api.put(_cfg(base_url, "chain"), json={"value": {"main_rounds": wanted, "retry_policy": {"max_attempts": 2}}}, timeout=15)
    assert_status_code(response, 200)
    body = response.json()
    assert body["kind"] == "singleton" and body["inLayer"] is True
    assert body["effective"]["main_rounds"] == wanted
    assert body["layer"]["retry_policy"]["max_attempts"] == 2

    assert_status_code(api.delete(_cfg(base_url, "chain"), timeout=15), 200)
    after = api.get(_cfg(base_url, "chain"), timeout=15).json()
    assert after["inLayer"] is False
    assert after["effective"]["main_rounds"] == default_rounds, "removing the section hands the setting back to the shipped default"
    assert_status_code(api.delete(_cfg(base_url, "chain"), timeout=15), 404)

    assert_status_code(api.put(_cfg(base_url, "envelopes"), json={"value": {}}, timeout=15), 400)
    assert_status_code(api.post(_cfg(base_url, "chain"), json={"name": "x", "value": {}}, timeout=15), 400)
    assert_status_code(api.get(_cfg(base_url, "chain", "x"), timeout=15), 404)


def test_agents_and_tool_policy_overlays_round_trip(api, base_url, restore_home):
    agent = unique_name("apitest-agent")
    response = api.post(_cfg(base_url, "agents"), json={"name": agent, "value": {"chain": {"main_rounds": 5}}}, timeout=15)
    assert_status_code(response, 201)
    assert response.json()["layer"]["chain"]["main_rounds"] == 5
    assert_status_code(api.delete(_cfg(base_url, "agents", agent), timeout=15), 200)

    response = api.post(_cfg(base_url, "tools_policies"), json={"name": "local_fs", "value": {"_max_read_bytes": "1024"}}, timeout=15)
    assert response.status_code in (201, 409), response.text
    if response.status_code == 409:
        response = api.put(_cfg(base_url, "tools_policies", "local_fs"), json={"value": {"_max_read_bytes": "1024"}}, timeout=15)
        assert_status_code(response, 200)
    assert response.json()["effective"]["_max_read_bytes"] == "1024"


def test_the_whole_file_can_be_read_and_replaced(api, base_url, restore_home):
    current = restore_home
    assert_status_code(api.put(_cfg(base_url), json={"raw": "[envelopes.x\nbroken"}, timeout=15), 422)
    assert_status_code(api.put(_cfg(base_url), json={"raw": current["raw"], "revision": "0" * 64}, timeout=15), 409)

    edited = current["raw"] + "\n# added by the whole-file test\n"
    response = api.put(_cfg(base_url), json={"raw": edited, "revision": current["revision"]}, timeout=15)
    assert_status_code(response, 200)
    assert response.json()["raw"] == edited
    assert response.json()["revision"] != current["revision"]
    assert "# added by the whole-file test" in api.get(_cfg(base_url), timeout=15).json()["raw"]


def test_unknown_sections_and_bad_scopes_are_rejected(api, base_url, restore_home):
    assert_status_code(api.get(_cfg(base_url, "nothing"), timeout=15), 404)
    assert_status_code(api.get(_cfg(base_url, scope="elsewhere"), timeout=15), 400)
    assert_status_code(api.get(_cfg(base_url, scope="workspace"), timeout=15), 400)
    response = api.get(_cfg(base_url, **_ws_query(tempfile.gettempdir())), timeout=15)
    assert_status_code(response, 422)
    assert "granted workspace" in response.text


def test_a_workspace_has_its_own_layer_that_never_leaks(api, base_url, restore_home, workspace_folder):
    home_revision = restore_home["revision"]
    query = _ws_query(workspace_folder)
    layer_file = Path(workspace_folder) / ".contenox" / "agents.toml"

    doc = api.get(_cfg(base_url, **query), timeout=15)
    assert_status_code(doc, 200)
    assert doc.json()["exists"] is False and doc.json()["raw"] == "" and doc.json()["scope"] == "workspace"

    name = unique_name("apitest-ws").replace("-", "_")
    response = api.post(_cfg(base_url, "envelopes", **query), json={"name": name, "value": {"extends": "read_only", "description": "Only here."}}, timeout=15)
    assert_status_code(response, 201)
    assert response.json()["scope"] == "workspace" and response.json()["inLayer"] is True
    assert layer_file.is_file(), "the workspace's own .contenox/agents.toml was created"
    assert f"[envelopes.{name}]" in layer_file.read_text(encoding="utf-8")

    scoped = _envelope_names(api, base_url, workspace=workspace_folder)
    assert name in scoped and scoped[name]["scope"] == "workspace"
    assert name not in _envelope_names(api, base_url), "the shared list never shows a workspace's envelope"
    assert api.get(_cfg(base_url), timeout=15).json()["revision"] == home_revision, "the global file was not touched"

    assert_status_code(api.get(_cfg(base_url, "envelopes", name), timeout=15), 404)
    item = api.get(_cfg(base_url, "envelopes", name, **query), timeout=15)
    assert_status_code(item, 200)
    assert item.json()["effective"]["description"] == "Only here."

    assert_status_code(api.delete(_cfg(base_url, "envelopes", name, **query), timeout=15), 200)
    assert name not in _envelope_names(api, base_url, workspace=workspace_folder)


def test_a_workspace_overrides_a_global_envelope_only_when_it_says_so(api, base_url, restore_home, workspace_folder):
    query = _ws_query(workspace_folder)

    inherited = api.get(_cfg(base_url, "envelopes", "strict", **query), timeout=15)
    assert_status_code(inherited, 200)
    assert inherited.json()["inLayer"] is False, "the workspace's own file says nothing about strict yet"
    original = inherited.json()["effective"]["description"]

    assert_status_code(api.put(_cfg(base_url, "envelopes", "strict", **query), json={"value": {"extends": "ask_always"}}, timeout=15), 404)

    response = api.post(
        _cfg(base_url, "envelopes", **query),
        json={"name": "strict", "value": {"extends": "ask_always", "default_action": "deny", "description": "Stricter here."}},
        timeout=15,
    )
    assert_status_code(response, 201)
    assert response.json()["effective"]["description"] == "Stricter here."

    here = _envelope_names(api, base_url, workspace=workspace_folder)["strict"]
    assert here["scope"] == "workspace" and here["description"].startswith("Stricter here.")
    elsewhere = _envelope_names(api, base_url)["strict"]
    assert elsewhere["description"].startswith(original.split(".")[0]), "everywhere else, strict is unchanged"
    assert "scope" not in elsewhere or not elsewhere["scope"]


def test_a_workspace_edit_is_checked_against_the_global_file_too(api, base_url, restore_home, workspace_folder):
    query = _ws_query(workspace_folder)
    name = unique_name("apitest-child").replace("-", "_")
    response = api.post(_cfg(base_url, "envelopes", **query), json={"name": name, "value": {"extends": "strict"}}, timeout=15)
    assert_status_code(response, 201)
    assert response.json()["effective"]["extends"] == "strict", "a workspace envelope can extend a global one"

    response = api.post(_cfg(base_url, "envelopes", **query), json={"name": name + "x", "value": {"extends": "nope"}}, timeout=15)
    assert_status_code(response, 422)


def test_patch_changes_only_what_it_names_inside_an_envelope(api, base_url, restore_home):
    name = unique_name("apitest-patch").replace("-", "_")
    value = {
        "extends": "read_only",
        "description": "Before.",
        "compute": {"max_tool_calls": 100, "max_tokens": 5000},
        "shell": {"grant": "approve", "blacklist": ["mkfs", "shred"]},
        "network": {"read": "allow"},
    }
    assert_status_code(api.post(_cfg(base_url, "envelopes"), json={"name": name, "value": value}, timeout=15), 201)

    response = api.patch(
        _cfg(base_url, "envelopes", name),
        json={"value": {"description": "After.", "compute": {"max_tool_calls": 7}, "shell": {"blacklist": ["mkfs"]}, "network": None}},
        timeout=15,
    )
    assert_status_code(response, 200)
    layer = response.json()["layer"]
    assert layer["description"] == "After."
    assert layer["extends"] == "read_only", "what the patch did not name is kept"
    assert layer["compute"] == {"max_tool_calls": 7, "max_tokens": 5000}, "nested objects merge key by key"
    assert layer["shell"] == {"grant": "approve", "blacklist": ["mkfs"]}, "arrays are replaced whole"
    assert "network" not in layer, "null removes a key"

    raw = api.get(_cfg(base_url), timeout=15).json()["raw"]
    assert "max_tool_calls = 7" in raw and "max_tokens = 5000" in raw
    assert _envelope_names(api, base_url)[name]["description"] == "After.", "a patch takes effect at once, like any change"


def test_patch_accepts_a_plain_merge_patch_body_and_checks_revisions(api, base_url, restore_home):
    name = unique_name("apitest-merge").replace("-", "_")
    created = api.post(_cfg(base_url, "envelopes"), json={"name": name, "value": {"extends": "read_only", "description": "One."}}, timeout=15)
    assert_status_code(created, 201)
    revision = created.json()["revision"]

    response = api.patch(
        _cfg(base_url, "envelopes", name),
        data=json.dumps({"description": "Two."}),
        headers={"Content-Type": "application/merge-patch+json", "If-Match": f'"{revision}"'},
        timeout=15,
    )
    assert_status_code(response, 200)
    assert response.json()["layer"]["description"] == "Two."

    response = api.patch(
        _cfg(base_url, "envelopes", name),
        data=json.dumps({"description": "Three."}),
        headers={"Content-Type": "application/merge-patch+json", "If-Match": f'"{revision}"'},
        timeout=15,
    )
    assert_status_code(response, 409)
    assert api.get(_cfg(base_url, "envelopes", name), timeout=15).json()["layer"]["description"] == "Two."


def test_patch_is_refused_when_it_would_break_the_config_or_the_item_is_missing(api, base_url, restore_home):
    revision = restore_home["revision"]
    assert_status_code(api.patch(_cfg(base_url, "envelopes", "strict"), json={"value": {"extends": "no_such_envelope"}}, timeout=15), 422)
    assert_status_code(api.patch(_cfg(base_url, "envelopes", "strict"), json={"value": {"nonsense_key": True}}, timeout=15), 422)
    assert_status_code(api.patch(_cfg(base_url, "envelopes", "strict"), json={"value": "not an object"}, timeout=15), 422)
    assert_status_code(api.patch(_cfg(base_url, "envelopes", unique_name("ghost").replace("-", "_")), json={"value": {"description": "x"}}, timeout=15), 404)
    assert_status_code(api.patch(_cfg(base_url, "envelopes"), json={"value": {}}, timeout=15), 400)
    assert_status_code(api.patch(_cfg(base_url, "chain", "x"), json={"value": {}}, timeout=15), 404)
    assert api.get(_cfg(base_url), timeout=15).json()["revision"] == revision, "no refused patch changed the file"


def test_patching_a_singleton_section_sets_one_knob_and_keeps_the_rest(api, base_url, restore_home, workspace_folder):
    query = _ws_query(workspace_folder)
    inherited = api.get(_cfg(base_url, "chain", **query), timeout=15).json()
    assert inherited["inLayer"] is False

    response = api.patch(_cfg(base_url, "chain", **query), json={"value": {"main_rounds": inherited["effective"]["main_rounds"] + 2}}, timeout=15)
    assert_status_code(response, 200)
    body = response.json()
    assert body["inLayer"] is True
    assert body["effective"]["main_rounds"] == inherited["effective"]["main_rounds"] + 2
    assert body["effective"]["recovery_rounds"] == inherited["effective"]["recovery_rounds"], "everything else is inherited"

    response = api.patch(_cfg(base_url, "chain", **query), json={"value": {"recovery_rounds": 3}}, timeout=15)
    assert_status_code(response, 200)
    assert response.json()["layer"] == {"main_rounds": inherited["effective"]["main_rounds"] + 2, "recovery_rounds": 3}, "a second patch keeps the first"

    assert api.get(_cfg(base_url), timeout=15).json()["revision"] == restore_home["revision"], "the global file was not touched"
