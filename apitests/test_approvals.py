
from helpers import api_url, assert_status_code


def test_approvals_list_is_empty_array_shape(api, base_url):
    response = api.get(api_url(base_url, "approvals"), timeout=15)
    assert_status_code(response, 200)
    body = response.json()
    assert isinstance(body, list)
    assert body == []


def test_approvals_list_honours_limit_param(api, base_url):
    response = api.get(api_url(base_url, "approvals", limit="10"), timeout=15)
    assert_status_code(response, 200)
    assert isinstance(response.json(), list)


def test_approvals_answer_unknown_id_is_404(api, base_url):
    response = api.post(
        api_url(base_url, "approvals", "no-such-approval-apitest"),
        json={"approved": True},
        timeout=15,
    )
    assert_status_code(response, 404)


def test_approvals_answer_deny_unknown_id_is_404(api, base_url):
    response = api.post(
        api_url(base_url, "approvals", "no-such-approval-apitest"),
        json={"approved": False},
        timeout=15,
    )
    assert_status_code(response, 404)
