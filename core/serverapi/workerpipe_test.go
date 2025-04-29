package serverapi_test

import (
	"log"
	"net/http"
	"testing"

	"github.com/js402/cate/core/llmembed"
	"github.com/js402/cate/core/serverapi/filesapi"
	"github.com/js402/cate/core/serverapi/indexapi"
	"github.com/js402/cate/core/serverops"
	"github.com/js402/cate/core/serverops/vectors"
	"github.com/js402/cate/core/services/fileservice"
	"github.com/js402/cate/core/services/indexservice"
	"github.com/js402/cate/core/services/testingsetup"
	"github.com/js402/cate/libs/libtestenv"
)

func TestWorkerPipe(t *testing.T) {
	config := &serverops.Config{
		JWTExpiry: "1h",
	}
	ctx, state, dbInstance, cleanup := testingsetup.SetupTestEnvironment(t, config)
	defer cleanup()

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
	embedder, err := llmembed.New(ctx, config, dbInstance, state)
	if err != nil {
		log.Fatalf("initializing embedding pool failed: %v", err)
	}
	vectorStore, cleanup4, err := vectors.New(ctx, config.VectorStoreURL, vectors.Args{
		// TODO
	})
	if err != nil {
		log.Fatalf("initializing vector store failed: %v", err)
	}
	defer cleanup4()
	indexService := indexservice.New(ctx, embedder, vectorStore)
	indexapi.AddIndexRoutes(mux, config, indexService)

	_, cleanup3, err := libtestenv.SetupLocalWorkerInstance(ctx, libtestenv.WorkerConfig{})
	defer cleanup3()
	if err != nil {
		t.Fatal(err)
	}

	// t.Run("create a file should trigger vectorization", func(t *testing.T) {
	// 	fileService.CreateFile(ctx, &fileservice.File{})
	// })
}
