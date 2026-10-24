
import contextlib

from helpers import api_url, assert_status_code, assert_status_in, unique_name


def _valid_mission():
    return {
        "intent": unique_name("apitest mission"),
        "agentName": unique_name("apitest-agent"),
        "hitlPolicyName": "hitl-policy-default.json",
    }


def _create_mission(api, base_url, overrides=None):
    payload = _valid_mission()
    if overrides:
        payload.update(overrides)
    response = api.post(api_url(base_url, "missions"), json=payload, timeout=15)
    assert_status_code(response, 201)
    return response.json()


def _delete_mission(api, base_url, mission_id):
    with contextlib.suppress(Exception):
        api.delete(api_url(base_url, "missions", mission_id), timeout=15)


def test_missions_list_shape(api, base_url):
    response = api.get(api_url(base_url, "missions"), timeout=15)
    assert_status_code(response, 200)
    assert isinstance(response.json(), list)


def test_mission_create_requires_envelope(api, base_url):
    payload = _valid_mission()
    del payload["hitlPolicyName"]
    response = api.post(api_url(base_url, "missions"), json=payload, timeout=15)
    assert_status_code(response, 422)


def test_mission_create_requires_intent(api, base_url):
    payload = _valid_mission()
    del payload["intent"]
    response = api.post(api_url(base_url, "missions"), json=payload, timeout=15)
    assert_status_code(response, 422)


def test_mission_create_rejects_multiline_intent(api, base_url):
    payload = _valid_mission()
    payload["intent"] = "line one\nline two"
    response = api.post(api_url(base_url, "missions"), json=payload, timeout=15)
    assert_status_code(response, 422)


def test_mission_full_crud(api, base_url):
    created = _create_mission(api, base_url)
    mission_id = created["id"]
    try:
        assert created["status"] == "open", "a fresh mission is forced to status open"
        assert created["hitlPolicyName"] == "hitl-policy-default.json"

        got = api.get(api_url(base_url, "missions", mission_id), timeout=15)
        assert_status_code(got, 200)
        assert got.json()["id"] == mission_id
        assert got.json()["intent"] == created["intent"]

        new_intent = unique_name("patched intent")
        patched = api.patch(
            api_url(base_url, "missions", mission_id),
            json={"intent": new_intent, "status": "landed"},
            timeout=15,
        )
        assert_status_code(patched, 200)
        assert patched.json()["intent"] == new_intent
        assert patched.json()["status"] == "landed"

        deleted = api.delete(api_url(base_url, "missions", mission_id), timeout=15)
        assert_status_code(deleted, 200)

        gone = api.get(api_url(base_url, "missions", mission_id), timeout=15)
        assert_status_code(gone, 404)
    finally:
        _delete_mission(api, base_url, mission_id)


def test_mission_get_unknown_is_404(api, base_url):
    response = api.get(
        api_url(base_url, "missions", "no-such-mission-apitest"), timeout=15
    )
    assert_status_code(response, 404)


def test_mission_reports_roundtrip(api, base_url):
    created = _create_mission(api, base_url)
    mission_id = created["id"]
    try:
        empty = api.get(api_url(base_url, "missions", mission_id, "reports"), timeout=15)
        assert_status_code(empty, 200)
        assert empty.json() == []

        summary = unique_name("made progress")
        filed = api.post(
            api_url(base_url, "missions", mission_id, "reports"),
            json={
                "kind": "progress",
                "summary": summary,
                "detail": "a longer detail body",
                "refs": ["/workspace/out.txt"],
            },
            timeout=15,
        )
        assert_status_code(filed, 201)
        report = filed.json()
        assert report["kind"] == "progress"
        assert report["summary"] == summary
        assert report["missionId"] == mission_id

        listed = api.get(
            api_url(base_url, "missions", mission_id, "reports"), timeout=15
        )
        assert_status_code(listed, 200)
        reports = listed.json()
        assert isinstance(reports, list)
        assert any(r["summary"] == summary for r in reports)
    finally:
        _delete_mission(api, base_url, mission_id)


def test_mission_report_rejects_invalid_kind(api, base_url):
    created = _create_mission(api, base_url)
    mission_id = created["id"]
    try:
        response = api.post(
            api_url(base_url, "missions", mission_id, "reports"),
            json={"kind": "not-a-kind", "summary": "x"},
            timeout=15,
        )
        assert_status_code(response, 422)
    finally:
        _delete_mission(api, base_url, mission_id)


def test_mission_report_unknown_mission_is_404(api, base_url):
    response = api.post(
        api_url(base_url, "missions", "no-such-mission-apitest", "reports"),
        json={"kind": "progress", "summary": "x"},
        timeout=15,
    )
    assert_status_in(response, (404, 422))
