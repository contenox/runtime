import requests
from helpers import assert_status_code


def test_configure_openai_provider(base_url, admin_session):
    """Test that an admin user can configure OpenAI provider."""
    url = f"{base_url}/providers/openai/configure"
    payload = {
        "apiKey": "sk-test-openai-key",
        "modelName": "gpt-3.5-turbo"
    }

    response = requests.post(url, json=payload, headers=admin_session)
    assert_status_code(response, 200)

    data = response.json()
    assert data["configured"] == True, "Provider was not configured"


def test_get_gemini_status_unconfigured(base_url, admin_session):
    """Test that Gemini is unconfigured by default."""
    status_url = f"{base_url}/providers/gemini/status"
    response = requests.get(status_url, headers=admin_session)
    assert_status_code(response, 200)

    data = response.json()
    assert data["configured"] is False, "Gemini should NOT be configured"
    assert data["provider"] == "gemini"


def test_configure_gemini_provider(base_url, admin_session):
    """Test that an admin user can configure Gemini provider."""
    url = f"{base_url}/providers/gemini/configure"
    payload = {
        "apiKey": "gemini-test-key",
        "modelName": "gemini-pro"
    }

    response = requests.post(url, json=payload, headers=admin_session)
    assert_status_code(response, 200)

    data = response.json()
    assert data["configured"] == True, "Provider was not configured"


def test_missing_api_key_fails(base_url, admin_session):
    """Test that missing API key returns 400."""
    url = f"{base_url}/providers/openai/configure"
    payload = {"modelName": "gpt-3.5-turbo"}  # Missing apiKey

    response = requests.post(url, json=payload, headers=admin_session)
    assert_status_code(response, 422)


def test_get_openai_status_configured(base_url, admin_session):
    """Test that after configuration, OpenAI status is 'configured'."""
    # First configure it
    configure_url = f"{base_url}/providers/openai/configure"
    payload = {"apiKey": "sk-test-openai-key"}
    requests.post(configure_url, json=payload, headers=admin_session)

    # Now check status
    status_url = f"{base_url}/providers/openai/status"
    response = requests.get(status_url, headers=admin_session)
    assert_status_code(response, 200)

    data = response.json()
    assert data["configured"] is True, "OpenAI should be configured"
    assert data["provider"] == "openai"


def test_configure_and_check_status_roundtrip(base_url, admin_session):
    """Configure both providers and verify their statuses."""
    # Configure both
    requests.post(f"{base_url}/providers/openai/configure",
                  json={"apiKey": "sk-test-openai-key"}, headers=admin_session)
    requests.post(f"{base_url}/providers/gemini/configure",
                  json={"apiKey": "gemini-test-key"}, headers=admin_session)

    # Check OpenAI
    openai_status = requests.get(f"{base_url}/providers/openai/status", headers=admin_session)
    assert_status_code(openai_status, 200)
    assert openai_status.json()["configured"] is True

    # Check Gemini
    gemini_status = requests.get(f"{base_url}/providers/gemini/status", headers=admin_session)
    assert_status_code(gemini_status, 200)
    assert gemini_status.json()["configured"] is True


def test_configure_unauthorized(base_url, generate_email, register_user):
    """Test that a non-admin user cannot configure providers."""
    email = generate_email("unauthorized")
    password = "unauthorizedpassword"
    user_data = register_user(email, "Unauthorized User", password)
    headers = {"Authorization": f"Bearer {user_data['token']}"}

    url = f"{base_url}/providers/openai/configure"
    payload = {"apiKey": "bad-api-key"}

    response = requests.post(url, json=payload, headers=headers)
    assert_status_code(response, 401)


def test_status_unauthorized(base_url, generate_email, register_user):
    """Test that a non-admin user cannot check provider status."""
    email = generate_email("unauthorized")
    password = "unauthorizedpassword"
    user_data = register_user(email, "Unauthorized User", password)
    headers = {"Authorization": f"Bearer {user_data['token']}"}

    url = f"{base_url}/providers/openai/status"

    response = requests.get(url, headers=headers)
    assert_status_code(response, 401)
