import requests
import os
from workers import parser
from time import sleep

class AuthSession:
    def __init__(self, base_url: str, email: str, password: str):
        self.base_url = base_url
        self.email = email
        self.password = password
        self.session = requests.Session()
        # Set default headers for all requests
        self.session.headers.update({
            "Accept": "application/json",
            "Content-Type": "application/json"
        })
        self.login()

    def login(self):
        payload = {
            "email": self.email,
            "password": self.password,
        }
        response = self.session.post(
            f"{self.base_url}/login",
            json=payload,
        )
        if response.status_code != 200:
            raise Exception(f"Failed to login: {response.status_code} {response.text}")
        self.token = response.json()['token']
        # Update session headers with the authorization token
        self.session.headers.update({"Authorization": f"Bearer {self.token}"})

    def post(self, url, **kwargs):
        return self.session.post(url, **kwargs)

    def get(self, url, **kwargs):
        return self.session.get(url, **kwargs)

    def patch(self, url, **kwargs):
        return self.session.patch(url, **kwargs)

    def delete(self, url, **kwargs):
        return self.session.delete(url, **kwargs)

class Steps:
    def __init__(self, parser: parser.Parser, base_url: str, lease_duration: int, leaser_id: str, session: AuthSession):
        self.parser = parser
        self.leaser_id = leaser_id
        self.lease_duration = lease_duration
        self.base_url = base_url
        self.session = session

    def lease_job(self):
        payload = {
            "leaserId": self.leaser_id,
            "leaseDuration": f"{self.lease_duration}s",
            "jobTypes": self.parser.supported_types(),
        }
        response = self.session.post(f"{self.base_url}/leases", json=payload)
        if response.status_code != 201:
            raise Exception(f"Failed to lease job: {response.status_code} {response.text}")
        return response.json()

    def fetch_file(self, file_id):
        response = self.session.get(f"{self.base_url}/files/{file_id}/download")
        if response.status_code != 200:
            raise Exception(f"Failed to fetch file: {response.status_code} {response.text}")
        return response.content

    def mark_done(self, job_id):
        payload = {"leaserId": self.leaser_id}
        response = self.session.patch(f"{self.base_url}/jobs/{job_id}/done", json=payload)
        if response.status_code != 204:
            raise Exception(f"Failed to mark job done: {response.status_code} {response.text}")

    def mark_failed(self, job_id):
        payload = {"leaserId": self.leaser_id}
        response = self.session.patch(f"{self.base_url}/jobs/{job_id}/failed", json=payload)
        if response.status_code != 204:
            raise Exception(f"Failed to mark job failed: {response.status_code} {response.text}")

    def ingest(self, file_id: str, text: str):
        response = self.session.post(
            f"{self.base_url}/index",
            json={"text": text, "id": file_id},
        )
        if response.status_code != 204:
            raise Exception(f"Failed to ingest text: {response.status_code} {response.text}")

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
    try:
        print("Starting AuthSession...")
        session = AuthSession(
            base_url=config["base_url"],
            email=config["email"],
            password=config["password"],
        )
        print("AuthSession started")
    except Exception as e:
        print(f"Error during login: {e}")
        raise e
    print("Starting Worker...")
    while True:
        try:
            worker = Steps(
                parser=parser,
                base_url=config["base_url"],
                leaser_id=config["leaser_id"],
                lease_duration=int(config["lease_duration"]),
                session=session
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
        if job_id is None:
            print("No job available")
            sleep(1)
            return
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
