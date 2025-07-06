import requests
from helpers import assert_status_code

def test_create_chain_success(base_url, admin_session):
    """Admin can create valid chain"""
    payload = {
        "id": "test-chain",
        "tasks": [
            {
                "id": "task1",
                "type": "llm",
                "prompt": "Test prompt",
                "model": "gpt-3.5-turbo"
            }
        ]
    }

    response = requests.post(f"{base_url}/chains", json=payload, headers=admin_session)
    assert_status_code(response, 201)
    assert response.json()["id"] == payload["id"]

    # Cleanup
    requests.delete(f"{base_url}/chains/test-chain", headers=admin_session)

def test_create_chain_missing_id(base_url, admin_session):
    """Reject chain without ID"""
    payload = {"tasks": [{"id": "task1", "type": "llm"}]}
    response = requests.post(f"{base_url}/chains", json=payload, headers=admin_session)
    assert_status_code(response, 400)

def test_create_chain_no_tasks(base_url, admin_session):
    """Reject chain with empty tasks"""
    payload = {"id": "empty-chain", "tasks": []}
    response = requests.post(f"{base_url}/chains", json=payload, headers=admin_session)
    assert_status_code(response, 400)

def test_create_chain_unauthorized(base_url, generate_email, register_user):
    """Non-admin cannot create chains"""
    payload = {"id": "user-chain", "tasks": [{"id": "task1"}]}
    email = generate_email("unauthorized")
    password = "unauthorizedpassword"
    user_data = register_user(email, "Unauthorized User", password)
    headers = {"Authorization": f"Bearer {user_data['token']}"}
    response = requests.post(f"{base_url}/chains", json=payload, headers=headers)
    assert_status_code(response, 401)

def test_get_chain_success(base_url, admin_session, create_test_chain):
    """Retrieve created chain by ID"""
    chain_id = create_test_chain["id"]
    response = requests.get(f"{base_url}/chains/{chain_id}", headers=admin_session)

    assert_status_code(response, 200)
    assert response.json()["id"] == chain_id

def test_get_chain_not_found(base_url, admin_session):
    """Handle missing chain gracefully"""
    response = requests.get(f"{base_url}/chains/invalid_id", headers=admin_session)
    assert_status_code(response, 404)

def test_get_chain_unauthorized(base_url, create_test_chain, generate_email, register_user):
    """Non-admin cannot access chains"""
    chain_id = create_test_chain["id"]
    email = generate_email("unauthorized")
    password = "unauthorizedpassword"
    user_data = register_user(email, "Unauthorized User", password)
    headers = {"Authorization": f"Bearer {user_data['token']}"}
    response = requests.get(f"{base_url}/chains/{chain_id}", headers=headers)
    assert_status_code(response, 401)

def test_list_chains_success(base_url, admin_session, create_test_chain):
    """List returns created chains"""
    response = requests.get(f"{base_url}/chains", headers=admin_session)

    assert_status_code(response, 200)
    assert isinstance(response.json(), list)
    assert any(chain["id"] == create_test_chain["id"] for chain in response.json())

def test_list_empty_chains(base_url, admin_session):
    """Handle empty chain list"""
    response = requests.get(f"{base_url}/chains", headers=admin_session)
    assert_status_code(response, 200)
    assert len(response.json()) == 2 # standard chains must be present

def test_list_chains_unauthorized(base_url, register_user, generate_email):
    """Non-admin cannot list chains"""
    email = generate_email("unauthorized")
    password = "unauthorizedpassword"
    user_data = register_user(email, "Unauthorized User", password)
    headers = {"Authorization": f"Bearer {user_data['token']}"}
    response = requests.get(f"{base_url}/chains", headers=headers)
    assert_status_code(response, 401)

def test_delete_chain_success(base_url, admin_session, create_test_chain):
    """Admin can delete chains"""
    chain_id = create_test_chain["id"]
    response = requests.delete(f"{base_url}/chains/{chain_id}", headers=admin_session)

    assert_status_code(response, 200)
    assert response.json()["status"] == "deleted"

    # Verify deletion
    get_response = requests.get(f"{base_url}/chains/{chain_id}", headers=admin_session)
    assert_status_code(get_response, 404)

def test_delete_missing_chain(base_url, admin_session):
    """Deleting non-existent chain fails"""
    response = requests.delete(f"{base_url}/chains/invalid_id", headers=admin_session)
    assert_status_code(response, 404)

def test_delete_chain_unauthorized(base_url, create_test_chain, register_user, generate_email):
    """Non-admin cannot delete chains"""
    password = "unauthorizedpassword"
    email = generate_email("unauthorized")
    user_data = register_user(email, "Unauthorized User", password)
    headers = {"Authorization": f"Bearer {user_data['token']}"}
    chain_id = create_test_chain["id"]
    response = requests.delete(f"{base_url}/chains/{chain_id}", headers=headers)
    assert_status_code(response, 401)
