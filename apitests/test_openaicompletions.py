import pytest
import requests
from openai import OpenAI
from helpers import assert_status_code
import uuid
import time

def test_openai_sdk_compatibility(
    base_url,
    wait_for_declared_models,
    create_model_and_assign_to_pool,
    create_backend_and_assign_to_pool,
    wait_for_model_in_backend
):
    """Verify basic compatibility with the official OpenAI Python SDK."""
    # Get model info and wait for download
    model_info = create_model_and_assign_to_pool
    backend_info = create_backend_and_assign_to_pool
    model_name = model_info["model_name"]
    backend_id = backend_info["backend_id"]

    wait_for_model_in_backend(
        model_name=model_name,
        backend_id=backend_id
    )

    # Create minimal task chain for OpenAI compatibility
    chain_id = f"openai-sdk-test-{str(uuid.uuid4())[:8]}"
    task_chain = {
        "id": chain_id,
        "debug": True,
        "description": "OpenAI SDK compatibility test chain",
        "token_limit": 4096,
        "tasks": [
            {
                "id": "main_task",
                "handler": "model_execution",
                "system_instruction":"You are a task processing engine talking to other machines. Return the direct answer without explanation to the given task.",
                "execute_config": {
                    "model": model_name,
                    "provider": "ollama"
                },
                "transition": {
                    "branches": [{"operator": "default", "goto": "format_response"}]
                }
            },
            {
                "id": "format_response",
                "handler": "convert_to_openai_chat_response",
                "transition": {
                    "branches": [{"operator": "default", "goto": "end"}]
                }
            }
        ]
    }

    # Create task chain
    response = requests.post(f"{base_url}/taskchains", json=task_chain)
    assert_status_code(response, 201)


    # Configure OpenAI client to point to our endpoint
    client = OpenAI(
        base_url=f"{base_url}/{chain_id}/v1",
        api_key="empty-key-for-now"
    )

    # Test basic chat completion
    response = client.chat.completions.create(
        model=model_name,
        messages=[{"role": "user", "content": "Hello"}]
    )

    # Verify response structure matches OpenAI format
    assert response.id is not None
    assert response.object == "chat.completion"
    assert response.created > 0
    assert response.model == model_name
    assert len(response.choices) > 0
    assert response.choices[0].message is not None
    assert response.choices[0].message.content is not None
    assert response.usage is not None
    assert response.usage.prompt_tokens > 0
    assert response.usage.completion_tokens > 0
    assert response.usage.total_tokens == response.usage.prompt_tokens + response.usage.completion_tokens

def test_openai_sdk_streaming_compatibility(
    base_url,
    create_model_and_assign_to_pool,
    create_backend_and_assign_to_pool,
    wait_for_model_in_backend
):
    """Verify SSE streaming compatibility with the official OpenAI Python SDK."""
    # --- Setup: Same as the non-streaming test ---
    model_info = create_model_and_assign_to_pool
    backend_info = create_backend_and_assign_to_pool
    model_name = model_info["model_name"]
    backend_id = backend_info["backend_id"]
    wait_for_model_in_backend(model_name=model_name, backend_id=backend_id)

    chain_id = f"openai-sdk-stream-test-{str(uuid.uuid4())[:8]}"
    task_chain = {
        "id": chain_id, "debug": True,
        "tasks": [
            {"id": "main_task", "handler": "model_execution", "transition": {"branches": [{"operator": "default", "goto": "format_response"}]}},
            {"id": "format_response", "handler": "convert_to_openai_chat_response", "transition": {"branches": [{"operator": "default", "goto": "end"}]}}
        ]
    }
    response = requests.post(f"{base_url}/taskchains", json=task_chain)
    assert_status_code(response, 201)

    client = OpenAI(
        base_url=f"{base_url}/{chain_id}/v1",
        api_key="a-key"
    )

    # --- Test: Call the API with stream=True ---
    stream = client.chat.completions.create(
        model=model_name,
        messages=[{"role": "user", "content": "What is the capital of Germany? Respond with only the city name."}],
        stream=True
    )

    # --- Verification ---
    full_response_content = ""
    chunk_count = 0
    for chunk in stream:
        chunk_count += 1
        assert chunk.id is not None
        assert chunk.object == "chat.completion.chunk"
        assert chunk.model == model_name

        if chunk.choices[0].delta.content is not None:
            full_response_content += chunk.choices[0].delta.content

    assert chunk_count > 1 # Ensure we received multiple chunks
    assert "Berlin" in full_response_content
