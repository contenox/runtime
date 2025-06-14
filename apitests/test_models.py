import requests
from helpers import assert_status_code


def test_create_model(base_url, admin_session):
    """Test that an admin user can create a model."""
    headers = admin_session

    payload = {
        "model": "test-model",
        "name": "Test Model"
    }

    response = requests.post(f"{base_url}/models", json=payload, headers=headers)
    assert_status_code(response, 201)

    model = response.json()
    assert model["model"] == payload["model"], "Model name does not match"
    assert "id" in model, "Missing model ID"

    # Clean up
    model_id = model["model"]
    delete_url = f"{base_url}/models/{model_id}"
    del_response = requests.delete(delete_url, headers=headers, params={"purge": "true"})
    assert_status_code(del_response, 200)


def test_list_models(base_url, admin_session):
    """Test that an admin user can list all models."""
    headers = admin_session

    response = requests.get(f"{base_url}/models", headers=headers)
    assert_status_code(response, 200)

    data = response.json()
    assert data["object"] == "list", "Expected list object"
    assert isinstance(data["data"], list), "Data should be a list"


def test_delete_model(base_url, admin_session):
    """Test that an admin user can delete a model."""
    headers = admin_session

    # Step 1: Create a model
    payload = {"model": "temp-delete-model"}
    response = requests.post(f"{base_url}/models", json=payload, headers=headers)
    assert_status_code(response, 201)
    model = response.json()

    # Step 2: Delete it
    model_id = model["model"]
    delete_url = f"{base_url}/models/{model_id}"
    del_response = requests.delete(delete_url, headers=headers, params={"purge": "true"})
    assert_status_code(del_response, 200)

    # Optional: Verify it's gone
    get_response = requests.get(f"{base_url}/models", headers=headers)
    models = get_response.json()["data"]
    assert not any(m["id"] == model_id for m in models), "Model was not deleted"


def test_delete_immutable_model_fails(base_url, admin_session):
    """Test that deletion of the immutable embed model fails."""
    headers = admin_session
    immutable_model_name = "nomic-embed-text:latest"  # This depends on config

    delete_url = f"{base_url}/models/{immutable_model_name}"
    response = requests.delete(delete_url, headers=headers, params={"purge": "true"})
    assert_status_code(response, 403)


def test_list_models_unauthorized(base_url, generate_email, register_user):
    """Test that a random user gets a 401 when listing models."""
    email = generate_email("unauthorized")
    password = "unauthorizedpassword"
    user_data = register_user(email, "Unauthorized User", password)
    headers = {"Authorization": f"Bearer {user_data['token']}"}

    response = requests.get(f"{base_url}/models", headers=headers)
    assert_status_code(response, 401)


def test_create_model_unauthorized(base_url, generate_email, register_user):
    """Test that a random user cannot create a model."""
    email = generate_email("unauthorized")
    password = "unauthorizedpassword"
    user_data = register_user(email, "Unauthorized User", password)
    headers = {"Authorization": f"Bearer {user_data['token']}"}

    payload = {"model": "bad-model"}
    response = requests.post(f"{base_url}/models", json=payload, headers=headers)
    assert_status_code(response, 401)


def test_delete_model_unauthorized(base_url, admin_session, generate_email, register_user):
    """Test that a random user cannot delete a model."""
    # First create a model as admin
    headers = admin_session
    payload = {"model": "to-be-deleted"}
    response = requests.post(f"{base_url}/models", json=payload, headers=headers)
    assert_status_code(response, 201)
    model = response.json()
    model_id = model["model"]

    # Try to delete as unauthorized user
    email = generate_email("unauthorized")
    password = "unauthorizedpassword"
    user_data = register_user(email, "Unauthorized User", password)
    headers = {"Authorization": f"Bearer {user_data['token']}"}

    delete_url = f"{base_url}/models/{model_id}"
    response = requests.delete(delete_url, headers=headers, params={"purge": "true"})
    assert_status_code(response, 401)

    # Clean up as admin
    headers = admin_session
    del_response = requests.delete(delete_url, headers=headers, params={"purge": "true"})
    assert_status_code(del_response, 200)

def test_model_assigned_to_pool(base_url, admin_session, create_model_and_assign_to_pool):
    """Test that the model was successfully assigned to the pool."""
    data = create_model_and_assign_to_pool
    model_id = data["model_id"]
    pool_id = data["pool_id"]

    # Verify assignment by listing models in the pool
    list_url = f"{base_url}/model-associations/{pool_id}/models"
    response = requests.get(list_url, headers=admin_session)
    assert response.status_code == 200
    models_in_pool = response.json()

    assert any(m['id'] == model_id for m in models_in_pool), "Model not found in pool"

def trigger_model_pull(base_url, admin_session, model_name, with_ollama_backend):
    """Trigger model pull via an internal endpoint or logic."""
    payload = {"model": model_name}
    response = requests.post(f"{base_url}/models/pull", json=payload, headers=admin_session)
    assert response.status_code == 202, f"Failed to trigger model pull: {response.text}"
