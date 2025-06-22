import requests
from helpers import assert_status_code

def test_create_backend(base_url, admin_session, request):
    """Test that an admin user can create a backend and ensure cleanup afterward."""
    headers = admin_session
    ollama_url = "http://bad-test:11434"

    payload = {
        "name": "Test backend",
        "baseUrl": ollama_url,
        "type": "ollama",
    }

    # Step 1: Create backend
    response = requests.post(f"{base_url}/backends", json=payload, headers=headers)
    assert_status_code(response, 201)
    backend = response.json()
    backend_id = backend["id"]
    delete_url = f"{base_url}/backends/{backend_id}"
    del_response = requests.delete(delete_url, headers=headers)
    assert_status_code(del_response, 200)

def test_backend_assigned_to_pool(base_url, admin_session, create_backend_and_assign_to_pool):
    """Test that the backend was successfully assigned to the pool."""
    headers = admin_session
    data = create_backend_and_assign_to_pool
    backend_id = data["backend_id"]
    pool_id = data["pool_id"]

    # Verify assignment by fetching backends under the pool
    list_url = f"{base_url}/backend-associations/{pool_id}/backends"
    response = requests.get(list_url, headers=headers)
    assert_status_code(response, 200)
    backends = response.json()
    assert any(b['id'] == backend_id for b in backends), "Backend not found in pool"

def test_list_backends(base_url, admin_session):
    """Test that an admin user can list backends."""
    headers = admin_session
    response = requests.get(f"{base_url}/backends", headers=headers)
    assert_status_code(response, 200)
    backends = response.json()
    assert isinstance(backends, list)

def test_list_backends_unauthorized(base_url, generate_email, register_user):
    """Test that a random user gets a 401 when listing backends."""
    email = generate_email("unauthorized")
    password = "unauthorizedpassword"
    user_data = register_user(email, "Unauthorized User", password)
    headers = {"Authorization": f"Bearer {user_data['token']}"}
    response = requests.get(f"{base_url}/backends", headers=headers)
    assert_status_code(response, 401)
