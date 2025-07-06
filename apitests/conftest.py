import pytest
import uuid
import requests
import logging
import time


BASE_URL = "http://localhost:8081/api"
OLLAMA_URL = "http://host.docker.internal:11435"
DEFAULT_POLL_INTERVAL = 3  # seconds
DEFAULT_TIMEOUT = 420       # seconds

# Configure the root logger
logging.basicConfig(level=logging.ERROR,
                    format='%(asctime)s - %(levelname)s - %(name)s - %(message)s')

# Create a logger object for your test module
logger = logging.getLogger(__name__)

@pytest.fixture(scope="session")
def base_url():
    logger.debug("Providing base URL: %s", BASE_URL)
    return BASE_URL

@pytest.fixture
def generate_email():
    """Generate a unique email address."""
    def _generate(prefix="user"):
        email = f"{prefix}_{uuid.uuid4().hex[:8]}@example.com"
        logger.debug("Generated email: %s", email)
        return email
    return _generate

@pytest.fixture(scope="session")
def admin_email():
    """Default admin email."""
    return "admin@admin.com"

@pytest.fixture(scope="session")
def admin_password():
    """Default admin password."""
    return "admin123"

@pytest.fixture
def register_user(base_url):
    """
    Fixture that registers a user and returns their token.
    Usage:
        token = register_user(email, friendly_name, password)
    """
    def _register(email, friendly_name, password):
        user_data = {
            "email": email,
            "friendlyName": friendly_name,
            "password": password
        }
        logger.info("Registering user: %s", email)
        try:
            response = requests.post(f"{base_url}/register", json=user_data)
            response.raise_for_status()
            logger.debug("User registration response: %s", response.json())
        except requests.RequestException as e:
            logger.exception("User registration failed for %s: %s", email, e)
            pytest.fail(f"Registration failed: {e}")
        assert response.status_code == 201, f"Registration failed: {response.text}"
        token = response.json().get("token", "")
        user_id = response.json().get("user", {}).get("id", None)
        logger.info("User registered successfully, token obtained.")
        return {"token": token, "user_id": user_id}
    return _register

@pytest.fixture
def auth_headers(register_user, base_url, generate_email):
    """
    Fixture that registers a user and returns the authorization headers.
    """
    email = generate_email("auth")
    token = register_user(email, "Test User", "password123")
    logger.debug("Auth headers created for %s", email)
    return {"Authorization": f"Bearer {token}"}


@pytest.fixture(scope="session")
def admin_session(base_url, admin_email, admin_password):
    """
    Registers an admin user and returns authentication headers with session scope.
    """
    admin_data = {
        "email": admin_email,
        "friendlyName": "Admin User",
        "password": admin_password
    }
    logger.info("Registering admin user: %s", admin_email)
    try:
        response = requests.post(f"{base_url}/register", json=admin_data)
        response.raise_for_status()
        logger.debug("Admin registration response: %s", response.json())
    except requests.RequestException as e:
        logger.exception("Admin registration failed: %s", e)
        pytest.fail(f"Admin registration failed: {e}")
    assert response.status_code == 201, f"Admin registration failed: {response.text}"
    token = response.json().get("token", "")
    logger.info("Admin registered successfully, token obtained.")
    return {"Authorization": f"Bearer {token}"}

@pytest.fixture
def create_test_file(base_url, admin_session, tmp_path):
    """Fixture to create a test file and return its metadata.

    Usage:
        test_file = create_test_file(file_name="my-test-file.txt")
    """
    def _create_test_file(content="Test content", file_name=None, path=None):
        if file_name is None:
            file_name = f"tempfile-{uuid.uuid4().hex}.txt"

        # Ensure tmp_path is used
        file_path = tmp_path / file_name
        file_path.write_text(content)

        # Prepare upload data
        if path is None:
            path = file_name  # Use file_name as default path if not provided

        with open(file_path, 'rb') as f:
            files = {'file': f}
            data = {'name': path}
            response = requests.post(
                f"{base_url}/files",
                files=files,
                data=data,
                headers=admin_session
            )
            response.raise_for_status()
            file_data = response.json()
            file_data['content'] = content
            return file_data

    return _create_test_file
@pytest.fixture
def create_test_folder(base_url, admin_session):
    """Fixture to create a test folder and return its metadata (simple pattern)."""

    def _create_test_folder(path, parent_id=""):
        folder_path = path
        logger.info(f"Creating folder: path='{folder_path}', parent_id='{parent_id}'")

        data = {'path': folder_path, 'parentId': parent_id}

        response = requests.post(
            f"{base_url}/folders",
            json=data,
            headers=admin_session
        )
        response.raise_for_status()

        folder_data = response.json()
        assert 'id' in folder_data
        logger.info(f"Folder created successfully: {folder_data}")
        return folder_data # Return the parsed JSON data

    return _create_test_folder

@pytest.fixture(scope="session")
def with_ollama_backend():
    """Check if the Ollama backend is reachable and return its base URL."""
    try:
        location = OLLAMA_URL.replace("host.docker.internal", "localhost")
        response = requests.get(f"{location}", timeout=5)
        response.raise_for_status()
        logger.info("Ollama backend is reachable.")
    except requests.RequestException as e:
        logger.error("Ollama backend not reachable: %s", e)
        pytest.fail(f"Ollama backend check failed: {e}")
    return OLLAMA_URL


@pytest.fixture(scope="session")
def create_backend_and_assign_to_pool(base_url, admin_session, with_ollama_backend):
    """
    Fixture that creates a backend and assigns it to the 'internal_embed_pool'.
    Returns: dict containing backend_id and pool_id
    """
    headers = admin_session
    ollama_url = with_ollama_backend

    payload = {
        "name": "Test Embedder Backend",
        "baseUrl": ollama_url,
        "type": "ollama",
    }
    response = requests.post(f"{base_url}/backends", json=payload, headers=headers)
    response.raise_for_status()
    backend = response.json()
    backend_id = backend["id"]

    pool_id = "internal_embed_pool"
    assign_url = f"{base_url}/backend-associations/{pool_id}/backends/{backend_id}"
    response = requests.post(assign_url, headers=headers)
    response.raise_for_status()
    assert response.json() == "backend assigned"

    logger.info("Backend %s assigned to pool %s", backend_id, pool_id)

    yield {
        "backend_id": backend_id,
        "pool_id": pool_id,
        "backend": backend,
    }

@pytest.fixture(scope="session")
def create_model_and_assign_to_pool(base_url, admin_session):
    """
    Fixture that creates a model 'smollm2:135m' and assigns it to the Embedder pool.
    Returns: dict containing model_id and pool_id
    """
    headers = admin_session
    model_name = "smollm2:135m"
    pool_id = "internal_embed_pool"

    payload = {"model": model_name}
    create_url = f"{base_url}/models"
    response = requests.post(create_url, json=payload, headers=headers)
    assert response.status_code == 201, f"Model creation failed: {response.text}"
    model = response.json()
    model_id = model["id"]
    logger.info("Created model: %s", model_id)

    assign_url = f"{base_url}/model-associations/{pool_id}/models/{model_id}"
    response = requests.post(assign_url, headers=headers)
    assert response.status_code == 200, f"Failed to assign model to pool: {response.text}"
    assert response.json() == "model assigned", "Unexpected response when assigning model"
    logger.info("Assigned model %s to pool %s", model_id, pool_id)

    yield {
        "model_id": model_id,
        "model_name": model_name,
        "pool_id": pool_id,
    }

@pytest.fixture(scope="session")
def wait_for_model_in_backend(base_url, admin_session):
    """
    Fixture that waits until a specific model appears in a backend's pulledModels list.

    Usage:
        def test_something(wait_for_model_in_backend):
            backend_data = wait_for_model_in_backend(model_name="smollm2:135m", backend_id="...")
            assert any(m['name'] == "smollm2:135m" for m in backend_data["pulledModels"])
    """
    def _wait_for_model(*, model_name, backend_id, timeout=DEFAULT_TIMEOUT, poll_interval=DEFAULT_POLL_INTERVAL):
        url = f"{base_url}/backends/{backend_id}"
        start_time = time.time()

        while True:
            try:
                response = requests.get(url, headers=admin_session)
                if response.status_code != 200:
                    logger.warning("Failed to fetch backend info: %s", response.text)
                    time.sleep(poll_interval)
                    continue

                data = response.json()
                pulled_models = data.get("pulledModels", [])
                logger.debug("Pulled models: %s", [m.get('name') for m in pulled_models])

                if any(m.get('name') == model_name for m in pulled_models):
                    logger.info("✅ Model '%s' found in backend '%s'", model_name, backend_id)
                    return data

                if time.time() - start_time > timeout:
                    raise TimeoutError(
                        f"⏰ Timed out waiting for model '{model_name}' to appear in backend '{backend_id}'. "
                        f"Last response: {data}"
                    )

                logger.info("⏳ Waiting for model '%s' to appear in backend '%s'...", model_name, backend_id)
                time.sleep(poll_interval)

            except requests.RequestException as e:
                logger.warning("Network error while polling backend: %s", str(e))
                time.sleep(poll_interval)

    return _wait_for_model

@pytest.fixture
def create_test_chain(base_url, admin_session):
    """Fixture to create test chain and clean up after"""
    payload = {
        "id": "test-chain-" + str(uuid.uuid4())[:8],
        "tasks": [{"id": "task1", "type": "llm"}]
    }

    response = requests.post(f"{base_url}/chains",
                           json=payload,
                           headers=admin_session)
    assert response.status_code == 201

    yield response.json()

    # Teardown
    requests.delete(
        f"{base_url}/chains/{payload['id']}",
        headers=admin_session
    )
