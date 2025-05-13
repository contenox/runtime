package fileservice_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/js402/cate/core/serverops"
	"github.com/js402/cate/core/serverops/store"
	"github.com/js402/cate/core/services/fileservice"
	"github.com/js402/cate/libs/libdb"
)

func TestFileService(t *testing.T) {
	var cleanups []func()
	addCleanup := func(fn func()) {
		cleanups = append(cleanups, fn)
	}
	defer func() {
		for i := len(cleanups) - 1; i >= 0; i-- {
			cleanups[i]()
		}
	}()

	testRunCtx := context.Background()

	// Pass testRunCtx to setupFileServiceTestEnv
	_, fileService, dbCleanup := setupFileServiceTestEnv(testRunCtx, t)
	addCleanup(dbCleanup)

	t.Run("CreateFile", func(t *testing.T) {
		testFile := &fileservice.File{
			Name:        "test.txt", // This will be the name at root
			ContentType: "text/plain",
			Data:        []byte("test data"),
		}

		// Use the ctx from setupFileServiceTestEnv which has identity
		createdFile, err := fileService.CreateFile(testRunCtx, testFile)
		if err != nil {
			t.Fatalf("CreateFile failed: %v", err)
		}

		if createdFile.ID == "" {
			t.Error("Expected non-empty ID")
		}
		// For a root file, Path is just its name.
		if createdFile.Path != "test.txt" {
			t.Errorf("Expected path 'test.txt', got %s", createdFile.Path)
		}
		if createdFile.ContentType != "text/plain" {
			t.Errorf("Expected content type 'text/plain', got %s", createdFile.ContentType)
		}
		if createdFile.Size != int64(len(testFile.Data)) {
			t.Errorf("Expected size %d, got %d", len(testFile.Data), createdFile.Size)
		}
		if createdFile.ParentID != "" {
			t.Errorf("Expected ParentID '' for root file, got '%s'", createdFile.ParentID)
		}

		retrievedFile, err := fileService.GetFileByID(testRunCtx, createdFile.ID)
		if err != nil {
			t.Fatalf("GetFileByID failed: %v", err)
		}
		if !bytes.Equal(retrievedFile.Data, testFile.Data) {
			t.Error("Retrieved file data does not match original")
		}
	})

	t.Run("UpdateFile", func(t *testing.T) {
		originalFile := &fileservice.File{
			Name:        "update.txt",
			ContentType: "text/plain",
			Data:        []byte("original data"),
		}

		createdFile, err := fileService.CreateFile(testRunCtx, originalFile)
		if err != nil {
			t.Fatalf("CreateFile failed: %v", err)
		}
		if createdFile.Path != "update.txt" {
			t.Fatalf("CreateFile path expected 'update.txt', got '%s'", createdFile.Path)
		}

		newData := []byte("updated data")
		updateFile := &fileservice.File{
			ID:          createdFile.ID,
			Path:        "update.txt", // Path is not updated by UpdateFile
			ContentType: "text/plain",
			Data:        newData,
			Size:        int64(len(newData)), // Size should be updated automatically by CreateFile/UpdateFile from Data
		}
		updatedFile, err := fileService.UpdateFile(testRunCtx, updateFile)
		if err != nil {
			t.Fatalf("UpdateFile failed: %v", err)
		}

		if updatedFile.Path != "update.txt" {
			t.Errorf("Expected path 'update.txt' after update, got %s", updatedFile.Path)
		}
		if !bytes.Equal(updatedFile.Data, newData) {
			t.Error("Data not updated")
		}
		if updatedFile.Size != int64(len(newData)) {
			t.Errorf("Expected size %d, got %d", len(newData), updatedFile.Size)
		}

		retrievedFile, err := fileService.GetFileByID(testRunCtx, createdFile.ID)
		if err != nil {
			t.Fatalf("GetFileByID failed: %v", err)
		}
		if !bytes.Equal(retrievedFile.Data, newData) {
			t.Error("Retrieved data does not match updated data")
		}
	})

	t.Run("DeleteFile", func(t *testing.T) {
		fileName := "delete_" + uuid.NewString()[:4] + ".txt"
		testFile := &fileservice.File{
			Name:        fileName,
			ContentType: "text/plain",
			Data:        []byte("data to delete"),
		}

		createdFile, err := fileService.CreateFile(testRunCtx, testFile)
		if err != nil {
			t.Fatalf("CreateFile failed: %v", err)
		}

		err = fileService.DeleteFile(testRunCtx, createdFile.ID)
		if err != nil {
			t.Fatalf("DeleteFile failed: %v", err)
		}

		_, err = fileService.GetFileByID(testRunCtx, createdFile.ID)
		if err == nil {
			t.Error("Expected error when retrieving deleted file, got nil")
		} else if !errors.Is(err, libdb.ErrNotFound) && !strings.Contains(strings.ToLower(err.Error()), "not found") {
			// Check if libdb.ErrNotFound is wrapped or if it's a generic not found.
			t.Errorf("Expected 'not found' error, got: %v", err)
		}
	})

	t.Run("CreateFolder_AtRoot", func(t *testing.T) {
		name := "test_folder_at_root_" + uuid.NewString()[:4]
		folder, err := fileService.CreateFolder(testRunCtx, "", name)
		if err != nil {
			t.Fatalf("CreateFolder_AtRoot failed for '%s': %v", name, err)
		}

		if folder.ID == "" {
			t.Error("Expected non-empty folder ID")
		}
		if folder.Path != name {
			t.Errorf("Expected path '%s', got '%s'", name, folder.Path)
		}
		if folder.ParentID != "" {
			t.Errorf("Expected ParentID '' for root folder, got '%s'", folder.ParentID)
		}
	})

	t.Run("RenameFile", func(t *testing.T) {
		oldName := "oldname_" + uuid.NewString()[:4] + ".txt"
		testFile := &fileservice.File{
			Name:        oldName,
			ContentType: "text/plain",
			Data:        []byte("data"),
		}

		createdFile, err := fileService.CreateFile(testRunCtx, testFile)
		if err != nil {
			t.Fatalf("CreateFile failed: %v", err)
		}

		newName := "newname_" + uuid.NewString()[:4] + ".txt"
		renamedFile, err := fileService.RenameFile(testRunCtx, createdFile.ID, newName)
		if err != nil {
			t.Fatalf("RenameFile failed: %v", err)
		}

		if renamedFile.Path != newName {
			t.Errorf("Expected path '%s' after rename, got '%s'", newName, renamedFile.Path)
		}

		retrievedFile, err := fileService.GetFileByID(testRunCtx, createdFile.ID)
		if err != nil {
			t.Fatalf("GetFileByID failed: %v", err)
		}
		if retrievedFile.Path != newName {
			t.Errorf("Retrieved file path is '%s', expected '%s'", retrievedFile.Path, newName)
		}
	})

	t.Run("CreateAndGetFolder_folder1_variant", func(t *testing.T) {
		folderPath := "folder1_variant_" + uuid.NewString()[:4]
		folder, err := fileService.CreateFolder(testRunCtx, "", folderPath)
		if err != nil {
			t.Fatalf("CreateFolder failed for '%s': %v", folderPath, err)
		}
		storedFolder, err := fileService.GetFolderByID(testRunCtx, folder.ID)
		if err != nil {
			t.Fatalf("GetFolderByID failed for '%s': %v", folderPath, err)
		}
		if storedFolder.ID != folder.ID {
			t.Fatalf("Folder ID mismatch: expected '%s', got '%s'", folder.ID, storedFolder.ID)
		}
		if storedFolder.Path != folderPath {
			t.Errorf("Expected path '%s', got '%s'", folderPath, storedFolder.Path)
		}
	})

	t.Run("CreateMiniTree", func(t *testing.T) {
		folder1Name := "folder1_minitree_" + uuid.NewString()[:4] // Name for root folder
		folder1, err := fileService.CreateFolder(testRunCtx, "", folder1Name)
		if err != nil {
			t.Fatalf("CreateFolder (root of MiniTree '%s') failed: %v", folder1Name, err)
		}
		retrievedFolder1, err := fileService.GetFolderByID(testRunCtx, folder1.ID)
		if err != nil {
			t.Fatalf("GetFolderByID (root of MiniTree '%s') failed: %v", folder1Name, err)
		}
		if retrievedFolder1.Path != folder1Name { // Path is just name for root folder
			t.Errorf("Expected path for folder1 '%s', got '%s'", folder1Name, retrievedFolder1.Path)
		}
		if retrievedFolder1.ParentID != "" {
			t.Errorf("Expected ParentID '' for folder1, got '%s'", retrievedFolder1.ParentID)
		}

		folder2Name := "folder2" // Name for subfolder
		folder2, err := fileService.CreateFolder(testRunCtx, folder1.ID, folder2Name)
		if err != nil {
			t.Fatalf("CreateFolder (child '%s' in MiniTree) failed: %v", folder2Name, err)
		}
		storedFolder2, err := fileService.GetFolderByID(testRunCtx, folder2.ID)
		if err != nil {
			t.Fatalf("GetFolderByID (child '%s' in MiniTree) failed: %v", folder2Name, err)
		}
		expectedPathForFolder2 := folder1Name + "/" + folder2Name
		if storedFolder2.Path != expectedPathForFolder2 {
			t.Fatalf("MiniTree folder2 path mismatch, expected '%s', got '%s'", expectedPathForFolder2, storedFolder2.Path)
		}
		if storedFolder2.ParentID != folder1.ID {
			t.Fatalf("MiniTree folder2 ParentID mismatch, expected '%s', got '%s'", folder1.ID, storedFolder2.ParentID)
		}
	})

	t.Run("RenameFolder", func(t *testing.T) {
		oldFolderName := "old_folder_rename_test_" + uuid.NewString()[:4]
		folder, err := fileService.CreateFolder(testRunCtx, "", oldFolderName)
		if err != nil {
			t.Fatalf("CreateFolder failed for '%s': %v", oldFolderName, err)
		}
		file1Name := "file1.txt"
		// For CreateFile, Path is treated as the name if ParentID is given.
		createdFile1, err := fileService.CreateFile(testRunCtx, &fileservice.File{
			Name:        file1Name,
			ParentID:    folder.ID,
			ContentType: "text/plain",
			Data:        []byte("data1"),
		})
		if err != nil {
			t.Fatalf("CreateFile failed for file1: %v", err)
		}

		subFolderName := "sub"
		subFolder, err := fileService.CreateFolder(testRunCtx, folder.ID, subFolderName)
		if err != nil {
			t.Fatalf("CreateFolder for subfolder '%s' failed: %v", subFolderName, err)
		}

		file2Name := "file2.txt"
		createdFile2, err := fileService.CreateFile(testRunCtx, &fileservice.File{
			Name:        file2Name,
			ParentID:    subFolder.ID,
			ContentType: "text/plain",
			Data:        []byte("data2"),
		})
		if err != nil {
			t.Fatalf("CreateFile failed for file2: %v", err)
		}

		newFolderName := "new_folder_renamed_" + uuid.NewString()[:4]
		renamedFolder, err := fileService.RenameFolder(testRunCtx, folder.ID, newFolderName)
		if err != nil {
			t.Fatalf("RenameFolder failed: %v", err)
		}

		if renamedFolder.Path != newFolderName { // Path for a root folder is its name
			t.Errorf("Folder path expected '%s', got '%s'", newFolderName, renamedFolder.Path)
		}
		if renamedFolder.ParentID != "" {
			t.Errorf("Renamed root folder ParentID expected '', got '%s'", renamedFolder.ParentID)
		}

		retrievedFile1, err := fileService.GetFileByID(testRunCtx, createdFile1.ID)
		if err != nil {
			t.Fatalf("GetFileByID failed for file1: %v", err)
		}
		expectedPath1 := newFolderName + "/" + file1Name
		if retrievedFile1.Path != expectedPath1 {
			t.Errorf("File1 path expected '%s', got '%s'", expectedPath1, retrievedFile1.Path)
		}

		retrievedSubFolder, err := fileService.GetFolderByID(testRunCtx, subFolder.ID)
		if err != nil {
			t.Fatalf("GetFolderByID for subfolder failed: %v", err)
		}
		expectedSubFolderPath := newFolderName + "/" + subFolderName
		if retrievedSubFolder.Path != expectedSubFolderPath {
			t.Errorf("SubFolder path expected '%s', got '%s'", expectedSubFolderPath, retrievedSubFolder.Path)
		}

		retrievedFile2, err := fileService.GetFileByID(testRunCtx, createdFile2.ID)
		if err != nil {
			t.Fatalf("GetFileByID failed for file2: %v", err)
		}
		expectedPath2 := newFolderName + "/" + subFolderName + "/" + file2Name
		if retrievedFile2.Path != expectedPath2 {
			t.Errorf("File2 path expected '%s', got '%s'", expectedPath2, retrievedFile2.Path)
		}
	})

	t.Run("ListAllPaths", func(t *testing.T) {
		// Create unique items for this specific test to avoid relying on external state
		baseName := "listtest_" + uuid.NewString()[:4]
		folder1Name := baseName + "_folder1"
		folder2Name := "folder2_in_list" // simple name for child
		file1Name := "file1_in_list.txt"
		file2InFolder1Name := "file2_in_folder1.txt"
		rootFile1Name := baseName + "_rootfile1.txt"
		rootFile2Name := baseName + "_rootfile2.txt"

		// Create items
		folder1, err := fileService.CreateFolder(testRunCtx, "", folder1Name)
		if err != nil {
			t.Fatalf("ListAllPaths: CreateFolder '%s' failed: %v", folder1Name, err)
		}
		folder2, err := fileService.CreateFolder(testRunCtx, folder1.ID, folder2Name)
		if err != nil {
			t.Fatalf("ListAllPaths: CreateFolder '%s' in '%s' failed: %v", folder2Name, folder1Name, err)
		}
		_, err = fileService.CreateFile(testRunCtx, &fileservice.File{Name: file1Name, ParentID: folder2.ID, ContentType: "text/plain", Data: []byte("data1")})
		if err != nil {
			t.Fatalf("ListAllPaths: CreateFile '%s' in '%s' failed: %v", file1Name, folder2.Path, err)
		}
		_, err = fileService.CreateFile(testRunCtx, &fileservice.File{Name: file2InFolder1Name, ParentID: folder1.ID, ContentType: "text/plain", Data: []byte("data_in_folder1")})
		if err != nil {
			t.Fatalf("ListAllPaths: CreateFile '%s' in '%s' failed: %v", file2InFolder1Name, folder1Name, err)
		}
		_, err = fileService.CreateFile(testRunCtx, &fileservice.File{Name: rootFile1Name, ContentType: "text/plain", Data: []byte("data_root1")})
		if err != nil {
			t.Fatalf("ListAllPaths: CreateFile '%s' failed: %v", rootFile1Name, err)
		}
		_, err = fileService.CreateFile(testRunCtx, &fileservice.File{Name: rootFile2Name, ContentType: "text/plain", Data: []byte("data_root2")})
		if err != nil {
			t.Fatalf("ListAllPaths: CreateFile '%s' failed: %v", rootFile2Name, err)
		}

		// Test listing root
		rootFiles, err := fileService.GetFilesByPath(testRunCtx, "")
		if err != nil {
			t.Fatalf("ListAllPaths: GetFilesByPath for root failed: %v", err)
		}

		// Check only for the specific root items created in this test
		expectedRootItemsThisTest := map[string]bool{
			folder1Name:   true,
			rootFile1Name: true,
			rootFile2Name: true,
		}
		foundCount := 0
		for _, f := range rootFiles {
			if expectedRootItemsThisTest[f.Path] {
				foundCount++
			}
		}
		if foundCount != len(expectedRootItemsThisTest) {
			t.Errorf("ListAllPaths: Expected to find %d specific root items from this test, found %d matching. All listed: %v", len(expectedRootItemsThisTest), foundCount, filesToPaths(rootFiles))
		}
		for path := range expectedRootItemsThisTest {
			found := false
			for _, rf := range rootFiles {
				if rf.Path == path {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("ListAllPaths: Expected specific root item '%s' not found. All listed: %v", path, filesToPaths(rootFiles))
			}
		}

		// Test listing files within folder1
		filesInFolder1, err := fileService.GetFilesByPath(testRunCtx, folder1Name)
		if err != nil {
			t.Fatalf("ListAllPaths: GetFilesByPath for '%s' failed: %v", folder1Name, err)
		}
		expectedPathsInFolder1 := map[string]bool{
			// Paths are absolute from root
			folder1Name + "/" + folder2Name:        true,
			folder1Name + "/" + file2InFolder1Name: true,
		}
		actualPathsInFolder1 := make(map[string]bool)
		for _, f := range filesInFolder1 {
			actualPathsInFolder1[f.Path] = true
		}

		if len(filesInFolder1) != len(expectedPathsInFolder1) {
			t.Errorf("ListAllPaths: Expected %d items in folder '%s', got %d. Expected: %v, Got: %v",
				len(expectedPathsInFolder1), folder1Name, len(filesInFolder1), getKeys(expectedPathsInFolder1), filesToPaths(filesInFolder1))
		}
		for expectedPath := range expectedPathsInFolder1 {
			if !actualPathsInFolder1[expectedPath] {
				t.Errorf("ListAllPaths: Expected item '%s' in folder '%s' not found. Listed: %v", expectedPath, folder1Name, filesToPaths(filesInFolder1))
			}
		}
	})

	t.Run("RenameFile_ConflictWithExistingFile", func(t *testing.T) {
		conflictName := "conflict_file_" + uuid.NewString()[:4] + ".txt"
		_, err := fileService.CreateFile(testRunCtx, &fileservice.File{Name: conflictName, ContentType: "text/plain", Data: []byte("existing")})
		if err != nil {
			t.Fatalf("CreateFile failed for existing file: %v", err)
		}

		originalName := "original_for_conflict_" + uuid.NewString()[:4] + ".txt"
		createdFile, err := fileService.CreateFile(testRunCtx, &fileservice.File{Name: originalName, ContentType: "text/plain", Data: []byte("to be renamed")})
		if err != nil {
			t.Fatalf("CreateFile failed for file to rename: %v", err)
		}

		_, err = fileService.RenameFile(testRunCtx, createdFile.ID, conflictName)
		if err == nil {
			t.Errorf("Expected error when renaming to existing file path '%s', got nil", conflictName)
		} else if !strings.Contains(err.Error(), "already exists") && !strings.Contains(err.Error(), "unique constraint") {
			t.Logf("RenameFile_ConflictWithExistingFile: Received error as expected: %v", err)
		}
	})

	t.Run("RenameFolder_ConflictWithExistingFolder", func(t *testing.T) {
		existingFolderName := "existing_folder_for_conflict_" + uuid.NewString()[:4]
		_, err := fileService.CreateFolder(testRunCtx, "", existingFolderName)
		if err != nil {
			t.Fatalf("CreateFolder failed for existing_folder: %v", err)
		}

		folderToRenameName := "folder_to_rename_for_conflict_" + uuid.NewString()[:4]
		folderToRename, err := fileService.CreateFolder(testRunCtx, "", folderToRenameName)
		if err != nil {
			t.Fatalf("CreateFolder failed for folder_to_rename: %v", err)
		}

		_, err = fileService.RenameFolder(testRunCtx, folderToRename.ID, existingFolderName)
		if err == nil {
			t.Errorf("Expected error when renaming to existing folder path '%s', got nil", existingFolderName)
		} else if !strings.Contains(err.Error(), "already exists") && !strings.Contains(err.Error(), "unique constraint") {
			t.Logf("RenameFolder_ConflictWithExistingFolder: Received error as expected: %v", err)
		}
	})

	//
	// --- UN-NESTED MoveFile and MoveFolder tests ---
	//
	t.Run("MoveFile_Simple_RootToSubfolder", func(t *testing.T) {
		folderName := "move_target_folder_" + uuid.NewString()[:4]
		targetFolder, err := fileService.CreateFolder(testRunCtx, "", folderName)
		if err != nil {
			t.Fatalf("Failed to create target folder: %v", err)
		}

		fileName := "move_me_" + uuid.NewString()[:4] + ".txt"
		fileToMove, err := fileService.CreateFile(testRunCtx, &fileservice.File{Name: fileName, ContentType: "text/plain", Data: []byte("move data")})
		if err != nil {
			t.Fatalf("Failed to create file to move: %v", err)
		}
		if fileToMove.ParentID != "" { // Check it's at root initially
			t.Fatalf("File to move initial ParentID unexpected: got '%s', want ''", fileToMove.ParentID)
		}

		movedFile, err := fileService.MoveFile(testRunCtx, fileToMove.ID, targetFolder.ID)
		if err != nil {
			t.Fatalf("MoveFile failed: %v", err)
		}

		if movedFile.ParentID != targetFolder.ID {
			t.Errorf("MovedFile ParentID incorrect: got '%s', want '%s'", movedFile.ParentID, targetFolder.ID)
		}
		expectedPath := folderName + "/" + fileName
		if movedFile.Path != expectedPath {
			t.Errorf("MovedFile Path incorrect: got '%s', want '%s'", movedFile.Path, expectedPath)
		}

		retrievedFile, err := fileService.GetFileByID(testRunCtx, fileToMove.ID)
		if err != nil {
			t.Fatalf("GetFileByID for moved file failed: %v", err)
		}
		if retrievedFile.ParentID != targetFolder.ID {
			t.Errorf("Retrieved moved file ParentID incorrect: got '%s', want '%s'", retrievedFile.ParentID, targetFolder.ID)
		}
		if retrievedFile.Path != expectedPath {
			t.Errorf("Retrieved moved file Path incorrect: got '%s', want '%s'", retrievedFile.Path, expectedPath)
		}
	})

	t.Run("MoveFile_ToRoot", func(t *testing.T) {
		sourceFolderName := "move_source_folder_" + uuid.NewString()[:4]
		sourceFolder, err := fileService.CreateFolder(testRunCtx, "", sourceFolderName)
		if err != nil {
			t.Fatalf("Failed to create source folder: %v", err)
		}

		fileName := "move_me_to_root_" + uuid.NewString()[:4] + ".txt"
		fileToMove, err := fileService.CreateFile(testRunCtx, &fileservice.File{Name: fileName, ParentID: sourceFolder.ID, ContentType: "text/plain", Data: []byte("to root")})
		if err != nil {
			t.Fatalf("Failed to create file in subfolder: %v", err)
		}
		if fileToMove.ParentID != sourceFolder.ID { // Check it's in subfolder initially
			t.Fatalf("File to move initial ParentID unexpected: got '%s', want '%s'", fileToMove.ParentID, sourceFolder.ID)
		}
		initialPath := sourceFolderName + "/" + fileName
		if fileToMove.Path != initialPath {
			t.Fatalf("File to move initial Path unexpected: got '%s', want '%s'", fileToMove.Path, initialPath)
		}

		movedFile, err := fileService.MoveFile(testRunCtx, fileToMove.ID, "") // "" for root
		if err != nil {
			t.Fatalf("MoveFile to root failed: %v", err)
		}

		if movedFile.ParentID != "" {
			t.Errorf("MovedFile ParentID incorrect: got '%s', want ''", movedFile.ParentID)
		}
		if movedFile.Path != fileName { // Path at root is just the name
			t.Errorf("MovedFile Path incorrect: got '%s', want '%s'", movedFile.Path, fileName)
		}
	})

	t.Run("MoveFile_NameCollision", func(t *testing.T) {
		folderName := "move_collision_folder_" + uuid.NewString()[:4]
		targetFolder, err := fileService.CreateFolder(testRunCtx, "", folderName)
		if err != nil {
			t.Fatalf("Failed to create target folder: %v", err)
		}

		collidingFileName := "iamhere_" + uuid.NewString()[:4] + ".txt" // Unique name for this test
		_, err = fileService.CreateFile(testRunCtx, &fileservice.File{Name: collidingFileName, ParentID: targetFolder.ID, ContentType: "text/plain", Data: []byte("existing")})
		if err != nil {
			t.Fatalf("Failed to create existing file in target: %v", err)
		}

		// File to move, created at root with the same name as the one in targetFolder
		fileToMove, err := fileService.CreateFile(testRunCtx, &fileservice.File{Name: collidingFileName, ContentType: "text/plain", Data: []byte("original")})
		if err != nil {
			t.Fatalf("Failed to create file to move: %v", err)
		}

		_, err = fileService.MoveFile(testRunCtx, fileToMove.ID, targetFolder.ID)
		if err == nil {
			t.Error("MoveFile expected to fail due to name collision, but it succeeded")
		} else if !strings.Contains(err.Error(), "already exists") && !strings.Contains(err.Error(), "unique constraint") {
			t.Errorf("MoveFile error message unexpected for collision: got '%v', expected 'already exists' or similar", err)
		}
	})

	t.Run("MoveFolder_Simple_RootToSubfolder", func(t *testing.T) {
		parentFolderName := "move_parent_F_" + uuid.NewString()[:4]
		targetParentFolder, err := fileService.CreateFolder(testRunCtx, "", parentFolderName)
		if err != nil {
			t.Fatalf("Failed to create target parent folder: %v", err)
		}

		folderToMoveName := "folder_to_move_" + uuid.NewString()[:4]
		folderToMove, err := fileService.CreateFolder(testRunCtx, "", folderToMoveName)
		if err != nil {
			t.Fatalf("Failed to create folder to move: %v", err)
		}
		childFileName := "child_" + uuid.NewString()[:4] + ".txt"
		childFileInFolderToMove, err := fileService.CreateFile(testRunCtx, &fileservice.File{Name: childFileName, ParentID: folderToMove.ID, ContentType: "text/plain", Data: []byte("child data")})
		if err != nil {
			t.Fatalf("Failed to create child file in folder to move: %v", err)
		}

		movedFolder, err := fileService.MoveFolder(testRunCtx, folderToMove.ID, targetParentFolder.ID)
		if err != nil {
			t.Fatalf("MoveFolder failed: %v", err)
		}

		if movedFolder.ParentID != targetParentFolder.ID {
			t.Errorf("MovedFolder ParentID incorrect: got '%s', want '%s'", movedFolder.ParentID, targetParentFolder.ID)
		}
		expectedMovedFolderPath := parentFolderName + "/" + folderToMoveName
		if movedFolder.Path != expectedMovedFolderPath {
			t.Errorf("MovedFolder Path incorrect: got '%s', want '%s'", movedFolder.Path, expectedMovedFolderPath)
		}

		// Verify child file path and parent
		retrievedChild, err := fileService.GetFileByID(testRunCtx, childFileInFolderToMove.ID)
		if err != nil {
			t.Fatalf("Failed to get child file by ID after move: %v", err)
		}
		expectedChildPath := expectedMovedFolderPath + "/" + childFileName
		if retrievedChild.Path != expectedChildPath {
			t.Errorf("Child file path after move incorrect: got '%s', want '%s'", retrievedChild.Path, expectedChildPath)
		}
		if retrievedChild.ParentID != movedFolder.ID { // Parent should be the ID of the moved folder
			t.Errorf("Child file ParentID after move incorrect: got '%s', want '%s'", retrievedChild.ParentID, movedFolder.ID)
		}
	})

	t.Run("MoveFolder_ToRoot", func(t *testing.T) {
		sourceFolderName := "move_F_source_" + uuid.NewString()[:4]
		sourceFolder, err := fileService.CreateFolder(testRunCtx, "", sourceFolderName)
		if err != nil {
			t.Fatalf("Failed to create source folder: %v", err)
		}

		folderToMoveName := "move_me_F_to_root_" + uuid.NewString()[:4]
		folderToMove, err := fileService.CreateFolder(testRunCtx, sourceFolder.ID, folderToMoveName)
		if err != nil {
			t.Fatalf("Failed to create folder in subfolder: %v", err)
		}

		movedFolder, err := fileService.MoveFolder(testRunCtx, folderToMove.ID, "") // "" for root
		if err != nil {
			t.Fatalf("MoveFolder to root failed: %v", err)
		}

		if movedFolder.ParentID != "" {
			t.Errorf("MovedFolder ParentID incorrect for root: got '%s', want ''", movedFolder.ParentID)
		}
		if movedFolder.Path != folderToMoveName { // Path at root is just its name
			t.Errorf("MovedFolder Path incorrect: got '%s', want '%s'", movedFolder.Path, folderToMoveName)
		}
	})

	t.Run("MoveFolder_CircularDependency_IntoItself", func(t *testing.T) {
		folderName := "move_F_self_" + uuid.NewString()[:4]
		folder, err := fileService.CreateFolder(testRunCtx, "", folderName)
		if err != nil {
			t.Fatalf("Failed to create folder: %v", err)
		}

		_, err = fileService.MoveFolder(testRunCtx, folder.ID, folder.ID)
		if err == nil {
			t.Error("MoveFolder expected to fail (move into self), but succeeded")
		} else if !strings.Contains(err.Error(), "into itself") {
			t.Errorf("MoveFolder error message unexpected for self-move: got '%v'", err)
		}
	})

	t.Run("MoveFolder_CircularDependency_IntoChild", func(t *testing.T) {
		parentFolderName := "move_F_parent_circ_" + uuid.NewString()[:4]
		parentFolder, err := fileService.CreateFolder(testRunCtx, "", parentFolderName)
		if err != nil {
			t.Fatalf("Failed to create parent folder: %v", err)
		}

		childFolderName := "move_F_child_circ_" + uuid.NewString()[:4]
		childFolder, err := fileService.CreateFolder(testRunCtx, parentFolder.ID, childFolderName)
		if err != nil {
			t.Fatalf("Failed to create child folder: %v", err)
		}

		_, err = fileService.MoveFolder(testRunCtx, parentFolder.ID, childFolder.ID)
		if err == nil {
			t.Error("MoveFolder expected to fail (move into child), but succeeded")
		} else if !strings.Contains(err.Error(), "into itself or one of its subfolders") {
			t.Errorf("MoveFolder error message unexpected for circular dependency: got '%v'", err)
		}
	})

	t.Run("MoveFolder_NameCollision", func(t *testing.T) {
		targetParentName := "mf_collision_parent_" + uuid.NewString()[:4]
		targetParent, err := fileService.CreateFolder(testRunCtx, "", targetParentName)
		if err != nil {
			t.Fatalf("Failed to create target parent folder: %v", err)
		}

		collidingName := "iamfolderhere_" + uuid.NewString()[:4] // Unique name for this test
		_, err = fileService.CreateFolder(testRunCtx, targetParent.ID, collidingName)
		if err != nil {
			t.Fatalf("Failed to create existing folder in target parent: %v", err)
		}

		// Folder to move, created at root with the same name
		folderToMove, err := fileService.CreateFolder(testRunCtx, "", collidingName)
		if err != nil {
			t.Fatalf("Failed to create folder to move: %v", err)
		}

		_, err = fileService.MoveFolder(testRunCtx, folderToMove.ID, targetParent.ID)
		if err == nil {
			t.Error("MoveFolder expected to fail due to name collision, but it succeeded")
		} else if !strings.Contains(err.Error(), "already exists") && !strings.Contains(err.Error(), "unique constraint") {
			t.Errorf("MoveFolder error message unexpected for name collision: got '%v'", err)
		}
	})
}

func filesToPaths(files []fileservice.File) []string {
	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = f.Path
	}
	return paths
}

func getKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func setupFileServiceTestEnv(ctx context.Context, t *testing.T) (libdb.DBManager, fileservice.Service, func()) {
	t.Helper()
	dbConn, _, dbCleanup, err := libdb.SetupLocalInstance(ctx, uuid.NewString(), "test", "test")
	if err != nil {
		t.Fatalf("failed to setup local database: %v", err)
	}

	dbInstance, err := libdb.NewPostgresDBManager(ctx, dbConn, store.Schema)
	if err != nil {
		t.Fatalf("failed to create new Postgres DB Manager: %v", err)
	}
	err = serverops.NewServiceManager(&serverops.Config{
		JWTExpiry:       "1h",
		SecurityEnabled: "false",
	})
	if err != nil {
		t.Fatalf("failed to create new Service Manager: %v", err)
	}
	fileService := fileservice.New(dbInstance, &serverops.Config{
		JWTExpiry:       "1h",
		SecurityEnabled: "false",
	})
	err = store.New(dbInstance.WithoutTransaction()).CreateUser(ctx, &store.User{
		Email:        serverops.DefaultAdminUser,
		ID:           uuid.NewString(),
		Subject:      serverops.DefaultAdminUser,
		FriendlyName: "Admin",
	})
	if err != nil {
		t.Fatalf("failed to create user: %v", err)
	}
	err = store.New(dbInstance.WithoutTransaction()).CreateAccessEntry(ctx, &store.AccessEntry{
		Identity:     serverops.DefaultAdminUser,
		ID:           uuid.NewString(),
		Resource:     serverops.DefaultServerGroup,
		ResourceType: serverops.DefaultServerGroup,
		Permission:   store.PermissionManage,
	})
	if err != nil {
		t.Fatalf("failed to create access entry: %v", err)
	}
	return dbInstance, fileService, dbCleanup
}
