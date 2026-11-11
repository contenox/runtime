import hashlib
import os

import pytest

from helpers import api_url, assert_status_code, unique_name


def _sha(text):
    return hashlib.sha256(text.encode("utf8")).hexdigest()


def _default_root(api, base_url):
    response = api.get(api_url(base_url, "workspace/roots"), timeout=15)
    assert_status_code(response, 200)
    return next(r["path"] for r in response.json()["roots"] if r["default"])


def _write_disk(root, rel, body):
    path = os.path.join(root, *rel.split("/"))
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w", encoding="utf8", newline="") as fh:
        fh.write(body)
    return path


def _read_disk(root, rel):
    with open(os.path.join(root, *rel.split("/")), encoding="utf8", newline="") as fh:
        return fh.read()


def _read(api, base_url, rel):
    response = api.get(api_url(base_url, "files/content", path=rel), timeout=15)
    assert_status_code(response, 200)
    return response


def _save(api, base_url, rel, content, etag=None):
    headers = {"If-Match": f'"{etag}"'} if etag is not None else {}
    return api.put(api_url(base_url, "files"), json={"path": rel, "content": content}, headers=headers, timeout=15)


def _preview(api, base_url, **body):
    return api.post(api_url(base_url, "workspace/replace"), json=body, timeout=30)


def _apply(api, base_url, preview_id, paths=None):
    body = {"paths": paths} if paths else {}
    return api.post(api_url(base_url, "workspace/replace", preview_id, "apply"), json=body, timeout=30)


@pytest.fixture()
def folder(api, base_url):
    root = _default_root(api, base_url)
    name = unique_name("editor")
    os.makedirs(os.path.join(root, name))
    return root, name


def test_read_returns_the_content_hash_as_its_version(api, base_url, folder):
    root, name = folder
    _write_disk(root, f"{name}/main.go", "package main\n")
    response = _read(api, base_url, f"{name}/main.go")
    body = response.json()
    assert body["etag"] == _sha("package main\n")
    assert response.headers["ETag"] == f'"{_sha("package main\n")}"'


def test_two_editors_on_one_file_the_second_save_conflicts(api, base_url, folder):
    root, name = folder
    rel = f"{name}/shared.md"
    _write_disk(root, rel, "v1\n")
    first = _read(api, base_url, rel).json()["etag"]
    second = _read(api, base_url, rel).json()["etag"]
    assert first == second

    saved = _save(api, base_url, rel, "from editor one\n", first)
    assert_status_code(saved, 200)
    assert saved.json()["etag"] == _sha("from editor one\n")

    refused = _save(api, base_url, rel, "from editor two\n", second)
    assert_status_code(refused, 409)
    error = refused.json()["error"]
    assert error["code"] == "file_changed"
    assert error["currentEtag"] == _sha("from editor one\n")
    assert _read_disk(root, rel) == "from editor one\n"

    retried = _save(api, base_url, rel, "from editor two, merged\n", error["currentEtag"])
    assert_status_code(retried, 200)
    assert _read_disk(root, rel) == "from editor two, merged\n"


def test_a_change_on_disk_makes_an_open_buffer_stale(api, base_url, folder):
    root, name = folder
    rel = f"{name}/config.toml"
    _write_disk(root, rel, "a = 1\n")
    base = _read(api, base_url, rel).json()["etag"]
    _write_disk(root, rel, "a = 2\n")
    refused = _save(api, base_url, rel, "a = 3\n", base)
    assert_status_code(refused, 409)
    assert refused.json()["error"]["currentEtag"] == _sha("a = 2\n")


def test_accepted_saves_are_workspace_events(api, base_url, folder):
    root, name = folder
    rel = f"{name}/evented.txt"
    _write_disk(root, rel, "one\n")
    cursor = 0
    while True:
        page = api.get(api_url(base_url, "workspace/events", since=str(cursor)), timeout=15).json()
        if not page["events"]:
            break
        cursor = page["cursor"]
    base = _read(api, base_url, rel).json()["etag"]
    assert_status_code(_save(api, base_url, rel, "two\n", base), 200)
    assert_status_code(_save(api, base_url, rel, "three\n", base), 409)
    events = api.get(api_url(base_url, "workspace/events", since=str(cursor)), timeout=15).json()["events"]
    saves = [e for e in events if e["type"] == "editor.saved" and e["subject"] == rel]
    assert [e["data"]["etag"] for e in saves] == [_sha("two\n")]


def test_a_save_must_name_the_version_it_replaces(api, base_url, folder):
    root, name = folder
    rel = f"{name}/guarded.txt"
    _write_disk(root, rel, "on disk\n")

    blind = api.put(api_url(base_url, "files"), json={"path": rel, "content": "blind\n"}, timeout=15)
    assert_status_code(blind, 428)
    assert blind.json()["error"]["code"] == "precondition_required"
    assert _read_disk(root, rel) == "on disk\n"

    assert_status_code(_save(api, base_url, rel, "any version\n", "*"), 200)
    assert _read_disk(root, rel) == "any version\n"

    os.remove(os.path.join(root, *rel.split("/")))
    gone = _save(api, base_url, rel, "back\n", "*")
    assert_status_code(gone, 409)
    assert gone.json()["error"]["currentEtag"] == ""
    assert not os.path.exists(os.path.join(root, *rel.split("/")))


def test_replace_preview_is_exactly_what_apply_writes(api, base_url, folder):
    root, name = folder
    token = unique_name("Old").replace("-", "_")
    _write_disk(root, f"{name}/a.go", f"func {token}() {{}}\nvar x = {token}\n")
    _write_disk(root, f"{name}/pkg/b.go", f"{token}()\n")
    _write_disk(root, f"{name}/notes.md", f"{token}\n")

    preview = _preview(api, base_url, pattern=token, replacement="NewName", caseSensitive=True, include=["*.go"], path=name)
    assert_status_code(preview, 200)
    plan = preview.json()
    assert [f["path"] for f in plan["files"]] == [f"{name}/a.go", f"{name}/pkg/b.go"]
    assert plan["totalMatches"] == 3
    assert plan["files"][0]["hunks"] == [
        {"line": 1, "before": [f"func {token}() {{}}"], "after": ["func NewName() {}"]},
        {"line": 2, "before": [f"var x = {token}"], "after": ["var x = NewName"]},
    ]

    applied = _apply(api, base_url, plan["id"])
    assert_status_code(applied, 200)
    result = applied.json()
    assert result["conflicts"] == []
    assert sorted(a["path"] for a in result["applied"]) == [f"{name}/a.go", f"{name}/pkg/b.go"]
    for entry in plan["files"]:
        on_disk = _read_disk(root, entry["path"]).split("\n")
        for hunk in entry["hunks"]:
            start = hunk["line"] - 1
            assert on_disk[start:start + len(hunk["after"])] == hunk["after"]
    assert _read_disk(root, f"{name}/notes.md") == f"{token}\n"
    for applied_file in result["applied"]:
        assert applied_file["etag"] == _read(api, base_url, applied_file["path"]).json()["etag"]

    assert_status_code(_apply(api, base_url, plan["id"]), 404)


def test_replace_reports_a_file_that_changed_after_the_preview(api, base_url, folder):
    root, name = folder
    _write_disk(root, f"{name}/one.txt", "alpha\n")
    _write_disk(root, f"{name}/two.txt", "alpha\n")
    plan = _preview(api, base_url, pattern="alpha", replacement="beta", path=name).json()
    _write_disk(root, f"{name}/two.txt", "alpha, edited meanwhile\n")
    result = _apply(api, base_url, plan["id"]).json()
    assert [a["path"] for a in result["applied"]] == [f"{name}/one.txt"]
    assert result["conflicts"] == [{"path": f"{name}/two.txt", "currentEtag": _sha("alpha, edited meanwhile\n")}]
    assert _read_disk(root, f"{name}/two.txt") == "alpha, edited meanwhile\n"


def test_replace_never_reaches_the_control_plane_or_skipped_folders(api, base_url, folder):
    root, name = folder
    token = unique_name("secret").replace("-", "_")
    probe = _write_disk(root, f".contenox/{token}.txt", f"{token}\n")
    _write_disk(root, f"{name}/node_modules/pkg/index.js", f"{token}\n")
    _write_disk(root, f"{name}/visible.txt", f"{token}\n")
    try:
        plan = _preview(api, base_url, pattern=token, replacement="x").json()
        assert [f["path"] for f in plan["files"]] == [f"{name}/visible.txt"]
        assert_status_code(_preview(api, base_url, pattern=token, replacement="x", path=".contenox"), 422)
        assert_status_code(_preview(api, base_url, pattern=token, replacement="x", path="../"), 422)
    finally:
        os.remove(probe)


@pytest.mark.parametrize(
    "body",
    [
        {"pattern": ""},
        {"pattern": "(", "regex": True},
        {"pattern": "x", "include": ["["]},
        {"pattern": "x" * 600},
    ],
)
def test_replace_refuses_bad_queries(api, base_url, body):
    assert_status_code(_preview(api, base_url, **body), 422)


def test_a_move_never_replaces_another_file(api, base_url, folder):
    root, name = folder
    _write_disk(root, f"{name}/a.txt", "a\n")
    _write_disk(root, f"{name}/b.txt", "b\n")
    refused = api.put(api_url(base_url, "files/move"), json={"path": f"{name}/a.txt", "newPath": f"{name}/b.txt"}, timeout=15)
    assert refused.status_code == 409, refused.text
    assert _read_disk(root, f"{name}/b.txt") == "b\n"
    moved = api.put(api_url(base_url, "files/move"), json={"path": f"{name}/a.txt", "newPath": f"{name}/c.txt"}, timeout=15)
    assert_status_code(moved, 200)
    assert _read_disk(root, f"{name}/c.txt") == "a\n"
