import requests
from helpers import assert_status_code

def test_create_file(base_url, admin_session, tmp_path):
    """Test that an admin can upload a file."""
    headers = admin_session
    file_path = tmp_path / "testfile.txt"
    file_path.write_text("Test file content")

    with open(file_path, 'rb') as f:
        files = {'file': f}
        data = {'path': 'test/path.txt'}
        response = requests.post(
            f"{base_url}/files",
            files=files,
            data=data,
            headers=headers
        )

    assert_status_code(response, 201)
    file_data = response.json()
    assert 'id' in file_data
    assert file_data['path'] == 'test/path.txt'
    assert file_data['size'] == len("Test file content")

def test_get_file_metadata(base_url, admin_session, create_test_file):
    """Test that we can retrieve file metadata."""
    test_file = create_test_file()
    headers = admin_session

    response = requests.get(
        f"{base_url}/files/{test_file['id']}",
        headers=headers
    )

    assert_status_code(response, 200)
    metadata = response.json()
    assert metadata['id'] == test_file['id']
    assert metadata['path'] == test_file['path']
    assert metadata['size'] == test_file['size']

def test_download_file(base_url, admin_session, create_test_file):
    """Test that we can download a file."""
    test_file = create_test_file()
    headers = admin_session

    response = requests.get(
        f"{base_url}/files/{test_file['id']}/download",
        headers=headers
    )

    assert_status_code(response, 200)
    assert response.content == test_file['content'].encode()
    assert response.headers['Content-Length'] == str(test_file['size'])

def test_update_file(base_url, admin_session, create_test_file, tmp_path):
    """Test that we can update a file."""
    test_file = create_test_file()
    headers = admin_session

    # Create new file content
    new_file_path = tmp_path / "newfile.txt"
    new_content = "Updated content"
    new_file_path.write_text(new_content)

    with open(new_file_path, 'rb') as f:
        files = {'file': f}
        data = {'path': 'updated/path.txt'}
        response = requests.put(
            f"{base_url}/files/{test_file['id']}",
            files=files,
            data=data,
            headers=headers
        )

    assert_status_code(response, 200)
    updated_file = response.json()
    assert updated_file['id'] == test_file['id']
    assert updated_file['path'] == 'updated/path.txt'
    assert updated_file['size'] == len(new_content)

def test_delete_file(base_url, admin_session, create_test_file):
    """Test that we can delete a file."""
    test_file = create_test_file()
    headers = admin_session

    # Delete the file
    delete_response = requests.delete(
        f"{base_url}/files/{test_file['id']}",
        headers=headers
    )
    assert_status_code(delete_response, 204)

    # Verify it's gone
    get_response = requests.get(
        f"{base_url}/files/{test_file['id']}",
        headers=headers
    )
    assert_status_code(get_response, 404)

def test_list_paths(base_url, admin_session, create_test_file):
    """Test that we can list file paths."""
    test_file = create_test_file()
    headers = admin_session

    response = requests.get(
        f"{base_url}/files/paths",
        headers=headers
    )

    assert_status_code(response, 200)
    paths = response.json()
    assert isinstance(paths, list)
    assert test_file['path'] in paths
