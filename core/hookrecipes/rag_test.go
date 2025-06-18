package hookrecipes_test

import (
	"strings"
	"testing"
	"time"

	"github.com/contenox/contenox/core/hookrecipes"
	"github.com/contenox/contenox/core/hooks"
	"github.com/contenox/contenox/core/indexrepo"
	"github.com/contenox/contenox/core/serverops"
	"github.com/contenox/contenox/core/serverops/store"
	"github.com/contenox/contenox/core/serverops/vectors"
	"github.com/contenox/contenox/core/services/fileservice"
	"github.com/contenox/contenox/core/services/testingsetup"
	"github.com/contenox/contenox/core/taskengine"
	"github.com/stretchr/testify/require"
)

func TestSystemSearchThenResolveWithFiles(t *testing.T) {
	// Setup test environment
	config := &serverops.Config{
		JWTExpiry:  "1h",
		EmbedModel: "nomic-embed-text:latest",
	}
	testenv := testingsetup.New(t.Context(), serverops.NoopTracker{}).
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
	require.NoError(t, testenv.Err)

	// Setup vector store
	uri, _, cleanup, err := vectors.SetupLocalInstance(testenv.Ctx, "../../")
	defer cleanup()
	require.NoError(t, err)
	config.VectorStoreURL = uri
	vectorStore, cleanupVectorStore, err := vectors.New(t.Context(), config.VectorStoreURL, vectors.Args{
		Timeout: 1 * time.Second,
	})
	require.NoError(t, err)
	defer cleanupVectorStore()

	// Setup embedder
	embedder, err := testenv.NewEmbedder(config)
	require.NoError(t, err)
	require.NoError(t, testenv.AssignBackends(serverops.EmbedPoolID).Err)
	require.NoError(t, testenv.WaitForModel(config.EmbedModel).Err)

	// Create file service
	fileService, err := testenv.NewFileservice(config)
	require.NoError(t, err)

	// Create test files
	ctx := t.Context()
	testFiles := []struct {
		Name     string
		Content  string
		Expected bool
	}{
		{
			Name:     "ai-fundamentals.txt",
			Content:  "Artificial intelligence (AI) refers to the simulation of human intelligence in machines that are programmed to think and learn. These systems can perform tasks such as problem-solving, pattern recognition, and decision-making.",
			Expected: true,
		},
		{
			Name:     "machine-learning.txt",
			Content:  "Machine learning, a key branch of AI, involves training algorithms on data so they can make predictions or decisions without being explicitly programmed. It's widely used in fields like finance, healthcare, and e-commerce.",
			Expected: true,
		},
		{
			Name:     "weather-report.txt",
			Content:  "The sun is shining brightly over the hills today, with temperatures expected to reach a comfortable 25°C. It's a perfect day for outdoor activities.",
			Expected: false,
		},
		{
			Name:     "ai-advancements.txt",
			Content:  "Modern AI systems leverage large datasets and sophisticated algorithms to continuously improve their performance. Through techniques like reinforcement learning, they adapt based on feedback and evolving data.",
			Expected: true,
		},
		{
			Name:     "hiking-trip.txt",
			Content:  "Last weekend, I went on a hiking trip through the alpine trails. The air was crisp, and the view from the summit was breathtaking. Nature and outdoor activities always offer a refreshing escape.",
			Expected: false,
		},
	}

	var fileIDs []string
	for _, tf := range testFiles {
		file := &fileservice.File{
			Name:        tf.Name,
			ContentType: "text/plain; charset=utf-8",
			Data:        []byte(tf.Content),
		}
		createdFile, err := fileService.CreateFile(ctx, file)
		require.NoError(t, err)
		fileIDs = append(fileIDs, createdFile.ID)
	}

	// Vectorize files
	dbInstance := testenv.GetDBInstance()
	for _, id := range fileIDs {
		file, err := store.New(dbInstance.WithoutTransaction()).GetFileByID(ctx, id)
		require.NoError(t, err)

		blob, err := store.New(dbInstance.WithoutTransaction()).GetBlobByID(ctx, file.BlobsID)
		require.NoError(t, err)

		chunks := strings.Split(string(blob.Data), "\n\n")
		_, _, err = indexrepo.IngestChunks(
			ctx,
			embedder,
			vectorStore,
			dbInstance.WithoutTransaction(),
			file.ID,
			"file",
			chunks,
			indexrepo.DummyaugmentStrategy,
		)
		require.NoError(t, err)
	}

	// Create hooks
	searchHook := hooks.NewSearch(embedder, vectorStore, dbInstance)
	resolveHook := hooks.NewSearchResolveHook(dbInstance)
	ragHook := &hookrecipes.SearchThenResolveHook{
		SearchHook:     searchHook,
		ResolveHook:    resolveHook,
		DefaultTopK:    3,
		DefaultDist:    15,
		DefaultPos:     0,
		DefaultEpsilon: 0.8,
		DefaultRadius:  15.0,
	}

	// Allow time for vectorization to complete
	time.Sleep(2 * time.Second)

	t.Run("FileContentRetrieval", func(t *testing.T) {
		tests := []struct {
			name         string
			query        string
			wantContains string
			wantErr      bool
		}{
			{
				name:         "Retrieve AI fundamentals",
				query:        "What is artificial intelligence?",
				wantContains: "simulation of human intelligence",
			},
			{
				name:         "Retrieve machine learning",
				query:        "Explain machine learning",
				wantContains: "training algorithms on data",
			},
			{
				name:         "Retrieve AI advancements",
				query:        "How do modern AI systems improve?",
				wantContains: "reinforcement learning",
			},
			{
				name:         "Position selection",
				query:        "args: position=2 epsilon=0.7 radius=25.0 | How do modern AI systems improve?",
				wantContains: "AI systems leverage large datasets", // Should be third result
			},
			{
				name:    "Invalid position",
				query:   "args: position=5 | AI",
				wantErr: true,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				status, out, datatype, transitionEval, err := ragHook.Exec(
					ctx,
					time.Now().UTC(),
					tt.query,
					taskengine.DataTypeString,
					"",
					&taskengine.HookCall{Type: "search_knowledge"},
				)

				if tt.wantErr {
					require.Error(t, err)
					return
				}

				require.NoError(t, err)
				require.Equal(t, taskengine.StatusSuccess, status)
				require.Equal(t, "text/plain", transitionEval)
				require.Equal(t, taskengine.DataTypeString, datatype)
				content, ok := out.(string)
				require.True(t, ok, "output should be string")
				require.Contains(t, content, tt.wantContains)
			})
		}
	})
}
