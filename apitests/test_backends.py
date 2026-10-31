import os
import tempfile

from helpers import (
    api_url,
    assert_status_code,
    assert_status_in,
    unique_name,
)


def _backend_payload(name: str) -> dict:
    return {
        "name": name,
        "baseUrl": "http://127.0.0.1:11434",
        "type": "ollama",
    }


def test_backend_crud(api, base_url):
    name = unique_name("apitest-backend")
    response = api.post(api_url(base_url, "backends"), json=_backend_payload(name), timeout=15)
    assert_status_code(response, 201)
    backend = response.json()
    backend_id = backend["id"]

    try:
        response = api.get(api_url(base_url, "backends"), timeout=15)
        assert_status_code(response, 200)
        assert any(item["id"] == backend_id for item in response.json())

        response = api.get(api_url(base_url, "backends", backend_id), timeout=15)
        assert_status_code(response, 200)
        details = response.json()
        assert details["name"] == name
        assert details["type"] == "ollama"
        assert "models" in details
        assert "pulledModels" in details

        updated = _backend_payload(f"{name}-updated")
        response = api.put(api_url(base_url, "backends", backend_id), json=updated, timeout=15)
        assert_status_code(response, 200)
        assert response.json()["name"] == updated["name"]
    finally:
        response = api.delete(api_url(base_url, "backends", backend_id), timeout=15)
        assert_status_code(response, 200)


def test_backend_validation_rejects_missing_base_url(api, base_url):
    response = api.post(
        api_url(base_url, "backends"),
        json={"name": unique_name("apitest-backend"), "type": "ollama"},
        timeout=15,
    )
    assert response.status_code in (400, 422)


def test_backend_rejects_unknown_type(api, base_url):
    response = api.post(
        api_url(base_url, "backends"),
        json={
            "name": unique_name("apitest-backend"),
            "baseUrl": "http://127.0.0.1:11434",
            "type": "local",
        },
        timeout=15,
    )
    assert_status_code(response, 422)
    assert "Type must be" in response.text


def test_backend_rejects_duplicate_connection(api, base_url):
    payload = _backend_payload(unique_name("apitest-backend"))
    first = api.post(api_url(base_url, "backends"), json=payload, timeout=15)
    assert_status_code(first, 201)
    backend_id = first.json()["id"]

    try:
        duplicate = api.post(
            api_url(base_url, "backends"),
            json={**payload, "name": unique_name("apitest-backend")},
            timeout=15,
        )
        assert_status_code(duplicate, 409)
    finally:
        response = api.delete(api_url(base_url, "backends", backend_id), timeout=15)
        assert_status_code(response, 200)

