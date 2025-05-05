import requests
from helpers import assert_status_code
import time # Import time for sleep

def create_and_lease_job(base_url, headers, create_test_file, leaser_id="worker-test", duration="10s"):
    """Creates a test file and immediately leases the resulting job.

    Returns:
        tuple: (test_file_dict, leased_job_dict)
    """
    print("[Helper] Creating test file...")
    test_file = create_test_file()
    print(f"[Helper] Test file created: {test_file['id']}")

    pending_job = None
    job_type = None
    for i in range(5):
        print(f"[Helper] Attempt {i+1}: Listing pending jobs...")
        response = requests.get(f"{base_url}/jobs/pending", headers=headers)
        assert_status_code(response, 200)
        pending_jobs = response.json()
        print(f"[Helper] Found {len(pending_jobs)} pending jobs.")
        for job in pending_jobs:
            if job.get("entityId") == test_file['id']:
                pending_job = job
                job_type = job.get("taskType")
                print(f"[Helper] Found matching pending job: {pending_job['id']}")
                break # Found the job
        if pending_job:
            break # Exit retry loop
        time.sleep(0.2) # Wait before retrying

    if not pending_job:
        raise Exception(f"Job for file {test_file['id']} did not appear in pending queue.")
    if not job_type:
         raise Exception(f"Job {pending_job['id']} has no taskType.")

    print(f"[Helper] Leasing job {pending_job['id']} (type: {job_type}) for leaser {leaser_id}...")
    payload = {
        "leaserId": leaser_id,
        "leaseDuration": duration,
        "jobTypes": [job_type], # Use the actual job type found
    }
    lease_response = requests.post(f"{base_url}/leases", json=payload, headers=headers)
    assert_status_code(lease_response, 201) # Expect 201 Created for POST /leases

    leased_job_data = lease_response.json()
    print(f"[Helper] Job leased successfully: {leased_job_data.get('id')}")
    assert leased_job_data.get("id") == pending_job['id'], "Leased job ID should match pending job ID"
    assert leased_job_data.get("leaser") == leaser_id
    assert leased_job_data.get("entityId") == test_file['id']

    return test_file, leased_job_data

def test_dispatch_list_and_lease(base_url, admin_session, create_test_file):
    """Test listing pending jobs and leasing one."""
    headers = admin_session
    print("\n--- Running Test: List and Lease ---")
    print("Creating file...")
    test_file = create_test_file()

    print("Listing pending jobs...")
    response = requests.get(f"{base_url}/jobs/pending", headers=headers)
    assert_status_code(response, 200)
    pending_jobs = response.json()
    print(f"Found {len(pending_jobs)} pending jobs.")
    assert len(pending_jobs) > 0

    created_job = None
    for job in pending_jobs:
        if job.get("entityId") == test_file['id']:
            created_job = job
            break
    assert created_job is not None, f"Job for file {test_file['id']} not found."
    print(f"Found job {created_job['id']} for file {test_file['id']}")
    assert created_job["id"] is not None

    cursor = created_job["createdAt"]
    empty_response = requests.get(f"{base_url}/jobs/pending?cursor={cursor}", headers=headers)
    assert_status_code(empty_response, 200)
    assert len(empty_response.json()) == 0 # This assertion is likely incorrect

    job_type_to_lease = created_job["taskType"]
    leaser_id = "worker-list-lease"
    print(f"Leasing job {created_job['id']} (type: {job_type_to_lease})...")
    payload = {
        "leaserId": leaser_id,
        "leaseDuration": "10s",
        "jobTypes": [job_type_to_lease],
    }
    lease_response = requests.post(f"{base_url}/leases", json=payload, headers=headers)
    assert_status_code(lease_response, 201) # Expect 201 Created

    leased_job = lease_response.json()
    print(f"Leased job data: {leased_job}")
    assert leased_job["id"] == created_job["id"] # ID should persist through lease
    assert leased_job["leaser"] == leaser_id
    assert leased_job["entityId"] == test_file['id']

def test_list_in_progress_jobs(base_url, admin_session, create_test_file):
    """Test listing jobs that are currently leased."""
    headers = admin_session
    leaser_id = "worker-inprogress"
    print("\n--- Running Test: List In Progress ---")

    # Setup: Create and lease a job
    test_file, leased_job_data = create_and_lease_job(
        base_url, headers, create_test_file, leaser_id=leaser_id
    )
    leased_job_id = leased_job_data["id"]

    print("Listing in-progress jobs...")
    response = requests.get(f"{base_url}/jobs/in-progress", headers=headers)
    assert_status_code(response, 200)
    in_progress_jobs = response.json()
    print(f"Found {len(in_progress_jobs)} in-progress jobs.")

    # Verification
    assert len(in_progress_jobs) > 0 # Should have at least the one we leased

    found_job = None
    for job in in_progress_jobs:
        if job.get("id") == leased_job_id:
            found_job = job
            break

    assert found_job is not None, f"Leased job {leased_job_id} not found in in-progress list"
    print(f"Found leased job {leased_job_id} in in-progress list.")
    assert found_job.get("leaser") == leaser_id
    assert found_job.get("entityId") == test_file['id']

# def test_mark_job_done(base_url, admin_session, create_test_file):
#     """Test marking a leased job as done."""
#     headers = admin_session
#     leaser_id = "worker-done"
#     print("\n--- Running Test: Mark Job Done ---")

#     test_file, leased_job_data = create_and_lease_job(
#         base_url, headers, create_test_file, leaser_id=leaser_id
#     )
#     leased_job_id = leased_job_data["id"]

#     print(f"Marking job {leased_job_id} as done by leaser {leaser_id}...")
#     done_payload = {"leaserId": leaser_id}
#     response = requests.patch(f"{base_url}/jobs/{leased_job_id}/done", json=done_payload, headers=headers)

#     # Verification
#     assert_status_code(response, 204) # Expect No Content for PATCH /done
#     print(f"Job {leased_job_id} marked as done successfully.")

#     # Verify it's removed from in-progress
#     time.sleep(0.2) # Allow potential async processing
#     print("Verifying job is removed from in-progress list...")
#     response_inprogress = requests.get(f"{base_url}/jobs/in-progress", headers=headers)
#     assert_status_code(response_inprogress, 200)
#     in_progress_jobs = response_inprogress.json()
#     found_after_done = False
#     for job in in_progress_jobs:
#         if job.get("id") == leased_job_id:
#             found_after_done = True
#             break
#     assert not found_after_done, f"Job {leased_job_id} still found in in-progress after marking done"
#     print("Verified job is not in in-progress list.")

#     print("Verifying job is not back in pending list...")
#     response_pending = requests.get(f"{base_url}/jobs/pending", headers=headers)
#     assert_status_code(response_pending, 200)
#     pending_jobs = response_pending.json()
#     found_in_pending = False
#     for job in pending_jobs:
#         if job.get("id") == leased_job_id:
#              found_in_pending = True
#              break
#     assert not found_in_pending, f"Job {leased_job_id} found back in pending after marking done"
#     print("Verified job is not in pending list.")


def test_mark_job_failed(base_url, admin_session, create_test_file):
    """Test marking a leased job as failed (and expect requeue)."""
    headers = admin_session
    leaser_id = "worker-failed"
    print("\n--- Running Test: Mark Job Failed ---")

    test_file, leased_job_data = create_and_lease_job(
        base_url, headers, create_test_file, leaser_id=leaser_id
    )
    leased_job_id = leased_job_data["id"]
    initial_retry_count = leased_job_data.get("retryCount", 0) # Get initial count if available

    print(f"Marking job {leased_job_id} as failed by leaser {leaser_id}...")
    failed_payload = {"leaserId": leaser_id}
    response = requests.patch(f"{base_url}/jobs/{leased_job_id}/failed", json=failed_payload, headers=headers)

    assert_status_code(response, 204) # Expect No Content for PATCH /failed
    print(f"Job {leased_job_id} marked as failed successfully.")

    time.sleep(0.2) # Allow potential async processing
    print("Verifying job is removed from in-progress list...")
    response_inprogress = requests.get(f"{base_url}/jobs/in-progress", headers=headers)
    assert_status_code(response_inprogress, 200)
    in_progress_jobs = response_inprogress.json()
    found_after_failed = False
    for job in in_progress_jobs:
        if job.get("id") == leased_job_id:
            found_after_failed = True
            break
    assert not found_after_failed, f"Job {leased_job_id} still found in in-progress after marking failed"
    print("Verified job is not in in-progress list.")

    print("Verifying job is back in pending list...")
    response_pending = requests.get(f"{base_url}/jobs/pending", headers=headers)
    assert_status_code(response_pending, 200)
    pending_jobs = response_pending.json()
    requeued_job = None
    for job in pending_jobs:
        if job.get("id") == leased_job_id:
             requeued_job = job
             break
    assert requeued_job is not None, f"Job {leased_job_id} not found back in pending after marking failed"
    print(f"Verified job {leased_job_id} is back in pending list.")

    assert requeued_job.get("retryCount", 0) == initial_retry_count + 1
    print(f"Verified retry count (Expected > {initial_retry_count}, Got: {requeued_job.get('retryCount', 0)})")
