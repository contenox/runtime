
import contextlib

import pytest

from helpers import api_url, assert_status_code, assert_status_in


FIXTURE_AGENT = "agent-apitest-noop"
VALID_INSTANCE_STATES = {"starting", "running", "stopped", "error", "warning"}


def _fixture_agent_present(api, base_url) -> bool:
    response = api.get(api_url(base_url, "agents", "by-name", FIXTURE_AGENT), timeout=15)
    return response.status_code == 200


def _stop_instance(api, base_url, instance_id):
    with contextlib.suppress(Exception):
        api.delete(api_url(base_url, "fleet", instance_id), timeout=15)


def _delete_mission(api, base_url, mission_id):
    with contextlib.suppress(Exception):
        api.delete(api_url(base_url, "missions", mission_id), timeout=15)


def test_fleet_list_shape(api, base_url):
    response = api.get(api_url(base_url, "fleet"), timeout=15)
    assert_status_code(response, 200)
    entries = response.json()
    assert isinstance(entries, list)
    for entry in entries:
        assert isinstance(entry["agentId"], str) and entry["agentId"]
        assert isinstance(entry["agentName"], str) and entry["agentName"]
        assert isinstance(entry["kind"], str) and entry["kind"]
        assert entry["instances"] is None or isinstance(entry["instances"], list)


def test_fleet_get_unknown_instance_is_404(api, base_url):
    response = api.get(
        api_url(base_url, "fleet", "no-such-instance-apitest"), timeout=15
    )
    assert_status_code(response, 404)


def test_dispatch_missing_agent_name_is_400(api, base_url):
    response = api.post(
        api_url(base_url, "fleet", "dispatch"),
        json={"intent": "do the thing", "hitlPolicyName": "hitl-policy-default.json"},
        timeout=15,
    )
    assert_status_code(response, 400)


def test_dispatch_missing_intent_is_400(api, base_url):
    response = api.post(
        api_url(base_url, "fleet", "dispatch"),
        json={"agentName": FIXTURE_AGENT, "hitlPolicyName": "hitl-policy-default.json"},
        timeout=15,
    )
    assert_status_code(response, 400)


def test_dispatch_missing_policy_is_400(api, base_url):
    response = api.post(
        api_url(base_url, "fleet", "dispatch"),
        json={"agentName": FIXTURE_AGENT, "intent": "do the thing"},
        timeout=15,
    )
    assert_status_code(response, 400)


def test_dispatch_nonexistent_policy_is_400(api, base_url):
    response = api.post(
        api_url(base_url, "fleet", "dispatch"),
        json={
            "agentName": FIXTURE_AGENT,
            "intent": "do the thing",
            "hitlPolicyName": "no-such-policy-apitest.json",
        },
        timeout=15,
    )
    assert_status_code(response, 400)
    body = response.json()
    assert body.get("error", {}).get("param") == "hitlPolicyName", body


def test_dispatch_unknown_agent_is_404(api, base_url):
    response = api.post(
        api_url(base_url, "fleet", "dispatch"),
        json={
            "agentName": "no-such-agent-apitest",
            "intent": "do the thing",
            "hitlPolicyName": "hitl-policy-default.json",
        },
        timeout=15,
    )
    assert_status_code(response, 404)


def test_dispatch_lifecycle_running_then_stopped(api, base_url):
    if not _fixture_agent_present(api, base_url):
        pytest.skip(
            f"{FIXTURE_AGENT} not discovered — chain-agent discovery and the `chain` "
            "agent kind are a sibling slice not present in this base; the harness "
            "already seeds the fixture for when it lands"
        )

    dispatched = api.post(
        api_url(base_url, "fleet", "dispatch"),
        json={
            "agentName": FIXTURE_AGENT,
            "intent": "do the apitest thing",
            "hitlPolicyName": "hitl-policy-default.json",
        },
        timeout=30,
    )
    assert_status_code(dispatched, 202)
    result = dispatched.json()
    instance_id = result["instanceId"]
    session_id = result["sessionId"]
    mission_id = result["missionId"]
    assert instance_id and session_id and mission_id

    try:
        status = api.get(api_url(base_url, "fleet", instance_id), timeout=15)
        assert_status_code(status, 200)
        inst = status.json()
        assert inst["id"] == instance_id
        assert inst["kind"] == "chain"
        assert inst["state"] in VALID_INSTANCE_STATES

        board = api.get(api_url(base_url, "fleet"), timeout=15).json()
        instance_ids = {
            i["id"]
            for entry in board
            for i in (entry["instances"] or [])
        }
        assert instance_id in instance_ids

        mission = api.get(api_url(base_url, "missions", mission_id), timeout=15)
        assert_status_code(mission, 200)
        assert mission.json()["instanceId"] == instance_id
        assert mission.json()["agentName"] == FIXTURE_AGENT

        stopped = api.delete(api_url(base_url, "fleet", instance_id), timeout=15)
        assert_status_code(stopped, 200)

        again = api.delete(api_url(base_url, "fleet", instance_id), timeout=15)
        assert_status_in(again, (200, 404))
    finally:
        _stop_instance(api, base_url, instance_id)
        _delete_mission(api, base_url, mission_id)


def test_cancel_unknown_instance_is_404(api, base_url):
    response = api.post(
        api_url(base_url, "fleet", "no-such-instance-apitest", "cancel"),
        timeout=15,
    )
    assert_status_code(response, 404)
