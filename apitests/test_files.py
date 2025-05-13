import requests
from helpers import assert_status_code
import uuid

def test_create_file(base_url, admin_session, tmp_path):
    """Test that an admin can upload a file."""
    headers = admin_session
    file_path = tmp_path / "testfile.txt"
    file_path.write_text("Test file content")

    with open(file_path, 'rb') as f:
        files = {'file': f}
        data = {
            'name': 'path.txt',
            'parentid':'',
        }
        response = requests.post(
            f"{base_url}/files",
            files=files,
            data=data,
            headers=headers
        )

    assert_status_code(response, 201)
    file_data = response.json()
    assert 'id' in file_data
    assert file_data['path'] == 'path.txt'
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
        response = requests.put(
            f"{base_url}/files/{test_file['id']}",
            files=files,
            headers=headers
        )

    assert_status_code(response, 200)
    updated_file = response.json()
    assert updated_file['id'] == test_file['id']
    assert updated_file['path'] == test_file['path']
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
        params={'path': ""},
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
    data = {'name': 'test/folder'}

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
    create_data = {'name': 'old'}
    create_response = requests.post(
        f"{base_url}/folders",
        json=create_data,
        headers=admin_session
    )
    assert_status_code(create_response, 201)
    folder_id = create_response.json()['id']

    # Rename the folder
    new_path = 'new'
    update_data = {'name': new_path}
    update_response = requests.put(
        f"{base_url}/folders/{folder_id}/name",
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
    data = {'name': new_path}

    response = requests.put(
        f"{base_url}/files/{test_file['id']}/name",
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

def test_create_file_exceeds_max_upload_size(base_url, admin_session, tmp_path):
    """Test creating a file larger than MaxUploadSize."""
    headers = admin_session
    large_content = b"a" * (1 * 1024 * 1024 + 1)
    file_path = tmp_path / "largefile.bin"
    file_path.write_bytes(large_content)

    with open(file_path, 'rb') as f:
        files = {'file': ('largefile.bin', f)}
        data = {'name': 'large_file.bin', 'parentid': ''}
        response = requests.post(
            f"{base_url}/files",
            files=files,
            data=data,
            headers=headers
        )

    assert response.status_code in [400, 413], \
        f"Expected 400 or 413, got {response.status_code}. Response: {response.text}"
    # TODO: More specific error message check
    # error_data = response.json()
    # assert "exceeded" in error_data.get("error", "").lower()

def test_create_file_empty_content(base_url, admin_session, tmp_path):
    """Test creating a file with empty content."""
    headers = admin_session
    file_path = tmp_path / "emptyfile.txt"
    file_path.write_text("") # Empty content

    with open(file_path, 'rb') as f:
        files = {'file': ('emptyfile.txt', f)}
        data = {'name': 'empty.txt', 'parentid': ''}
        response = requests.post(
            f"{base_url}/files",
            files=files,
            data=data,
            headers=headers
        )
    assert_status_code(response, 400)
    error_data = response.json()
    assert "empty" in error_data.get("error", "").lower()

def test_create_file_missing_file_part(base_url, admin_session):
    """Test creating a file without the 'file' multipart field."""
    headers = admin_session
    data = {'name': 'somefile.txt', 'parentid': ''}
    response = requests.post(
        f"{base_url}/files",
        data=data, # No 'files' parameter
        headers=headers
    )
    assert_status_code(response, 415)
    error_data = response.json()
    assert "content-type isn't multipart/form-data" in error_data.get("error", "").lower()

def test_create_file_with_invalid_parent_id(base_url, admin_session, tmp_path):
    """Test creating a file with a non-existent parent ID."""
    headers = admin_session
    non_existent_parent_id = str(uuid.uuid4())

    file_path = tmp_path / "orphan_file.txt"
    file_path.write_text("Orphan content")

    with open(file_path, 'rb') as f:
        files = {'file': ('orphan_file.txt', f)}
        data = {'name': 'orphan.txt', 'parentid': non_existent_parent_id}
        response = requests.post(
            f"{base_url}/files",
            files=files,
            data=data,
            headers=headers
        )

    assert response.status_code in [400, 404, 500], \
        f"Expected 400, 404 or 500, got {response.status_code}. Response: {response.text}"

def test_get_file_metadata_not_found(base_url, admin_session):
    """Test retrieving metadata for a non-existent file."""
    headers = admin_session
    non_existent_id = str(uuid.uuid4())
    response = requests.get(
        f"{base_url}/files/{non_existent_id}",
        headers=headers
    )
    assert_status_code(response, 404)

def test_download_file_not_found(base_url, admin_session):
    """Test downloading a non-existent file."""
    headers = admin_session
    non_existent_id = str(uuid.uuid4())
    response = requests.get(
        f"{base_url}/files/{non_existent_id}/download",
        headers=headers
    )
    assert_status_code(response, 404)

def test_download_file_skip_content_disposition(base_url, admin_session, create_test_file):
    """Test downloading a file with skip=true for Content-Disposition."""
    test_file = create_test_file(content="inline content")
    headers = admin_session

    response = requests.get(
        f"{base_url}/files/{test_file['id']}/download?skip=true",
        headers=headers
    )

    assert_status_code(response, 200)
    assert response.content == test_file['content'].encode()
    assert 'Content-Disposition' not in response.headers
    assert response.headers['Content-Length'] == str(test_file['size'])

def test_update_file_not_found(base_url, admin_session, tmp_path):
    """Test updating a non-existent file."""
    headers = admin_session
    non_existent_id = str(uuid.uuid4())

    new_file_path = tmp_path / "newfile_for_non_existent.txt"
    new_content = "Updated content for non-existent"
    new_file_path.write_text(new_content)

    with open(new_file_path, 'rb') as f:
        files = {'file': ('newfile_for_non_existent.txt', f)}
        response = requests.put(
            f"{base_url}/files/{non_existent_id}",
            files=files,
            headers=headers
        )
    assert_status_code(response, 404)

def test_list_files_non_existent_path(base_url, admin_session):
    """Test listing files for a path that does not exist."""
    headers = admin_session
    response = requests.get(
        f"{base_url}/files",
        params={'path': f"non_existent_path/{uuid.uuid4()}"},
        headers=headers
    )
    assert_status_code(response, 404)

def test_rename_file_name_includes_slashes(base_url, admin_session, create_test_file):
    """
    Test renaming a file where the new 'name' (from pathUpdateRequest.Path) includes slashes.
    This tests how the backend interprets it: as a complex name or a path change.
    Based on fileservice.RenameFile(ctx, fileID, newName string), it should treat it as a single name.
    """
    test_file = create_test_file(path="original_file.txt")
    headers = admin_session

    new_name_with_slashes = 'folder_level/renamed_with_slash.txt'
    data = {'name': new_name_with_slashes}
    response = requests.put(
        f"{base_url}/files/{test_file['id']}/name",
        json=data,
        headers=headers
    )
    assert_status_code(response, 200)
    updated_file = response.json()
    assert updated_file['id'] == test_file['id']

    assert updated_file['path'] == new_name_with_slashes

    # Verify with a direct GET
    get_response = requests.get(f"{base_url}/files/{test_file['id']}", headers=headers)
    assert_status_code(get_response, 200)
    metadata = get_response.json()
    assert metadata['path'] == new_name_with_slashes
    def test_move_file_to_folder(base_url, admin_session, create_test_file, create_test_folder):
        """Test moving a file from root into a folder."""
        target_folder = create_test_folder(name="target_for_file_move")
        file_to_move = create_test_file(path="file_to_move.txt", content="move me")

        move_payload = {'newParentId': target_folder['id']}
        response = requests.put(
            f"{base_url}/files/{file_to_move['id']}/move",
            json=move_payload,
            headers=admin_session
        )
        assert_status_code(response, 200)
        moved_file_data = response.json()

        assert moved_file_data['id'] == file_to_move['id']
        assert moved_file_data['parentId'] == target_folder['id']
        assert moved_file_data['path'] == f"{target_folder['path']}/{file_to_move['path']}"

        # Verify with a GET request
        get_response = requests.get(f"{base_url}/files/{file_to_move['id']}", headers=admin_session)
        assert_status_code(get_response, 200)
        verified_file_data = get_response.json()
        assert verified_file_data['parentId'] == target_folder['id']
        assert verified_file_data['path'] == f"{target_folder['path']}/{file_to_move['path']}"

    def test_move_file_to_root(base_url, admin_session, create_test_file, create_test_folder):
        """Test moving a file from a folder to the root."""
        source_folder = create_test_folder(name="source_folder_for_file")
        # Create file inside the source_folder using its parentId
        file_in_folder = create_test_file(
            path="file_in_folder.txt",
            content="data in folder",
            parent_id=source_folder['id']
        )
        # Verify initial state
        assert file_in_folder['parentId'] == source_folder['id']
        assert file_in_folder['path'] == f"{source_folder['path']}/file_in_folder.txt"


        move_payload = {'newParentId': ""} # Empty string for root
        response = requests.put(
            f"{base_url}/files/{file_in_folder['id']}/move",
            json=move_payload,
            headers=admin_session
        )
        assert_status_code(response, 200)
        moved_file_data = response.json()

        assert moved_file_data['id'] == file_in_folder['id']
        assert moved_file_data.get('parentId', '') == "" # Should be at root
        assert moved_file_data['path'] == "file_in_folder.txt" # Path at root is just the name

    # --- Tests for Folder Move Endpoint ---

    def test_move_folder_to_another_folder(base_url, admin_session, create_test_folder, create_test_file):
        """Test moving a folder (with content) into another folder."""
        destination_parent_folder = create_test_folder(name="dest_parent_for_folder")
        folder_to_move = create_test_folder(name="folder_being_moved")

        # Create a file inside the folder_to_move to check its path update
        child_file_name = "child_in_moved_folder.txt"
        child_file = create_test_file(
            path=child_file_name,
            parent_id=folder_to_move['id'],
            content="child content"
        )
        original_child_path = f"{folder_to_move['path']}/{child_file_name}"
        assert child_file['path'] == original_child_path

        move_payload = {'newParentId': destination_parent_folder['id']}
        response = requests.put(
            f"{base_url}/folders/{folder_to_move['id']}/move",
            json=move_payload,
            headers=admin_session
        )
        assert_status_code(response, 200)
        moved_folder_data = response.json()

        assert moved_folder_data['id'] == folder_to_move['id']
        assert moved_folder_data['parentId'] == destination_parent_folder['id']
        expected_moved_folder_path = f"{destination_parent_folder['path']}/{folder_to_move['original_name']}"
        assert moved_folder_data['path'] == expected_moved_folder_path

        # Verify child file's path has updated
        get_child_response = requests.get(f"{base_url}/files/{child_file['id']}", headers=admin_session)
        assert_status_code(get_child_response, 200)
        updated_child_file_data = get_child_response.json()

        assert updated_child_file_data['parentId'] == folder_to_move['id'] # Parent ID of child is still the moved folder
        expected_updated_child_path = f"{expected_moved_folder_path}/{child_file_name}"
        assert updated_child_file_data['path'] == expected_updated_child_path

    def test_move_folder_to_root(base_url, admin_session, create_test_folder):
        """Test moving a folder from a subfolder to the root."""
        parent_of_source = create_test_folder(name="parent_of_source_folder")
        folder_to_move = create_test_folder(name="folder_moving_to_root", parent_id=parent_of_source['id'])

        # Verify initial state
        assert folder_to_move['parentId'] == parent_of_source['id']
        assert folder_to_move['path'] == f"{parent_of_source['path']}/{folder_to_move['original_name']}"

        move_payload = {'newParentId': ""} # Empty string for root
        response = requests.put(
            f"{base_url}/folders/{folder_to_move['id']}/move",
            json=move_payload,
            headers=admin_session
        )
        assert_status_code(response, 200)
        moved_folder_data = response.json()

        assert moved_folder_data['id'] == folder_to_move['id']
        assert moved_folder_data.get('parentId', '') == "" # Should be at root
        assert moved_folder_data['path'] == folder_to_move['original_name'] # Path at root is its original name

    def test_move_folder_circular_into_itself(base_url, admin_session, create_test_folder):
        """Test attempting to move a folder into itself (should fail)."""
        folder = create_test_folder(name="self_move_folder")

        move_payload = {'newParentId': folder['id']}
        response = requests.put(
            f"{base_url}/folders/{folder['id']}/move",
            json=move_payload,
            headers=admin_session
        )
        # Expecting a client error (e.g., 400 Bad Request)
        assert response.status_code == 400, f"Expected 400, got {response.status_code}. Response: {response.text}"
        error_data = response.json()
        assert "cannot move a folder into itself" in error_data.get("error", "").lower()


    def test_move_folder_circular_into_child(base_url, admin_session, create_test_folder):
        """Test attempting to move a folder into one of its own children (should fail)."""
        parent_folder = create_test_folder(name="circ_parent_folder")
        child_folder = create_test_folder(name="circ_child_folder", parent_id=parent_folder['id'])

        move_payload = {'newParentId': child_folder['id']}
        response = requests.put(
            f"{base_url}/folders/{parent_folder['id']}/move", # Attempt to move parent into child
            json=move_payload,
            headers=admin_session
        )
        assert response.status_code == 400, f"Expected 400, got {response.status_code}. Response: {response.text}"
        error_data = response.json()
        assert "cannot move folder" in error_data.get("error", "").lower()
        assert "into itself or one of its subfolders" in error_data.get("error", "").lower()
