import requests
import openai
import pytest
from helpers import assert_status_code

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


def test_openai_client_can_call_chat_completion(
    base_url,
    admin_session,
    with_ollama_backend,
    create_model_and_assign_to_pool,
    create_backend_and_assign_to_pool,
    wait_for_model_in_backend
):
    """
    Test that the OpenAI Python client can successfully call the chat completion endpoint.
    """
    model_info = create_model_and_assign_to_pool
    backend_info = create_backend_and_assign_to_pool
    model_name = model_info["model_name"]
    backend_id = backend_info["backend_id"]

    # Wait until model is downloaded and visible in backend
    _ = wait_for_model_in_backend(model_name=model_name, backend_id=backend_id)

    # Configure the OpenAI client to point to our local server
    client = openai.OpenAI(
        base_url=f"{base_url}/v1/",
        api_key=admin_session["Authorization"].replace("Bearer ", "")
    )

    try:
        response = client.chat.completions.create(
            model=model_name,
            messages=[
                {"role": "system", "content": "You are a bot echoing the user's message."},
                {"role": "user", "content": "Echo 'hello'"},
            ]
        )
    except openai.APIStatusError as e:
        assert False, f"OpenAI client request failed with status {e.status_code}: {e.message}"

    # Validate structure
    assert len(response.choices) > 0, "No choices returned"
    resp = response.choices[0].message.content
    assert resp
    content = resp.lower()
    assert content.find("hello") != -1, f"Response did not contain 'hello': {content}"

def test_unauthorized_sdk_access_fails(
    base_url,
    create_model_and_assign_to_pool,
    admin_session
):
    """
    Test that the OpenAI client fails with AuthenticationError when using an invalid token.
    """
    model_name = create_model_and_assign_to_pool["model_name"]

    # Use an incorrect or empty API key
    client = openai.OpenAI(
        base_url=f"{base_url}/v1/",
        api_key="invalid-or-missing-token"
    )

    from openai import AuthenticationError, BadRequestError

    with pytest.raises(AuthenticationError) or pytest.raises(BadRequestError) as exc_info:
        _ = client.chat.completions.create(
            model=model_name,
            messages=[
                {"role": "user", "content": "Hello"},
            ]
        )

    assert "authentication_error" in str(exc_info.value).lower()
