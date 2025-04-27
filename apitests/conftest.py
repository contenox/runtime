import requests
import logging # Use logging instead of print
from abc import ABC, abstractmethod
from typing import Dict, Any, List, Optional

# Configure basic logging
logging.basicConfig(level=logging.INFO, format='%(asctime)s - %(levelname)s - %(name)s - %(message)s')
logger = logging.getLogger("BaseWorker")

class AuthenticationError(Exception):
    """Custom exception for authentication failures."""
    pass

class APIError(Exception):
    """Custom exception for general API errors."""
    pass

class BaseWorker(ABC):
    def __init__(self, config: Dict[str, Any]):
        """
        Initializes the BaseWorker.

        Args:
            config: A dictionary containing configuration, including:
                    - base_url (str): The base URL of the API.
                    - email (str): User email for authentication.
                    - password (str): User password for authentication.
                    - leaser_id (str): The ID of the leaser for job operations.
                    - lease_duration (int): Duration in seconds for leasing jobs.
                    - headers (Optional[Dict[str, str]]): Optional base headers.
                    - request_timeout (Optional[int]): Optional timeout for requests (default: 30).
        """
        self.config = config
        self.base_url = config["base_url"]
        self._email = config["email"]
        self._password = config["password"]
        self._token: Optional[str] = None
        self._base_headers = config.get("headers", {}).copy()
        self.request_timeout = config.get("request_timeout", 30)

        # Attempt to login and get the token upon initialization
        self._login()
        logger.info("Worker initialized and logged in successfully.")

    def _login(self):
        """Authenticates with the API and stores the token."""
        login_url = f"{self.base_url}/login"
        payload = {
            "email": self._email,
            "password": self._password,
        }
        logger.info(f"Attempting login for user {self._email} at {login_url}")
        try:
            response = requests.post(
                login_url,
                json=payload,
                headers=self._base_headers,
                timeout=self.request_timeout
            )
            response.raise_for_status()

            response_data = response.json()
            self._token = response_data.get("token")

            if not self._token:
                logger.error("Login successful but no token found in response.")
                raise AuthenticationError("Login response did not contain a token.")

            logger.info("Login successful, token obtained.")
            # Update headers for subsequent requests AFTER successful login
            self._base_headers["Authorization"] = f"Bearer {self._token}"

        except requests.exceptions.RequestException as e:
            logger.exception(f"Login failed for user {self._email}: {e}")
            raise AuthenticationError(f"Login request failed: {e}") from e
        except Exception as e:
             logger.exception(f"An unexpected error occurred during login: {e}")
             raise AuthenticationError(f"An unexpected error during login: {e}") from e

    @property
    def headers(self) -> Dict[str, str]:
        """Returns the headers including the auth token."""
        if not self._token:
            logger.error("Headers requested but token is not available.")
            raise AuthenticationError("Cannot make authenticated request: Not logged in.")
        return self._base_headers.copy()

    @abstractmethod
    def parse(self, raw_data: bytes) -> str:
        """
        Abstract method to parse the raw file data.
        Must be implemented by subclasses.

        Args:
            raw_data: The raw byte content of the file.

        Returns:
            The parsed text content as a string.
        """
        raise NotImplementedError

    def _make_request(self, method: str, endpoint: str, **kwargs) -> requests.Response:
        """Helper method to make authenticated API requests."""
        url = f"{self.base_url}{endpoint}"
        try:
            response = requests.request(
                method,
                url,
                headers=self.headers,
                timeout=self.request_timeout,
                **kwargs
            )
            if not response.ok: # checks status_code < 400
                 logger.error(f"API request failed: {method} {url} - Status: {response.status_code} Body: {response.text[:500]}")
                 response.raise_for_status()
            return response
        except requests.exceptions.RequestException as e:
            logger.exception(f"API request error: {method} {url} - {e}")
            raise APIError(f"API request failed: {e}") from e

    def lease_job(self, worker_types: List[str]) -> Dict[str, Any]:
        """Lease a pending job for this worker."""
        payload = {
            "leaserId": self.config["leaser_id"],
            "leaseDuration": self.config["lease_duration"],
            "jobTypes": worker_types,
        }
        logger.info(f"Leasing job with types: {worker_types}")
        response = self._make_request("post", "/leases", json=payload)

        if response.status_code != 201:
             logger.warning(f"Lease job request returned status {response.status_code} (expected 201), but was successful ({response.ok}). Body: {response.text[:200]}")

        return response.json()

    def fetch_file(self, file_id: str) -> bytes:
        """Download the file by ID."""
        logger.info(f"Fetching file with ID: {file_id}")
        response = self._make_request("get", f"/files/{file_id}/download")
        return response.content

    def mark_done(self, job_id: str):
        """Mark the job as done."""
        payload = {"leaserId": self.config["leaser_id"]}
        logger.info(f"Marking job {job_id} as done.")
        response = self._make_request("patch", f"/jobs/{job_id}/done", json=payload)
        if response.status_code != 204:
             logger.warning(f"Mark job done request returned status {response.status_code} (expected 204), but was successful ({response.ok}).")

    def mark_failed(self, job_id: str):
        """Mark the job as failed."""
        payload = {"leaserId": self.config["leaser_id"]}
        logger.warning(f"Marking job {job_id} as failed.") # Warning level seems appropriate
        response = self._make_request("patch", f"/jobs/{job_id}/failed", json=payload)
        if response.status_code != 204:
             logger.warning(f"Mark job failed request returned status {response.status_code} (expected 204), but was successful ({response.ok}).")


    def ingest(self, file_id: str, text: str):
        """Ingest text output."""
        logger.info(f"Ingesting {len(text)} characters for file ID: {file_id}")
        try:
            self._make_request(
                "post",
                "/index",
                json={"text": text, "id": file_id}
            )
            logger.info(f"Successfully ingested text for file ID: {file_id}")
        except APIError as e:
            logger.exception(f"Failed to ingest text for file ID {file_id}: {e}")
            raise e

    def run(self, worker_types: List[str]):
        """Main worker loop to lease and process jobs."""
        while True:
            job_id = None
            try:
                logger.info("[Worker] Leasing a job...")
                job = self.lease_job(worker_types)
                if not job:
                    logger.info("[Worker] No job available, waiting...")
                    import time
                    time.sleep(10)
                    continue

                file_id = job.get("entityId")
                job_id = job.get("id")

                if not file_id or not job_id:
                    logger.error(f"[Worker] Leased job is missing 'entityId' or 'id'. Job data: {job}")
                    continue

                logger.info(f"[Worker] Leased job {job_id} for file {file_id}.")

                logger.info(f"[Worker] Downloading file {file_id}...")
                raw_data = self.fetch_file(file_id)

                logger.info("[Worker] Parsing...")
                parsed = self.parse(raw_data)

                logger.info("[Worker] Ingesting text...")
                self.ingest(file_id, parsed)

                logger.info(f"[Worker] Marking job {job_id} as done.")
                self.mark_done(job_id)

            except AuthenticationError as e:
                logger.critical(f"[Worker] CRITICAL: Authentication error, stopping worker: {e}")
                break
            except APIError as e:
                 logger.error(f"[Worker] API Error during job processing: {e}")
                 if job_id:
                     logger.warning(f"[Worker] Attempting to mark job {job_id} as failed due to API Error.")
                     try:
                         self.mark_failed(job_id)
                     except Exception as mark_fail_e:
                         logger.exception(f"[Worker] CRITICAL: Failed to mark job {job_id} as failed after another error: {mark_fail_e}")
                 import time
                 time.sleep(5)
            except Exception as e:
                logger.exception(f"[Worker] UNEXPECTED ERROR: {e}")
                if job_id:
                    logger.warning(f"[Worker] Attempting to mark job {job_id} as failed due to unexpected error.")
                    try:
                        self.mark_failed(job_id)
                    except Exception as mark_fail_e:
                        logger.exception(f"[Worker] CRITICAL: Failed to mark job {job_id} as failed after unexpected error: {mark_fail_e}")
                import time
                time.sleep(5)

# Example Usage (Requires a concrete implementation)
# class MyWorker(BaseWorker):
#     def parse(self, raw_data: bytes) -> str:
#         # Replace with actual parsing logic
#         return raw_data.decode('utf-8', errors='ignore')

# if __name__ == "__main__":
#     # Load config from somewhere (e.g., file, environment variables)
#     config = {
#         "base_url": "http://localhost:8081/api", # Your API base URL
#         "email": "worker_user@example.com",    # Worker email
#         "password": "worker_password",         # Worker password
#         "leaser_id": "worker_instance_01",     # Unique ID for this worker instance
#         "lease_duration": 600,                 # Lease duration in seconds (e.g., 10 minutes)
#         "request_timeout": 60,                 # Optional: Timeout for API calls
#         # "headers": {"X-Custom-Header": "value"} # Optional: Other base headers
#     }
#     try:
#         worker = MyWorker(config)
#         worker.run(worker_types=["file_processing", "text_extraction"]) # Specify job types this worker handles
#     except AuthenticationError:
#         logger.critical("Worker failed to initialize due to authentication error.")
#     except Exception as e:
#         logger.critical(f"Worker stopped due to an unhandled error: {e}")
