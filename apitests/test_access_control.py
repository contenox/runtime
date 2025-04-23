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
        "permission": "manage"
    }
    headers = admin_session
    response = requests.post(f"{base_url}/access-control", json=payload, headers=headers)
    assert_status_code(response, 201)

    list_response = requests.get(f"{base_url}/access-control?identity={user_data['user_id']}", headers=headers)
    assert_status_code(list_response, 200)
    entries = list_response.json()
    print(entries)
    found = any(entry.get("resource") == "server" and entry.get("permission") == 3 for entry in entries)
    assert found, "Access entry for managing 'server' was not found for the user."
