package serverapi_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/js402/cate/core/llmembed"
	"github.com/js402/cate/core/llmresolver"
	"github.com/js402/cate/core/modelprovider"
	"github.com/js402/cate/core/serverapi/filesapi"
	"github.com/js402/cate/core/serverapi/indexapi"
	"github.com/js402/cate/core/serverops"
	"github.com/js402/cate/core/serverops/store"
	"github.com/js402/cate/core/serverops/vectors"
	"github.com/js402/cate/core/services/fileservice"
	"github.com/js402/cate/core/services/indexservice"
	"github.com/js402/cate/core/services/testingsetup"
	"github.com/js402/cate/libs/libtestenv"
	"github.com/stretchr/testify/require"
)

func TestWorkerPipe(t *testing.T) {
	port := fmt.Sprintf(":%d", rand.Intn(16383)+49152)
	config := &serverops.Config{
		JWTExpiry:  "1h",
		EmbedModel: "all-minilm:33m",
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
		// TODO
	})
	if err != nil {
		log.Fatalf("initializing vector store failed: %v", err)
	}
	defer cleanup4()
	indexService := indexservice.New(ctx, embedder, vectorStore)
	indexapi.AddIndexRoutes(mux, config, indexService)

	_, cleanup3, err := libtestenv.SetupLocalWorkerInstance(ctx, libtestenv.WorkerConfig{
		APIBaseURL:                  fmt.Sprintf("127.0.0.0:%s", port),
		WorkerEmail:                 serverops.DefaultAdminUser,
		WorkerPassword:              "",
		WorkerLeaserID:              "my-worker-1",
		WorkerLeaseDurationSeconds:  2,
		WorkerRequestTimeoutSeconds: 2,
		WorkerType:                  "text-plain",
	})
	defer cleanup3()
	if err != nil {
		t.Fatal(err)
	}
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
		Identity:   serverops.DefaultAdminUser,
		ID:         uuid.NewString(),
		Resource:   serverops.DefaultServerGroup,
		Permission: store.PermissionManage,
	})
	if err != nil {
		t.Fatalf("failed to create access entry: %v", err)
	}
	go func() {
		if err := http.ListenAndServe("127.0.0.1"+port, mux); err != nil {
			log.Fatal(err)
		}
	}()
	file := &fileservice.File{
		Path:        "updated.txt",
		ContentType: "text/plain",
		Data:        []byte("some demo text to be embedded"),
	}
	time.Sleep(time.Second * 30)
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
		return strings.Contains(string(r), `"name":"all-minilm:33m"`)
	}, 2*time.Minute, 100*time.Millisecond)
	runtime := state.Get(ctx)
	url := ""
	backendID := ""
	found := false
	for _, runtimeState := range runtime {
		url = runtimeState.Backend.BaseURL
		backendID = runtimeState.Backend.ID
		for _, lmr := range runtimeState.PulledModels {
			if lmr.Model == "all-minilm:33m" {
				found = true
				break
			}
		}
		if found {
			break
		}
	}
	if !found {
		t.Fatalf("all-minilm:33m not found")
	}
	_ = url
	err = store.New(dbInstance.WithoutTransaction()).AssignBackendToPool(ctx, serverops.EmbedPoolID, backendID)
	if err != nil {
		t.Fatalf("failed to assign backend to pool: %v", err)
	}
	// sanity check
	_, err = llmresolver.ResolveEmbed(ctx, llmresolver.ResolveEmbedRequest{
		ModelName: "all-minilm:33m",
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
	// ensure embedder is ready
	embedderProvider, err := embedder.GetProvider(ctx)
	if err != nil {
		t.Fatalf("failed to get embedder provider: %v", err)
	}
	if !embedderProvider.CanEmbed() {
		t.Fatalf("embedder not ready")
	}

	t.Run("create a file should trigger vectorization", func(t *testing.T) {
		file, err = fileService.CreateFile(ctx, file)
		if err != nil {
			t.Fatalf("failed to create file: %v", err)
		}
	})
}
