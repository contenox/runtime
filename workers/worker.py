import requests
import os
from workers import parser

class Steps():
    def __init__(self, parser: parser.Parser, base_url: str, lease_duration: int, leaser_id: str, headers: dict):
        """Loads required configuration from environment variables."""
        self.parser = parser
        self.leaser_id = leaser_id
        self.lease_duration = lease_duration
        self.base_url = base_url
        self.headers = headers

    def lease_job(self):
        """Lease a pending job for this worker."""
        payload = {
            "leaserId": self.leaser_id,
            "leaseDuration": self.lease_duration,
            "jobTypes": self.parser.supported_types(),
        }
        response = requests.post(
            f"{self.base_url}/leases",
            json=payload,
            headers=self.headers,
        )
        if response.status_code != 201:
            raise Exception(f"Failed to lease job: {response.status_code} {response.text}")
        return response.json()

    def fetch_file(self, file_id):
        """Download the file by ID."""
        response = requests.get(
            f"{self.base_url}/files/{file_id}/download",
            headers=self.headers,
        )
        if response.status_code != 200:
            raise Exception(f"Failed to fetch file: {response.status_code} {response.text}")
        return response.content

    def mark_done(self, job_id):
        """Mark the job as done."""
        payload = {"leaserId": self.leaser_id}
        response = requests.patch(
            f"{self.base_url}/jobs/{job_id}/done",
            json=payload,
            headers=self.headers,
        )
        if response.status_code != 204:
            raise Exception(f"Failed to mark job done: {response.status_code} {response.text}")

    def mark_failed(self, job_id):
        """Mark the job as failed."""
        payload = {"leaserId": self.leaser_id}
        response = requests.patch(
            f"{self.base_url}/jobs/{job_id}/failed",
            json=payload,
            headers=self.headers,
        )
        if response.status_code != 204:
            raise Exception(f"Failed to mark job failed: {response.status_code} {response.text}")

    def ingest(self, file_id: str,text: str):
        """ingest text output."""
        response = requests.post(
            f"{self.base_url}/index",
            json={"text": text, "id": file_id},
            headers=self.headers,
        )
        if response.status_code != 204:
            raise Exception(f"Failed to ingest text: {response.status_code} {response.text}")


def login(base_url :str, email :str, password :str, headers :dict) -> dict:
    payload = {
        "email": email,
        "password": password,
    }
    response = requests.post(
        f"{base_url}/login",
        json=payload,
        headers=headers,
    )
    if response.status_code != 200:
        raise Exception(f"Failed to login: {response.status_code} {response.text}")
    headers["Authorization"] = f"Bearer {response.json()['token']}"
    return headers

def load_config() -> dict:
    config = {}
    required_vars = {
        "API_BASE_URL": "base_url",
        "WORKER_EMAIL": "email",
        "WORKER_PASSWORD": "password",
        "WORKER_LEASER_ID": "leaser_id",
        "WORKER_LEASE_DURATION_SECONDS": "lease_duration",
        "WORKER_REQUEST_TIMEOUT_SECONDS": "request_timeout",
    }
    missing = []
    for env_var, config_key in required_vars.items():
        value = os.getenv(env_var)
        if not value:
            missing.append(env_var)
        else:
            config[config_key] = value

    if missing:
        raise ValueError(f"missing required environment variables: {', '.join(missing)}")
    return config

def cycle(parser: parser.Parser, config: dict):
    headers = {}
    try:
        headers = login(config["base_url"], config["email"], config["password"], headers)
    except Exception as e:
        print(f"Error: {e}")
        raise e
    while True:
        try:
            worker = Steps(parser=parser,
                base_url=config["base_url"],
                leaser_id=config["leaser_id"],
                lease_duration=config["lease_duration"],
                headers=headers
            )
            run(worker)
        except Exception as e:
            print(f"Error: {e}")

def run(worker_steps: Steps):
    job_id = None
    try:
        print("Leasing a job...")
        job = worker_steps.lease_job()
        file_id = job["entityId"]
        job_id = job["id"]
        print("Job leased")
        print(f"Downloading file {file_id}...")
        raw_data = worker_steps.fetch_file(file_id)
        print("File downloaded")
        print("Parsing...")
        parsed = worker_steps.parser.parse(raw_data)
        print("Parsing complete")
        print("Ingesting text...")
        worker_steps.ingest(file_id, parsed)
        print("Ingestion complete")
        print(f"Marking job {job_id} as done.")
        worker_steps.mark_done(job_id)
        print(f"Job {job_id} completed.")

    except Exception as e:
        print(f"ERROR: {e}")
        if job_id is not None:
            print(f"marking job {job_id} as failed.")
            worker_steps.mark_failed(job_id)
