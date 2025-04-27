import requests
from helpers import assert_status_code
import datetime

def test_dispatch_job(base_url, admin_session, create_test_file):
    """Test that a file creation event creates a job."""
    headers = admin_session
    test_file = create_test_file()
    #timeNow = datetime.datetime.utcnow().isoformat()
    #response = requests.get(f"{base_url}/jobs/pending?cursor={timeNow}", headers=headers)
    response = requests.get(f"{base_url}/jobs/pending", headers=headers)

    assert_status_code(response, 200)
    print(response.json())
    assert response.json()["id"] is not None
    assert response.json()["entityId"] == test_file.id

# def test_dispatch_job(base_url, admin_session, create_test_file):
#     """Test that an file creation event creates a job."""
#     headers = admin_session
#     test_file = create_test_file()
#     payload = {
#         "leaserId": "worker-a",
#         "leaseDuration": "10s",
#         "jobTypes": ["vectorize_text/plain"],
#     }
#     response = requests.post(f"{base_url}/leases", json=payload, headers=headers)
#     assert_status_code(response, 200)
#     assert response.json()["id"] is not None
#     assert response.json()["leaser"] == "worker-a"
#     assert response.json()["entityId"] == test_file.id
