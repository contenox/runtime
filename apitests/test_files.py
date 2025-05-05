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
    assert_status_code(delete_response, 200)

    # Verify it's gone
    get_response = requests.get(
        f"{base_url}/files/{test_file['id']}",
        headers=headers
    )
    assert_status_code(get_response, 404)

def test_list_files(base_url, admin_session, create_test_file):
    """Test that we can list files with path filtering."""
    test_file = create_test_file()
    headers = admin_session

    # Test filtering by path
    response = requests.get(
        f"{base_url}/files",
        params={'path': "test"},
        headers=headers
    )

    assert_status_code(response, 200)
    files = response.json()
    assert isinstance(files, list)
    assert len(files) >= 1
    assert any(f['path'] == test_file['path'] for f in files)

def test_create_folder(base_url, admin_session):
    """Test that an admin can create a folder."""
    headers = admin_session
    data = {'path': 'test/folder'}

    response = requests.post(
        f"{base_url}/folders",
        json=data,
        headers=headers
    )

    assert_status_code(response, 201)
    folder_data = response.json()
    assert 'id' in folder_data
    assert folder_data['path'] == 'test/folder'


def test_rename_folder(base_url, admin_session):
    """Test that an admin can rename a folder."""
    # Create a folder to rename
    create_data = {'path': 'old/folder/path'}
    create_response = requests.post(
        f"{base_url}/folders",
        json=create_data,
        headers=admin_session
    )
    assert_status_code(create_response, 201)
    folder_id = create_response.json()['id']

    # Rename the folder
    new_path = 'new/folder/path'
    update_data = {'path': new_path}
    update_response = requests.put(
        f"{base_url}/folders/{folder_id}/path",
        json=update_data,
        headers=admin_session
    )

    assert_status_code(update_response, 200)
    updated_folder = update_response.json()
    assert updated_folder['id'] == folder_id
    assert updated_folder['path'] == new_path

def test_rename_file(base_url, admin_session, create_test_file):
    """Test that an admin can rename a file's path."""
    test_file = create_test_file()
    new_path = 'renamed/file.txt'
    data = {'path': new_path}

    response = requests.put(
        f"{base_url}/files/{test_file['id']}/path",
        json=data,
        headers=admin_session
    )

    assert_status_code(response, 200)
    updated_file = response.json()
    assert updated_file['id'] == test_file['id']
    assert updated_file['path'] == new_path

    # Verify the update via get metadata
    get_response = requests.get(
        f"{base_url}/files/{test_file['id']}",
        headers=admin_session
    )
    assert_status_code(get_response, 200)
    metadata = get_response.json()
    assert metadata['path'] == new_path

def test_rename_folder_updates_child_paths(base_url, admin_session, create_test_file):
    """Test renaming a folder updates nested file paths."""
    # Create folder structure
    folder_data = {'path': 'parent/old_folder'}
    folder_res = requests.post(
        f"{base_url}/folders",
        json=folder_data,
        headers=admin_session
    )
    assert_status_code(folder_res, 201)
    folder_id = folder_res.json()['id']

    # Create file inside folder
    file_path = 'parent/old_folder/nested_file.txt'
    test_file = create_test_file(path=file_path)

    # Rename the folder
    new_path = 'parent/new_folder'
    update_res = requests.put(
        f"{base_url}/folders/{folder_id}/path",
        json={'path': new_path},
        headers=admin_session
    )
    assert_status_code(update_res, 200)

    # Verify file path updated
    file_res = requests.get(
        f"{base_url}/files/{test_file['id']}",
        headers=admin_session
    )
    updated_file = file_res.json()
    expected_path = 'parent/new_folder/nested_file.txt'
    assert updated_file['path'] == expected_path
