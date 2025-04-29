package serverapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/js402/cate/core/llmembed"
	"github.com/js402/cate/core/llmresolver"
	"github.com/js402/cate/core/modelprovider"
	"github.com/js402/cate/core/serverapi"
	"github.com/js402/cate/core/serverapi/dispatchapi"
	"github.com/js402/cate/core/serverapi/filesapi"
	"github.com/js402/cate/core/serverapi/indexapi"
	"github.com/js402/cate/core/serverapi/usersapi"
	"github.com/js402/cate/core/serverops"
	"github.com/js402/cate/core/serverops/store"
	"github.com/js402/cate/core/serverops/vectors"
	"github.com/js402/cate/core/services/dispatchservice"
	"github.com/js402/cate/core/services/fileservice"
	"github.com/js402/cate/core/services/indexservice"
	"github.com/js402/cate/core/services/testingsetup"
	"github.com/js402/cate/core/services/userservice"
	"github.com/js402/cate/libs/libauth"
	"github.com/js402/cate/libs/libtestenv"
	"github.com/stretchr/testify/require"
)

func TestWorkerPipe(t *testing.T) {
	port := rand.Intn(16383) + 49152
	config := &serverops.Config{
		JWTExpiry:       "1h",
		JWTSecret:       "securecryptngkeysecurecryptngkey",
		EncryptionKey:   "securecryptngkeysecurecryptngkey",
		SigningKey:      "securecryptngkeysecurecryptngkey",
		EmbedModel:      "nomic-embed-text:latest",
		SecurityEnabled: "true",
	}

	ctx, state, dbInstance, cleanup := testingsetup.SetupTestEnvironment(t, config)
	defer cleanup()
	embedder, err := llmembed.New(ctx, config, dbInstance, state)
	if err != nil {
		log.Fatalf("initializing embedding pool failed: %v", err)
	}
	uri, _, cleanup2, err := vectors.SetupLocalInstance(ctx, "../../")
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

	fileService := fileservice.New(dbInstance, config)
	fileService = fileservice.WithActivityTracker(fileService, fileservice.NewFileVectorizationJobCreator(dbInstance))
	filesapi.AddFileRoutes(mux, config, fileService)

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
	indexService := indexservice.New(ctx, embedder, vectorStore)
	indexapi.AddIndexRoutes(mux, config, indexService)

	userService := userservice.New(dbInstance, config)
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

	require.Eventually(t, func() bool {
		currentState := state.Get(ctx)
		r, err := json.Marshal(currentState)
		if err != nil {
			t.Logf("error marshaling state: %v", err)
			return false
		}
		dst := &bytes.Buffer{}
		if err := json.Compact(dst, r); err != nil {
			t.Logf("error compacting JSON: %v", err)
			return false
		}
		return strings.Contains(string(r), `"name":"nomic-embed-text:latest"`)
	}, 2*time.Minute, 100*time.Millisecond)
	runtime := state.Get(ctx)
	url := ""
	backendID := ""
	found := false
	for _, runtimeState := range runtime {
		url = runtimeState.Backend.BaseURL
		backendID = runtimeState.Backend.ID
		for _, lmr := range runtimeState.PulledModels {
			if lmr.Model == "nomic-embed-text:latest" {
				found = true
				break
			}
		}
		if found {
			break
		}
	}
	if !found {
		t.Fatalf("nomic-embed-text:latest not found")
	}
	_ = url
	err = store.New(dbInstance.WithoutTransaction()).AssignBackendToPool(ctx, serverops.EmbedPoolID, backendID)
	if err != nil {
		t.Fatalf("failed to assign backend to pool: %v", err)
	}
	// sanity check
	client, err := llmresolver.ResolveEmbed(ctx, llmresolver.ResolveEmbedRequest{
		ModelName: "nomic-embed-text:latest",
	}, modelprovider.ModelProviderAdapter(ctx, state.Get(ctx)), llmresolver.ResolveRandomly)
	if err != nil {
		t.Fatalf("failed to resolve embed: %v", err)
	}
	// sanity-check 2
	backends, err := store.New(dbInstance.WithoutTransaction()).ListBackendsForPool(ctx, serverops.EmbedPoolID)
	if err != nil {
		t.Fatalf("failed to list backends for pool: %v", err)
	}
	found2 := false
	for _, backend2 := range backends {
		found2 = backend2.ID == backendID
		if found2 {
			break
		}
	}
	if !found2 {
		t.Fatalf("backend not found in pool")
	}
	dispatcher := dispatchservice.New(dbInstance, config)
	dispatchapi.AddDispatchRoutes(mux, config, dispatcher)
	handler := serverapi.JWTMiddleware(config, mux)
	go func() {
		if err := http.ListenAndServe("0.0.0.0:"+fmt.Sprint(port), handler); err != nil {
			log.Fatal(err)
		}
	}()

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
	time.Sleep(time.Second * 30)
	// ensure embedder is ready
	embedderProvider, err := embedder.GetProvider(ctx)
	if err != nil {
		t.Fatalf("failed to get embedder provider: %v", err)
	}
	if !embedderProvider.CanEmbed() {
		t.Fatalf("embedder not ready")
	}
	file := &fileservice.File{
		Path:        "updated.txt",
		ContentType: "text/plain",
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
		time.Sleep(time.Second * 10)
		readCloser, err := workerContainer.Logs(ctx)
		require.NoError(t, err, "failed to get worker logs stream")
		defer readCloser.Close()

		logBytes, err := io.ReadAll(readCloser)
		if err != nil && err != io.EOF {
			t.Logf("Warning: failed to read all worker logs: %v", err)
		}
		t.Logf("WORKER LOGS:\n%s\n--- END WORKER LOGS ---", string(logBytes))
		results, err := vectorStore.Search(ctx, vectorData32, 10, 1, nil)
		if err != nil {
			t.Fatalf("failed to search vector store: %v", err)
		}
		if len(results) == 0 {
			t.Fatalf("no results found")
		}
		if len(results) != 1 {
			t.Fatalf("expected 1 result, got %d", len(results))
		}
		if results[0].ID != file.ID {
			t.Fatalf("expected file ID %s, got %s", file.ID, results[0].ID)
		}
	})

}
