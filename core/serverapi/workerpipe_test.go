package serverapi_test

import (
	"context"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"testing"
	"time"

	"github.com/contenox/contenox/core/llmresolver"
	"github.com/contenox/contenox/core/modelprovider"
	"github.com/contenox/contenox/core/serverapi"
	"github.com/contenox/contenox/core/serverapi/dispatchapi"
	"github.com/contenox/contenox/core/serverapi/filesapi"
	"github.com/contenox/contenox/core/serverapi/indexapi"
	"github.com/contenox/contenox/core/serverapi/usersapi"
	"github.com/contenox/contenox/core/serverops"
	"github.com/contenox/contenox/core/serverops/vectors"
	"github.com/contenox/contenox/core/services/fileservice"
	"github.com/contenox/contenox/core/services/indexservice"
	"github.com/contenox/contenox/core/services/testingsetup"
	"github.com/contenox/contenox/core/services/userservice"
	"github.com/contenox/contenox/libs/libauth"
	"github.com/contenox/contenox/libs/libtestenv"
	"github.com/stretchr/testify/require"
)

func TestSystem_WorkerPipeline_ProcessesFileAndReturnsSearchResult(t *testing.T) {
	port := rand.Intn(16383) + 49152
	config := &serverops.Config{
		JWTExpiry:       "1h",
		JWTSecret:       "securecryptngkeysecurecryptngkey",
		EncryptionKey:   "securecryptngkeysecurecryptngkey",
		SigningKey:      "securecryptngkeysecurecryptngkey",
		EmbedModel:      "nomic-embed-text:latest",
		TasksModel:      "qwen2.5:0.5b",
		SecurityEnabled: "true",
	}

	testenv := testingsetup.New(context.Background(), serverops.NoopTracker{}).
		WithTriggerChan().
		WithServiceManager(config).
		WithDBConn("test").
		WithDBManager().
		WithPubSub().
		WithOllama().
		WithState().
		WithBackend().
		RunState().
		RunDownloadManager().
		WithDefaultUser().
		Build()
	defer testenv.Cleanup()
	if testenv.Err != nil {
		t.Fatal(testenv.Err)
	}
	embedder, err := testenv.NewEmbedder(config)
	if err != nil {
		log.Fatalf("initializing embedding pool failed: %v", err)
	}
	tastexec, err := testenv.NewExecRepo(config)
	if err != nil {
		log.Fatalf("initializing exec repo failed: %v", err)
	}
	require.NoError(t, testenv.AssignBackends(serverops.EmbedPoolID).Err)
	require.NoError(t, testenv.WaitForModel(config.TasksModel).Err)
	require.NoError(t, testenv.WaitForModel(config.EmbedModel).Err)

	provider, err := embedder.GetProvider(testenv.Ctx)
	if err != nil {
		log.Fatalf("initializing embedding provider failed: %v", err)
	}
	_, err = testenv.GetEmbedConnection(provider)
	if err != nil {
		log.Fatalf("initializing exec repo failed: %v", err)
	}
	uri, _, cleanup2, err := vectors.SetupLocalInstance(testenv.Ctx, "../../")
	defer cleanup2()
	if err != nil {
		t.Fatal(err)
	}
	config.VectorStoreURL = uri
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		// OK
	})

	fileService, err := testenv.NewFileservice(config)
	if err != nil {
		log.Fatalf("initializing file service failed: %v", err)
	}
	fileVectorizationJobCreator, err := testenv.NewFileVectorizationJobCreator(config)
	if err != nil {
		log.Fatalf("initializing file vectorization job creator failed: %v", err)
	}
	fileService = fileservice.WithActivityTracker(fileService, fileVectorizationJobCreator)
	filesapi.AddFileRoutes(mux, config, fileService)
	ctx := testenv.Ctx
	vectorStore, cleanup4, err := vectors.New(ctx, config.VectorStoreURL, vectors.Args{
		Timeout: 1 * time.Second,
		SearchArgs: vectors.SearchArgs{
			Epsilon: 0.1,
			Radius:  -1,
		},
	})
	if err != nil {
		log.Fatalf("initializing vector store failed: %v", err)
	}
	defer cleanup4()
	indexService, err := testenv.NewIndexService(config, vectorStore, embedder, tastexec)
	if err != nil {
		log.Fatalf("initializing index service failed: %v", err)
	}
	indexapi.AddIndexRoutes(mux, config, indexService)

	userService, err := testenv.NewUserservice(config)
	if err != nil {
		log.Fatalf("initializing user service failed: %v", err)
	}
	res, err := userService.Register(ctx, userservice.CreateUserRequest{
		Email:        serverops.DefaultAdminUser,
		FriendlyName: "Admin",
		Password:     "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	usersapi.AddAuthRoutes(mux, userService)
	ctx = context.WithValue(ctx, libauth.ContextTokenKey, res.Token)

	// sanity check
	client, err := llmresolver.Embed(ctx, llmresolver.EmbedRequest{
		ModelName: "nomic-embed-text:latest",
	}, modelprovider.ModelProviderAdapter(ctx, testenv.State()), llmresolver.Randomly)
	if err != nil {
		t.Fatalf("failed to resolve embed: %v", err)
	}

	dispatcher, err := testenv.NewDispatchService(config)
	if err != nil {
		t.Fatal(err)
	}
	dispatchapi.AddDispatchRoutes(mux, config, dispatcher)
	handler := serverapi.JWTMiddleware(config, mux)
	go func() {
		if err := http.ListenAndServe("0.0.0.0:"+fmt.Sprint(port), handler); err != nil {
			log.Fatal(err)
		}
	}()

	// ensure embedder is ready
	embedderProvider, err := embedder.GetProvider(ctx)
	if err != nil {
		t.Fatalf("failed to get embedder provider: %v", err)
	}
	if !embedderProvider.CanEmbed() {
		t.Fatalf("embedder not ready")
	}
	file := &fileservice.File{
		Name:        "updated.txt",
		ContentType: "text/plain; charset=utf-8",
		Data:        []byte("some demo text to be embedded"),
	}
	vectorData, err := client.Embed(ctx, string(file.Data))
	if err != nil {
		t.Fatalf("failed to embed file: %v", err)
	}
	vectorData32 := make([]float32, len(vectorData))

	// Iterate and cast each element
	for i, v := range vectorData {
		vectorData32[i] = float32(v)
	}

	t.Logf("Dimension of query vector generated in test: %d", len(vectorData32))
	// sanity-check 3
	require.Equal(t, 768, len(vectorData32), "Query vector dimension mismatch")
	t.Run("create a file should trigger vectorization", func(t *testing.T) {
		file, err = fileService.CreateFile(ctx, file)
		if err != nil {
			t.Fatalf("failed to create file: %v", err)
		}
		jobs, err := dispatcher.PendingJobs(ctx, nil)
		require.NoError(t, err, "failed to get pending jobs")
		for i, j := range jobs {
			t.Log(fmt.Sprintf("JOB %d: %s %v %v", i, j.TaskType, j.ID, j.RetryCount))
		}
		require.GreaterOrEqual(t, len(jobs), 1, "expected 1 pending job")
		require.Equal(t, "vectorize_text/plain; charset=utf-8", jobs[0].TaskType, "expected plaintext job")
		workerContainer, cleanup3, err := libtestenv.SetupLocalWorkerInstance(ctx, libtestenv.WorkerConfig{
			APIBaseURL:                  fmt.Sprintf("http://172.17.0.1:%d", port),
			WorkerEmail:                 serverops.DefaultAdminUser,
			WorkerPassword:              "test",
			WorkerLeaserID:              "my-worker-1",
			WorkerLeaseDurationSeconds:  2,
			WorkerRequestTimeoutSeconds: 2,
			WorkerType:                  "plaintext",
		})
		defer cleanup3()
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Second * 3)
		readCloser, err := workerContainer.Logs(ctx)
		require.NoError(t, err, "failed to get worker logs stream")
		defer readCloser.Close()

		logBytes, err := io.ReadAll(readCloser)
		if err != nil && err != io.EOF {
			t.Logf("Warning: failed to read all worker logs: %v", err)
		}
		t.Logf("WORKER LOGS:\n%s\n--- END WORKER LOGS ---", string(logBytes))
		jobs, err = dispatcher.PendingJobs(ctx, nil)
		found := -1
		for i, j := range jobs {
			if j.TaskType == "vectorize_text/plain; charset=utf-8" {
				found = i
			}
			t.Log(fmt.Sprintf("JOB %d: %s %v %v", i, j.TaskType, j.ID, j.RetryCount))
		}
		errText := ""
		if found != -1 {
			errText = fmt.Sprintf("expected 0 pending job for vectorize_text %v %v", *&jobs[found].RetryCount, *jobs[found])
		}
		require.Equal(t, -1, found, errText)

		results, err := vectorStore.Search(ctx, vectorData32, 10, 1, nil) // prior 10
		if err != nil {
			t.Fatalf("failed to search vector store: %v", err)
		}
		if len(results) == 0 {
			t.Fatalf("no results found")
		}
		if len(results) < 1 {
			t.Fatalf("expected at least one vector, got %d", len(results))
		}
		chunk, err := testenv.Store().GetChunkIndexByID(ctx, results[0].ID)
		if err != nil {
			t.Fatalf("failed to get chunk index by ID: %v", err)
		}
		if chunk.ResourceID != file.ID {
			t.Fatalf("expected file ID %s, got %s", file.ID, chunk.ResourceID)
		}
		resp, err := indexService.Search(ctx, &indexservice.SearchRequest{
			Query: "give me the file with the demo text",
			TopK:  10,
		})
		if err != nil {
			t.Fatal(err)
		}
		foundID := false
		for _, sr := range resp.Results {
			if sr.ID == file.ID {
				foundID = true
			}
		}
		if !foundID {
			t.Fatal("file was not found")
		}
	})
}
