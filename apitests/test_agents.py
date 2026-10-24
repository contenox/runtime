
import pytest

from helpers import api_url, assert_status_code, assert_status_in


FIXTURE_AGENT = "agent-apitest-noop"


def _list_agents(api, base_url):
    response = api.get(api_url(base_url, "agents"), timeout=15)
    assert_status_code(response, 200)
    body = response.json()
    assert body is None or isinstance(body, list)
    return body or []


def test_agents_list_shape(api, base_url):
    agents = _list_agents(api, base_url)
    for agent in agents:
        assert isinstance(agent["id"], str) and agent["id"]
        assert isinstance(agent["name"], str) and agent["name"]
        assert isinstance(agent["kind"], str) and agent["kind"]
        assert isinstance(agent["enabled"], bool)
        assert "configJson" in agent


def test_agents_get_by_name_and_by_id_roundtrip(api, base_url):
    agents = _list_agents(api, base_url)
    if not agents:
        pytest.skip("no declared agents in this base — agent discovery is a sibling slice")
    first = agents[0]

    by_name = api.get(api_url(base_url, "agents", "by-name", first["name"]), timeout=15)
    assert_status_code(by_name, 200)
    assert by_name.json()["name"] == first["name"]
    assert by_name.json()["id"] == first["id"]

    by_id = api.get(api_url(base_url, "agents", first["id"]), timeout=15)
    assert_status_code(by_id, 200)
    assert by_id.json()["id"] == first["id"]
    assert by_id.json()["name"] == first["name"]


def test_agents_fixture_agent_is_discovered(api, base_url):
    response = api.get(api_url(base_url, "agents", "by-name", FIXTURE_AGENT), timeout=15)
    if response.status_code == 404:
        pytest.skip(
            f"{FIXTURE_AGENT} not discovered — chain-agent discovery is a sibling slice; "
            "the harness already seeds the fixture for when it lands"
        )
    assert_status_code(response, 200)
    body = response.json()
    assert body["name"] == FIXTURE_AGENT
    assert body["kind"] == "chain"
    assert body["enabled"] is True


def test_agents_get_by_name_unknown_is_404(api, base_url):
    response = api.get(
        api_url(base_url, "agents", "by-name", "no-such-agent-apitest"), timeout=15
    )
    assert_status_code(response, 404)


def test_agents_get_by_id_unknown_is_404(api, base_url):
    response = api.get(
        api_url(base_url, "agents", "00000000-0000-0000-0000-000000000000"), timeout=15
    )
    assert_status_code(response, 404)


def test_agents_registry_is_read_only(api, base_url):
    created = api.post(api_url(base_url, "agents"), json={"name": "x"}, timeout=15)
    assert_status_in(created, (404, 405))

    updated = api.put(
        api_url(base_url, "agents", "00000000-0000-0000-0000-000000000000"),
        json={"name": "x"},
        timeout=15,
    )
    assert_status_in(updated, (404, 405))

    deleted = api.delete(
        api_url(base_url, "agents", "00000000-0000-0000-0000-000000000000"), timeout=15
    )
    assert_status_in(deleted, (404, 405))
