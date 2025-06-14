import requests
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
        "messages": [{"role": "user", "content": "Say 'hello'"}],
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
