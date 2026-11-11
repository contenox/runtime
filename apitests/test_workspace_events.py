import json
import os
import shutil
import tempfile
import time

import pytest

from helpers import api_url, assert_status_code, unique_name


def _events_url(base_url, *parts, **query):
    return api_url(base_url, "workspace/events", *parts, **query)


def _post(api, base_url, body, **query):
    return api.post(_events_url(base_url, **query), json=body, timeout=15)


def _page(api, base_url, **query):
    response = api.get(_events_url(base_url, **query), timeout=15)
    assert_status_code(response, 200)
    body = response.json()
    assert isinstance(body["events"], list)
    assert isinstance(body["cursor"], int)
    return body


def _editor(events):
    return [e for e in events if e["source"] == "editor"]


def _cursor(api, base_url, **query):
    cursor = 0
    while True:
        page = _page(api, base_url, since=str(cursor), **query)
        if not page["events"]:
            return cursor
        cursor = page["cursor"]


@pytest.fixture()
def second_root(api, base_url):
    folder = tempfile.mkdtemp(prefix="apitest-events-")
    response = api.post(api_url(base_url, "workspace/roots"), json={"path": folder, "name": "apitest-events"}, timeout=15)
    assert_status_code(response, 200)
    granted = next(r["path"] for r in response.json()["roots"] if os.path.samefile(r["path"], folder))
    yield granted
    api.delete(api_url(base_url, "workspace/roots", path=granted), timeout=15)
    shutil.rmtree(folder, ignore_errors=True)


def test_editor_event_is_appended_and_replayed_by_cursor(api, base_url):
    start = _cursor(api, base_url)
    path = f"src/{unique_name('opened')}.go"

    opened = _post(api, base_url, {"type": "editor.opened", "subject": path})
    assert_status_code(opened, 201)
    first = opened.json()
    assert first["type"] == "editor.opened"
    assert first["source"] == "editor"
    assert first["subject"] == path
    assert first["nid"] > start
    assert first["data"] == {}

    saved = _post(api, base_url, {"type": "editor.saved", "subject": path, "data": {"etag": "abc123"}})
    assert_status_code(saved, 201)
    second = saved.json()
    assert second["nid"] > first["nid"]
    assert second["data"] == {"etag": "abc123"}

    page = _page(api, base_url, since=str(start))
    assert [e["nid"] for e in _editor(page["events"])] == [first["nid"], second["nid"]]
    assert page["cursor"] == page["events"][-1]["nid"]

    tail = _page(api, base_url, since=str(first["nid"]))
    assert [e["type"] for e in _editor(tail["events"])] == ["editor.saved"]

    limited = _page(api, base_url, since=str(first["nid"] - 1), limit="1")
    assert [e["nid"] for e in limited["events"]] == [first["nid"]]
    assert limited["cursor"] == first["nid"]

    drained = _page(api, base_url, since=str(second["nid"]))
    assert _editor(drained["events"]) == []
    assert drained["cursor"] >= second["nid"]


@pytest.mark.parametrize(
    "body",
    [
        {"type": "fs.modified", "subject": "a.txt"},
        {"type": "tool.wrote", "subject": "a.txt"},
        {"type": "approval.decided", "subject": "x"},
        {"type": "editor.exploded", "subject": "a.txt"},
        {"type": "editor.opened", "subject": "../../outside.txt"},
        {"type": "editor.opened"},
        {"type": "editor.saved", "subject": "a.txt", "data": [1, 2, 3]},
        {"type": "editor.saved", "subject": "a.txt", "data": {"blob": "x" * 5000}},
    ],
)
def test_events_the_editor_may_not_post_are_refused(api, base_url, body):
    start = _cursor(api, base_url)
    response = _post(api, base_url, body)
    assert_status_code(response, 422)
    assert _editor(_page(api, base_url, since=str(start))["events"]) == []


def test_oversized_body_is_refused(api, base_url):
    response = _post(api, base_url, {"type": "editor.saved", "subject": "a.txt", "data": {"blob": "x" * 40000}})
    assert response.status_code in (413, 422), response.text


def test_bad_cursor_and_unknown_root_are_refused(api, base_url):
    assert_status_code(api.get(_events_url(base_url, since="-1"), timeout=15), 422)
    assert_status_code(api.get(_events_url(base_url, since="abc"), timeout=15), 422)
    assert_status_code(api.get(_events_url(base_url, limit="0"), timeout=15), 422)
    outside = tempfile.mkdtemp(prefix="apitest-not-granted-")
    try:
        assert_status_code(api.get(_events_url(base_url, root=outside), timeout=15), 422)
        assert_status_code(_post(api, base_url, {"type": "editor.opened", "subject": "a.txt"}, root=outside), 422)
    finally:
        shutil.rmtree(outside, ignore_errors=True)


def test_each_workspace_root_has_its_own_stream(api, base_url, second_root):
    default_start = _cursor(api, base_url)
    assert _cursor(api, base_url, root=second_root) == 0

    in_second = _post(api, base_url, {"type": "editor.opened", "subject": "only-here.txt"}, root=second_root)
    assert_status_code(in_second, 201)
    in_default = _post(api, base_url, {"type": "editor.opened", "subject": "only-there.txt"})
    assert_status_code(in_default, 201)

    second_events = _editor(_page(api, base_url, root=second_root)["events"])
    assert [e["subject"] for e in second_events] == ["only-here.txt"]

    default_events = _editor(_page(api, base_url, since=str(default_start))["events"])
    assert [e["subject"] for e in default_events] == ["only-there.txt"]

    nested = os.path.join(second_root, "pkg")
    os.makedirs(nested, exist_ok=True)
    from_nested = _editor(_page(api, base_url, root=nested)["events"])
    assert [e["subject"] for e in from_nested] == ["only-here.txt"]


def test_selection_events_are_coalesced(api, base_url):
    start = _cursor(api, base_url)
    path = f"{unique_name('sel')}.txt"
    first = _post(api, base_url, {"type": "editor.selection", "subject": path, "data": {"startLine": 1, "endLine": 2}})
    assert_status_code(first, 201)
    again = _post(api, base_url, {"type": "editor.selection", "subject": path, "data": {"startLine": 3, "endLine": 4}})
    assert_status_code(again, 201)
    assert again.json()["nid"] == first.json()["nid"]
    assert len(_editor(_page(api, base_url, since=str(start))["events"])) == 1


def _connected(response):
    lines = response.iter_lines(decode_unicode=True)
    for raw in lines:
        if raw and raw.startswith(": connected"):
            return lines
    raise AssertionError("the stream ended before it announced itself")


def _read_events(lines, count, deadline, source="editor"):
    events = []
    for raw in lines:
        if time.time() > deadline:
            break
        if raw and raw.startswith("data: "):
            event = json.loads(raw[len("data: "):])
            if event["source"] == source:
                events.append(event)
            if len(events) >= count:
                break
    return events


def test_stream_replays_from_a_cursor_then_follows_live(api, base_url):
    start = _cursor(api, base_url)
    path = f"{unique_name('stream')}.txt"
    replayed = _post(api, base_url, {"type": "editor.opened", "subject": path})
    assert_status_code(replayed, 201)

    with api.get(_events_url(base_url, "stream", since=str(start)), stream=True, timeout=(5, 20)) as response:
        assert_status_code(response, 200)
        assert response.headers["Content-Type"].startswith("text/event-stream")
        lines = _connected(response)
        assert_status_code(_post(api, base_url, {"type": "editor.saved", "subject": path}), 201)
        events = _read_events(lines, 2, time.time() + 15)

    assert [e["type"] for e in events] == ["editor.opened", "editor.saved"]
    assert events[0]["nid"] == replayed.json()["nid"]
    assert events[1]["nid"] > events[0]["nid"]


def test_stream_without_a_cursor_starts_at_now_and_resumes_by_last_event_id(api, base_url):
    path = f"{unique_name('resume')}.txt"
    before = _post(api, base_url, {"type": "editor.opened", "subject": path})
    assert_status_code(before, 201)

    with api.get(_events_url(base_url, "stream"), stream=True, timeout=(5, 20)) as response:
        assert_status_code(response, 200)
        lines = _connected(response)
        assert_status_code(_post(api, base_url, {"type": "editor.closed", "subject": path}), 201)
        live = _read_events(lines, 1, time.time() + 15)
    assert [e["type"] for e in live] == ["editor.closed"]

    headers = {"Last-Event-ID": str(before.json()["nid"])}
    with api.get(_events_url(base_url, "stream"), stream=True, timeout=(5, 20), headers=headers) as response:
        assert_status_code(response, 200)
        resumed = _read_events(_connected(response), 1, time.time() + 15)
    assert [e["nid"] for e in resumed] == [live[0]["nid"]]
