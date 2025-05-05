import requests
from helpers import assert_status_code

def test_assign_manage_permission(base_url, generate_email, register_user, admin_session):
    """
    Test that an admin can assign manage permission on the 'server' resource
    to a randomly registered user.
    """
    random_email = generate_email("access")
    password = "testpassword"
    user_data = register_user(random_email, "Test Access User", password)
    payload = {
        "identity": user_data['user_id'],
        "resource": "server",
        "resourceType": "system",
        "permission": "manage"
    }
    headers = admin_session
    response = requests.post(f"{base_url}/access-control", json=payload, headers=headers)
    assert_status_code(response, 201)

    list_response = requests.get(f"{base_url}/access-control?identity={user_data['user_id']}", headers=headers)
    assert_status_code(list_response, 200)
    entries = list_response.json()
    print(entries)
    found = any(entry.get("resource") == "server" and entry.get("permission") == "manage" and entry.get("resourceType") == "system" for entry in entries)
    assert found, "Access entry for managing 'server' was not found for the user."

def test_create_access_entry_invalid_file_resource(base_url, admin_session, generate_email, register_user):
    """Test creating an access entry with an invalid file resource returns an error."""
    email = generate_email()
    user_info = register_user(email, "Test User", "password")
    user_id = user_info['user_id']

    payload = {
        "identity": user_id,
        "resource": "invalid-file-id-123",
        "resourceType": "files",
        "permission": "view"
    }
    response = requests.post(f"{base_url}/access-control", json=payload, headers=admin_session)
    assert_status_code(response, 404)
