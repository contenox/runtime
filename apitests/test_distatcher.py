import requests
from helpers import assert_status_code

def test_dispatch_list_jobs(base_url, admin_session, create_test_file):
    """Test that a file creation event creates a job."""
    headers = admin_session
    test_file = create_test_file()
    #timeNow = datetime.datetime.utcnow().isoformat()
    #
    response = requests.get(f"{base_url}/jobs/pending", headers=headers)

    assert_status_code(response, 200)
    assert len(response.json()) > 0
    assert response.json()[0]["id"] is not None

    assert response.json()[0]["entityId"] == test_file['id']
    cursor = response.json()[0]["createdAt"]
    empty_response = requests.get(f"{base_url}/jobs/pending?cursor={cursor}", headers=headers)
    assert_status_code(empty_response, 200)
    assert len(empty_response.json()) == 0
    payload = {
        "leaserId": "worker-a",
        "leaseDuration": "10s",
        # Correct the job type to include the charset
        "jobTypes": ["vectorize_text/plain; charset=utf-8"],
    }
    response = requests.post(f"{base_url}/leases", json=payload, headers=headers)
    assert_status_code(response, 201)

    assert response.json()["id"] is not None
    assert response.json()["leaser"] == "worker-a"
    assert response.json()["entityId"] == test_file['id']
