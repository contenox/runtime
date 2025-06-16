import requests
import openai
import pytest
from helpers import assert_status_code
from openai import AuthenticationError, BadRequestError


def test_chat_completion_uses_assigned_model(
    base_url,
    admin_session,
    with_ollama_backend,
    create_model_and_assign_to_pool,
    create_backend_and_assign_to_pool,
    wait_for_model_in_backend
):
    """
    Test that the chat completion endpoint uses the assigned model after it's ready.
    """
    model_info = create_model_and_assign_to_pool
    backend_info = create_backend_and_assign_to_pool
    model_name = model_info["model_name"]
    backend_id = backend_info["backend_id"]

    # Wait until model is downloaded and visible in backend
    _ = wait_for_model_in_backend(model_name=model_name, backend_id=backend_id)

    # Now send a chat request using the model name
    payload = {
        "model": model_name,
        "messages": [{"role": "user", "content": "Echo 'hello'"}],
    }
    response = requests.post(f"{base_url}/v1/chat/completions", json=payload, headers=admin_session)
    assert response.status_code == 200, f"Chat failed: {response.text}"

    data = response.json()
    assert "choices" in data, "Missing choices"
    assert len(data["choices"]) > 0, "No choice returned"
    assert "content" in data["choices"][0]["message"], "No content in response"
    assert "hello" in data["choices"][0]["message"]["content"].lower(), "Unexpected response"

def test_openai_chat_missing_messages_fails(base_url, admin_session, create_model_and_assign_to_pool):
    """Test that missing messages causes error."""
    headers = admin_session
    model_name = create_model_and_assign_to_pool["model_name"]

    payload = {
        "model": model_name,
    }

    response = requests.post(f"{base_url}/v1/chat/completions", json=payload, headers=headers)
    assert_status_code(response, 422)


def test_openai_chat_unauthorized_user_fails(base_url, generate_email, register_user, create_model_and_assign_to_pool):
    """Test that unauthorized users cannot use the chat endpoint."""
    model_name = create_model_and_assign_to_pool["model_name"]
    email = generate_email("unauthorized")
    password = "unauthorizedpassword"
    user_data = register_user(email, "Unauthorized User", password)
    headers = {"Authorization": f"Bearer {user_data['token']}"}

    payload = {
        "model": model_name,
        "messages": [{"role": "user", "content": "Hello"}],
    }

    response = requests.post(f"{base_url}/v1/chat/completions", json=payload, headers=headers)
    assert_status_code(response, 401)


def test_unauthorized_sdk_access_fails(
    base_url,
    create_model_and_assign_to_pool,
    admin_session
):
    """
    Test that the OpenAI client fails with AuthenticationError or BadRequestError
    when using an invalid or missing API token.
    """
    model_name = create_model_and_assign_to_pool["model_name"]

    # Create a client with an invalid API key
    client = openai.OpenAI(
        base_url=f"{base_url}/v1/",
        api_key="invalid-or-missing-token"
    )

    with pytest.raises((AuthenticationError, BadRequestError)) as exc_info:
        _ = client.chat.completions.create(
            model=model_name,
            messages=[{"role": "user", "content": "Hello"}]
        )

    exc_value = exc_info.value

    if isinstance(exc_value, AuthenticationError):
        assert "authentication_error" in str(exc_value).lower(), \
            f"Expected 'authentication_error' in {exc_value}"
    elif isinstance(exc_value, BadRequestError):
        error_msg = str(exc_value).lower()
        assert any(keyword in error_msg for keyword in [
            "token is malformed",
            "token contains an invalid number of segments",
            "invalid token claims"
        ]), f"Unexpected error message: {error_msg}"
    else:
        pytest.fail(f"Unexpected exception type: {type(exc_value)}")
