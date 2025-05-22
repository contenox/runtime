package fileservice_test

import (
	"context"
	"fmt"
	"math/rand"
	"testing"

	"github.com/google/uuid"
	"github.com/contenox/contenox/core/serverops"
	"github.com/contenox/contenox/core/serverops/store"
	"github.com/contenox/contenox/core/services/fileservice"
	"github.com/contenox/contenox/libs/libdb"
)

const benchmarkFileSize = 1024 * 1024

func setupFileServiceBenchmark(ctx context.Context, t testing.TB) (fileservice.Service, func()) {
	t.Helper()
	var cleanups []func()
	addCleanup := func(fn func()) {
		cleanups = append(cleanups, fn)
	}

	dbConn, _, dbCleanup, err := libdb.SetupLocalInstance(ctx, uuid.NewString(), "test", "test")
	if err != nil {
		t.Fatalf("failed to setup local database: %v", err)
	}
	addCleanup(dbCleanup)

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

	return fileService, func() {
		for i := len(cleanups) - 1; i >= 0; i-- {
			cleanups[i]()
		}
	}
}

func createFileForBenchmark(ctx context.Context, b *testing.B, fs fileservice.Service, parentID, fileName string, data []byte, contentType string) *fileservice.File {
	b.Helper()
	file := &fileservice.File{
		Name:        fileName,
		ContentType: contentType,
		Data:        data,
		ParentID:    parentID,
	}
	created, err := fs.CreateFile(ctx, file)
	if err != nil {
		b.Fatalf("CreateFile failed for parentID '%s', name '%s': %v", parentID, fileName, err)
	}
	return created
}

func showOpsPerSecond(b *testing.B, ops int64) {
	b.Helper()
	elapsed := b.Elapsed().Seconds()
	if elapsed > 0 {
		opsPerSec := float64(ops) / elapsed
		b.ReportMetric(opsPerSec, "ops/s")
	}
}

func createFolderForBenchmark(ctx context.Context, b *testing.B, fs fileservice.Service, parentID, folderName string) *fileservice.Folder {
	b.Helper()
	folder, err := fs.CreateFolder(ctx, parentID, folderName)
	if err != nil {
		b.Fatalf("CreateFolder failed for parentID '%s', name '%s': %v", parentID, folderName, err)
	}
	return folder
}

func generateBenchmarkData(size int) []byte {
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(rand.Intn(256))
	}
	return data
}

func BenchmarkCreateFile(b *testing.B) {
	ctx := context.Background()
	fileService, teardown := setupFileServiceBenchmark(ctx, b)
	defer teardown()

	fileData := generateBenchmarkData(benchmarkFileSize)

	b.SetBytes(int64(len(fileData)))
	for b.Loop() {
		fileName := fmt.Sprintf("bench_%s.txt", uuid.NewString())
		file := &fileservice.File{
			Name:        fileName,
			ParentID:    "",
			ContentType: "text/plain",
			Data:        fileData,
		}
		_, err := fileService.CreateFile(ctx, file)
		if err != nil {
			b.Fatalf("CreateFile failed: %v", err)
		}
	}
	showOpsPerSecond(b, int64(b.N))
}

func BenchmarkGetFileByID(b *testing.B) {
	ctx := context.Background()
	fileService, teardown := setupFileServiceBenchmark(ctx, b)
	defer teardown()

	const numFilesToPrepopulate = 100
	prePopulatedIDs := make([]string, 0, numFilesToPrepopulate)

	fileData := generateBenchmarkData(benchmarkFileSize)
	for i := range numFilesToPrepopulate {
		fileName := fmt.Sprintf("get_bench_%d.txt", i)
		createdFile := createFileForBenchmark(ctx, b, fileService, "", fileName, fileData, "text/plain")
		prePopulatedIDs = append(prePopulatedIDs, createdFile.ID)
	}
	targetFileData := generateBenchmarkData(benchmarkFileSize)
	createdFile := createFileForBenchmark(ctx, b, fileService, "", "get_me.txt", targetFileData, "text/plain")

	b.SetBytes(int64(len(targetFileData)))
	for b.Loop() {
		_, err := fileService.GetFileByID(ctx, createdFile.ID)
		if err != nil {
			b.Fatalf("GetFileByID failed: %v", err)
		}
	}
	showOpsPerSecond(b, int64(b.N))
}

func BenchmarkGetFilesByPath(b *testing.B) {
	ctx := context.Background()
	fileService, teardown := setupFileServiceBenchmark(ctx, b)
	defer teardown()

	fileData := generateBenchmarkData(benchmarkFileSize)
	folderName := "shared"
	folder := createFolderForBenchmark(ctx, b, fileService, "", folderName)

	createFileForBenchmark(ctx, b, fileService, folder.ID, "file1.txt", fileData, "text/plain")
	createFileForBenchmark(ctx, b, fileService, folder.ID, "file2.txt", fileData, "application/json")

	for b.Loop() {
		files, err := fileService.GetFilesByPath(ctx, folderName)
		if err != nil {
			b.Fatalf("GetFilesByPath failed for path '%s': %v", folderName, err)
		}
		if len(files) > 0 {
			var totalBytes int64
			for _, f := range files {
				totalBytes += f.Size
			}
			if totalBytes > 0 {
				b.SetBytes(totalBytes / int64(len(files)))
			} else {
				b.SetBytes(int64(len(fileData)))
			}
		} else {
			b.SetBytes(0)
		}
	}
	showOpsPerSecond(b, int64(b.N))
}

func BenchmarkUpdateFile(b *testing.B) {
	ctx := context.Background()
	fileService, teardown := setupFileServiceBenchmark(ctx, b)
	defer teardown()

	fileData := generateBenchmarkData(benchmarkFileSize)
	updatedData := generateBenchmarkData(benchmarkFileSize)
	createdFile := createFileForBenchmark(ctx, b, fileService, "", "update_me.txt", fileData, "text/plain")

	b.SetBytes(int64(len(updatedData)))
	for b.Loop() {
		updatedFile := &fileservice.File{
			ID:          createdFile.ID,
			Path:        createdFile.Path,
			ParentID:    createdFile.ParentID,
			ContentType: "application/json",
			Data:        updatedData,
		}
		_, err := fileService.UpdateFile(ctx, updatedFile)
		if err != nil {
			b.Fatalf("UpdateFile failed: %v", err)
		}
	}
	showOpsPerSecond(b, int64(b.N))
}

func BenchmarkDeleteFile(b *testing.B) {
	ctx := context.Background()
	fileService, teardown := setupFileServiceBenchmark(ctx, b)
	defer teardown()

	fileData := generateBenchmarkData(benchmarkFileSize)

	// Deletion doesn't directly process bytes in the same way as read/write
	for i := 0; b.Loop(); i++ {
		// Create a new file for each iteration
		fileToDelete := createFileForBenchmark(ctx, b, fileService, "", fmt.Sprintf("delete_me_%d.txt", i), fileData, "text/plain")
		err := fileService.DeleteFile(ctx, fileToDelete.ID)
		if err != nil {
			b.Fatalf("DeleteFile failed: %v", err)
		}
	}
	showOpsPerSecond(b, int64(b.N))
}

func BenchmarkCreateFolder(b *testing.B) {
	ctx := context.Background()
	fileService, teardown := setupFileServiceBenchmark(ctx, b)
	defer teardown()

	for b.Loop() {
		folderName := fmt.Sprintf("folder_%s", uuid.NewString())
		_, err := fileService.CreateFolder(ctx, "", folderName)
		if err != nil {
			b.Fatalf("CreateFolder failed: %v", err)
		}
	}
	showOpsPerSecond(b, int64(b.N))
}

func populateFolderTree(ctx context.Context, b *testing.B, fs fileservice.Service, parentID string, depth, breadth int, fileData []byte) {
	b.Helper()
	if depth <= 0 {
		for i := range breadth {
			fileName := fmt.Sprintf("file_leaf_d%d_i%d_%s.txt", depth, i, uuid.NewString()[:4])
			createFileForBenchmark(ctx, b, fs, parentID, fileName, fileData, "text/plain")
		}
		return
	}

	currentLevelFolderName := fmt.Sprintf("folder_d%d_u%s", depth, uuid.NewString()[:6])
	currentFolder := createFolderForBenchmark(ctx, b, fs, parentID, currentLevelFolderName)

	for i := range breadth {
		fileName := fmt.Sprintf("file_in_%s_idx%d.txt", currentLevelFolderName, i)
		createFileForBenchmark(ctx, b, fs, currentFolder.ID, fileName, fileData, "text/plain")
	}

	for j := range breadth {
		subFolderName := fmt.Sprintf("subfolder_of_%s_idx%d_u%s", currentLevelFolderName, j, uuid.NewString()[:4])
		createdSubFolder := createFolderForBenchmark(ctx, b, fs, currentFolder.ID, subFolderName)
		populateFolderTree(ctx, b, fs, createdSubFolder.ID, depth-1, breadth, fileData)
	}
}

func BenchmarkRenameFolder(b *testing.B) {
	ctx := context.Background()
	fileService, teardown := setupFileServiceBenchmark(ctx, b)
	defer teardown()
	fileData := generateBenchmarkData(1024)
	masterFolderName := "master_rename_folder_" + uuid.NewString()[:8]
	folderToRename := createFolderForBenchmark(ctx, b, fileService, "", masterFolderName)

	populateFolderTree(ctx, b, fileService, folderToRename.ID, 3, 4, fileData)

	originalNameForReset := folderToRename.Path

	for b.Loop() {
		newNameForThisIteration := fmt.Sprintf("new_renamed_state_%s", uuid.NewString()[:8])

		_, err := fileService.RenameFolder(ctx, folderToRename.ID, newNameForThisIteration)
		if err != nil {
			b.Fatalf("RenameFolder to %s failed: %v", newNameForThisIteration, err)
		}
		_, err = fileService.RenameFolder(ctx, folderToRename.ID, originalNameForReset)
		if err != nil {
			b.Fatalf("RenameFolder back to %s failed: %v", originalNameForReset, err)
		}
	}

	showOpsPerSecond(b, int64(b.N*2))
}

func BenchmarkListAllPaths(b *testing.B) {
	ctx := context.Background()
	fileService, teardown := setupFileServiceBenchmark(ctx, b)
	defer teardown()

	fileData := generateBenchmarkData(1024)

	// This will create a structure like:
	// /folder_d3_uXXXXXX
	//   /file_in_folder_d3...
	//   /subfolder_of_folder_d3...
	//     /folder_d2_uYYYYYY
	//       ...
	populateFolderTree(ctx, b, fileService, "", 3, 4, fileData)
	// Also create some direct root items to ensure GetFilesByPath("") lists them.
	createFileForBenchmark(ctx, b, fileService, "", "a_root_file.txt", fileData, "text/plain")
	createFolderForBenchmark(ctx, b, fileService, "", "a_root_folder")

	for b.Loop() {
		_, err := fileService.GetFilesByPath(ctx, "")
		if err != nil {
			b.Fatalf("GetFilesByPath(\"\") (ListAllPaths) failed: %v", err)
		}
	}
	showOpsPerSecond(b, int64(b.N))
}

func BenchmarkCreateFileParallel(b *testing.B) {
	ctx := context.Background()
	fileService, teardown := setupFileServiceBenchmark(ctx, b)
	defer teardown()

	fileData := generateBenchmarkData(benchmarkFileSize)
	b.ResetTimer()
	b.SetBytes(int64(len(fileData)))

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			fileName := fmt.Sprintf("bench_parallel_%s.txt", uuid.NewString())
			file := &fileservice.File{
				Name:        fileName,
				ParentID:    "", // Explicitly root
				ContentType: "text/plain",
				Data:        fileData,
			}
			_, err := fileService.CreateFile(ctx, file)
			if err != nil {
				b.Errorf("CreateFile in parallel failed: %v", err)
				return
			}
		}
	})
}
