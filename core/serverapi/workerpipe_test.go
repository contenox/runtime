package serverapi_test

import (
	"context"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/contenox/contenox/core/indexrepo"
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
	"github.com/testcontainers/testcontainers-go"
)

func TestSystem_WorkerPipeline_ProcessesFileAndReturnsSearchResult(t *testing.T) {
	port := rand.Intn(16383) + 49152
	testStartTime := time.Now().UTC()
	config := &serverops.Config{
		JWTExpiry:       "1h",
		JWTSecret:       "securecryptngkeysecurecryptngkey",
		EncryptionKey:   "securecryptngkeysecurecryptngkey",
		SigningKey:      "securecryptngkeysecurecryptngkey",
		EmbedModel:      "nomic-embed-text:latest",
		TasksModel:      "qwen2.5:1.5b",
		SecurityEnabled: "true",
	}
	var workerContainer testcontainers.Container
	var cleanupWorker func() = func() {}
	defer cleanupWorker()

	getLogs := func(ctx context.Context) {
		if workerContainer == nil {
			t.Fatalf("worker container is not initialized")
		}
		readCloser, err := workerContainer.Logs(ctx)
		require.NoError(t, err, "failed to get worker logs stream")
		defer readCloser.Close()

		logBytes, err := io.ReadAll(readCloser)
		if err != nil && err != io.EOF {
			t.Logf("Warning: failed to read all worker logs: %v", err)
		}
		t.Logf("WORKER LOGS:\n%s\n--- END WORKER LOGS ---", string(logBytes))
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
			Epsilon: 0.9,
			Radius:  20.0,
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
	handler := serverapi.JWTMiddleware(config, mux)
	dispatchapi.AddDispatchRoutes(mux, config, dispatcher)
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
	require.Equal(t, 768, len(vectorData32), "Query vector dimension mismatch")

	// Run single file test
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

		workerContainer, cleanupWorker, err = libtestenv.SetupLocalWorkerInstance(ctx, libtestenv.WorkerConfig{
			APIBaseURL:                  fmt.Sprintf("http://172.17.0.1:%d", port),
			WorkerEmail:                 serverops.DefaultAdminUser,
			WorkerPassword:              "test",
			WorkerLeaserID:              "my-worker-1",
			WorkerLeaseDurationSeconds:  2,
			WorkerRequestTimeoutSeconds: 2,
			WorkerType:                  "plaintext",
		})
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Second * 3)
		readCloser, err := workerContainer.Logs(ctx)
		require.NoError(t, err, "failed to get worker logs stream")
		defer readCloser.Close()
		getLogs(ctx)
		require.Eventually(t, func() bool {
			jobs, err = dispatcher.PendingJobs(ctx, &testStartTime)
			if err != nil {
				t.Logf("Warning: failed to get pending jobs: %v", err)
			}
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
			if errText != "" {
				t.Log(errText)
			}
			return -1 == found
		}, time.Second*20, time.Second)

		results, err := vectorStore.Search(ctx, vectorData32, 10, 1, nil)
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

	// Run expanded multi-file semantic search test
	t.Run("multiple files are indexed and semantically searchable", func(t *testing.T) {
		testFiles := []struct {
			Name     string
			Content  string
			Expected bool
		}{
			{"file1.txt", "Artificial intelligence (AI) refers to the simulation of human intelligence in machines that are programmed to think and learn. These systems can perform tasks such as problem-solving, pattern recognition, and decision-making.", true},
			{"file2.txt", "Machine learning, a key branch of AI, involves training algorithms on data so they can make predictions or decisions without being explicitly programmed. It's widely used in fields like finance, healthcare, and e-commerce.", true},
			{"file3.txt", "The sun is shining brightly over the hills today, with temperatures expected to reach a comfortable 25°C. It's a perfect day for outdoor activities.", false},
			{"file4.txt", "Modern AI systems leverage large datasets and sophisticated algorithms to continuously improve their performance. Through techniques like reinforcement learning, they adapt based on feedback and evolving data.", true},
			{"file5.txt", "Last weekend, I went on a hiking trip through the alpine trails. The air was crisp, and the view from the summit was breathtaking. Nature always offers a refreshing escape.", false},
		}

		var createdFiles []*fileservice.File

		// Upload all files
		for _, tf := range testFiles {
			f := &fileservice.File{
				Name:        tf.Name,
				ContentType: "text/plain; charset=utf-8",
				Data:        []byte(tf.Content),
			}
			createdFile, err := fileService.CreateFile(ctx, f)
			require.NoError(t, err)
			createdFiles = append(createdFiles, createdFile)
		}
		if workerContainer == nil {
			workerContainer, cleanupWorker, err = libtestenv.SetupLocalWorkerInstance(ctx, libtestenv.WorkerConfig{
				APIBaseURL:                  fmt.Sprintf("http://172.17.0.1:%d", port),
				WorkerEmail:                 serverops.DefaultAdminUser,
				WorkerPassword:              "test",
				WorkerLeaserID:              "my-worker-1",
				WorkerLeaseDurationSeconds:  2,
				WorkerRequestTimeoutSeconds: 2,
				WorkerType:                  "plaintext",
			})
		}
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Second * 3)

		// Wait for ingestion
		require.Eventually(t, func() bool {
			jobs, err := dispatcher.PendingJobs(ctx, &testStartTime)
			if err != nil {
				t.Logf("Warning: failed to get pending jobs: %v", err)
			}
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
			if errText != "" {
				t.Log(errText)
			}
			return -1 == found
		}, time.Second*20, time.Second)
		time.Sleep(time.Second * 3)
		getLogs(ctx)
		// Define queries and expected matches
		testQueries := []struct {
			Query           string
			ExpectedMatches int
			RelevantFiles   []int
		}{
			// Direct and paraphrased queries related to AI
			{"artificial intelligence", 3, []int{0, 1, 3}},
			{"What is artificial intelligence?", 3, []int{0, 1, 3}},
			{"Explain how AI systems work", 2, []int{0, 3}},
			{"examples of AI applications", 2, []int{1, 3}},

			// Machine learning and related subtopics
			{"machine learning", 2, []int{1, 3}},
			{"how does machine learning work?", 2, []int{1, 3}},
			{"uses of ML in industries", 1, []int{1}},

			// Irrelevant topic - weather
			{"sunny weather", 1, []int{2}},
			{"What’s the temperature today?", 1, []int{2}},
			{"daily weather report", 1, []int{2}},

			// Irrelevant topic - hiking
			{"hiking mountains", 1, []int{4}},
			{"Tell me about a mountain trip", 1, []int{4}},
			{"experiences in nature", 1, []int{4}},

			// Slightly more abstract or misaligned queries
			{"neural networks", 1, []int{1}},
			{"how do computers learn?", 2, []int{1, 3}},
		}

		t.Run("query", func(t *testing.T) {
			for _, q := range testQueries {
				resp, err := indexService.Search(ctx, &indexservice.SearchRequest{
					Query: q.Query,
					TopK:  10,
					SearchRequestArgs: &indexservice.SearchRequestArgs{
						Epsilon: 0.8,
						Radius:  20,
					},
					ExpandFiles: true,
				})
				require.NoError(t, err)

				// Build a map of result IDs from search response for quick lookup
				resultMap := make(map[string]indexrepo.SearchResult)
				var resultDetails string
				for _, sr := range resp.Results {
					fileName, err := testenv.Store().GetFileNameByID(ctx, sr.ID)
					require.NoError(t, err)
					resultDetails += fmt.Sprintf("%s distance %f\n", fileName, sr.Distance)
					resultMap[sr.ID] = sr
				}

				// Prepare the list of expected file IDs
				expectedIDs := make(map[string]string) // ID -> filename for better error messages
				for _, idx := range q.RelevantFiles {
					f := createdFiles[idx]
					expectedIDs[f.ID] = f.Name
				}

				// Track missing files
				var missing []string

				// Ensure every expected file is in the results
				for id, name := range expectedIDs {
					if _, ok := resultMap[id]; !ok {
						missing = append(missing, name)
					}
				}

				// Fail if any expected files are missing
				if len(missing) > 0 {
					msg := "missing expected files: " + strings.Join(missing, ", ") + "\n"
					msg += "--- Results were:\n" + resultDetails
					msg += "--- Tried queries: " + strings.Join(resp.TriedQueries, ", ")
					require.Fail(t, msg)
				}

				// Optionally ensure no unexpected files appear in results
				for _, f := range createdFiles {
					isInResults := false
					if _, ok := resultMap[f.ID]; ok {
						isInResults = ok
					}

					isExpected := false
					for _, idx := range q.RelevantFiles {
						if f == createdFiles[idx] {
							isExpected = true
							break
						}
					}

					if !isExpected && isInResults {
						require.Failf(t, "unexpected match for file %s", f.Name)
					}
				}
			}
		})
	})
}
