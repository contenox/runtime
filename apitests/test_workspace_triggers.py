import contextlib
import os

import pytest

from helpers import api_url, assert_status_code, unique_name


FIXTURE_AGENT = "agent-apitest-noop"
ENVELOPE = "hitl-policy-default.json"


def _triggers_url(base_url, *parts, **query):
    return api_url(base_url, "workspace/triggers", *parts, **query)


def _default_root(api, base_url):
    response = api.get(api_url(base_url, "workspace/roots"), timeout=15)
    assert_status_code(response, 200)
    return next(r["path"] for r in response.json()["roots"] if r["default"])


def _body(name, **overrides):
    body = {
        "name": name,
        "events": ["fs.modified", "fs.created"],
        "pathGlob": "**/*_test.go",
        "debounceMs": 500,
        "agentName": FIXTURE_AGENT,
        "hitlPolicyName": ENVELOPE,
        "prompt": "Run the changed tests and report what failed.",
        "enabled": True,
    }
    body.update(overrides)
    return body


def _agent_present(api, base_url):
    return api.get(api_url(base_url, "agents", "by-name", FIXTURE_AGENT), timeout=15).status_code == 200


@pytest.fixture()
def created(api, base_url):
    made = []

    def create(body, **query):
        response = api.post(_triggers_url(base_url, **query), json=body, timeout=15)
        if response.status_code == 201:
            made.append((response.json()["id"], query))
        return response

    yield create
    for trigger_id, query in made:
        with contextlib.suppress(Exception):
            api.delete(_triggers_url(base_url, trigger_id, **query), timeout=15)


def test_a_trigger_is_created_listed_changed_and_removed(api, base_url, created):
    if not _agent_present(api, base_url):
        pytest.skip(f"{FIXTURE_AGENT} is not declared in this runtime")
    name = unique_name("trigger")
    response = created(_body(name))
    assert_status_code(response, 201)
    trigger = response.json()
    assert trigger["id"].startswith("trg_")
    assert trigger["events"] == ["fs.modified", "fs.created"]
    assert trigger["running"] is False
    assert os.path.samefile(trigger["root"], _default_root(api, base_url))

    listing = api.get(_triggers_url(base_url), timeout=15).json()
    assert "fs.deleted" in listing["events"]
    assert listing["active"] is True, "serve runs a dispatcher for its default root"
    assert [t["name"] for t in listing["triggers"] if t["id"] == trigger["id"]] == [name]

    assert_status_code(created(_body(name)), 409)

    changed = api.put(_triggers_url(base_url, trigger["id"]), json=_body(name, enabled=False, debounceMs=1000), timeout=15)
    assert_status_code(changed, 200)
    assert changed.json()["enabled"] is False
    assert changed.json()["debounceMs"] == 1000

    assert_status_code(api.delete(_triggers_url(base_url, trigger["id"]), timeout=15), 200)
    assert_status_code(api.delete(_triggers_url(base_url, trigger["id"]), timeout=15), 404)
    assert_status_code(api.put(_triggers_url(base_url, trigger["id"]), json=_body(name), timeout=15), 404)


@pytest.mark.parametrize(
    "overrides",
    [
        {"agentName": "no-such-agent-apitest"},
        {"hitlPolicyName": "no-such-envelope-apitest.json"},
        {"hitlPolicyName": ""},
        {"events": ["tool.wrote"]},
        {"events": []},
        {"prompt": ""},
        {"debounceMs": 10},
        {"pathGlob": "/etc/*"},
    ],
)
def test_a_trigger_that_could_not_run_is_refused(api, base_url, created, overrides):
    if not _agent_present(api, base_url):
        pytest.skip(f"{FIXTURE_AGENT} is not declared in this runtime")
    assert_status_code(created(_body(unique_name("bad"), **overrides)), 422)


def test_triggers_belong_to_their_root(api, base_url, created, tmp_path):
    if not _agent_present(api, base_url):
        pytest.skip(f"{FIXTURE_AGENT} is not declared in this runtime")
    granted = api.post(api_url(base_url, "workspace/roots"), json={"path": str(tmp_path), "name": "apitest-triggers"}, timeout=15)
    assert_status_code(granted, 200)
    root = next(r["path"] for r in granted.json()["roots"] if os.path.samefile(r["path"], tmp_path))
    try:
        response = created(_body(unique_name("elsewhere")), root=root)
        assert_status_code(response, 201)
        trigger_id = response.json()["id"]
        default_ids = [t["id"] for t in api.get(_triggers_url(base_url), timeout=15).json()["triggers"]]
        assert trigger_id not in default_ids
        other_ids = [t["id"] for t in api.get(_triggers_url(base_url, root=root), timeout=15).json()["triggers"]]
        assert other_ids == [trigger_id]
        assert_status_code(api.delete(_triggers_url(base_url, trigger_id), timeout=15), 404)
    finally:
        with contextlib.suppress(Exception):
            for t in api.get(_triggers_url(base_url, root=root), timeout=15).json()["triggers"]:
                api.delete(_triggers_url(base_url, t["id"], root=root), timeout=15)
        api.delete(api_url(base_url, "workspace/roots", path=root), timeout=15)
    assert_status_code(api.get(_triggers_url(base_url, root=str(tmp_path)), timeout=15), 422)


def test_enabled_defaults_on_and_an_update_without_it_keeps_it(api, base_url, created):
    if not _agent_present(api, base_url):
        pytest.skip(f"{FIXTURE_AGENT} is not declared in this runtime")
    body = _body(unique_name("default-on"))
    del body["enabled"]
    response = created(body)
    assert_status_code(response, 201)
    trigger = response.json()
    assert trigger["enabled"] is True

    off = api.put(_triggers_url(base_url, trigger["id"]), json={**body, "enabled": False}, timeout=15)
    assert_status_code(off, 200)
    assert off.json()["enabled"] is False

    renamed = api.put(_triggers_url(base_url, trigger["id"]), json={**body, "name": unique_name("renamed")}, timeout=15)
    assert_status_code(renamed, 200)
    assert renamed.json()["enabled"] is False, "an update that leaves enabled out keeps it"
