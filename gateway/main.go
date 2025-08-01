package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	"github.com/contenox/activitytracker"
	libbus "github.com/contenox/bus"
	libdb "github.com/contenox/dbexec"
	libkv "github.com/contenox/kvstore"
	"github.com/contenox/runtime-mvp/core/chat"
	"github.com/contenox/runtime-mvp/core/hookrecipes"
	"github.com/contenox/runtime-mvp/core/hooks"
	"github.com/contenox/runtime-mvp/core/kv"
	"github.com/contenox/runtime-mvp/core/llmrepo"
	"github.com/contenox/runtime-mvp/core/ollamatokenizer"
	"github.com/contenox/runtime-mvp/core/runtimestate"
	"github.com/contenox/runtime-mvp/core/serverops"
	"github.com/contenox/runtime-mvp/core/serverops/store"
	"github.com/contenox/runtime-mvp/core/serverops/vectors"
	"github.com/contenox/runtime-mvp/core/taskengine"
	"github.com/contenox/runtime-mvp/core/tasksrecipes"
	"github.com/contenox/runtime-mvp/gateway/serverapi"
	"github.com/contenox/runtime-mvp/libs/libroutine"
)

var (
	cliSetAdminUser   string
	cliSetCoreVersion string
)

func initDatabase(ctx context.Context, cfg *serverops.Config) (libdb.DBManager, error) {
	dbURL := cfg.DatabaseURL
	var err error
	if dbURL == "" {
		err = fmt.Errorf("DATABASE_URL is required")
		return nil, fmt.Errorf("failed to create store: %w", err)
	}
	var dbInstance libdb.DBManager
	err = libroutine.NewRoutine(10, time.Minute).ExecuteWithRetry(ctx, time.Second, 3, func(ctx context.Context) error {
		dbInstance, err = libdb.NewPostgresDBManager(ctx, dbURL, store.Schema)
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create store: %w", err)
	}

	return dbInstance, nil
}

func initPubSub(ctx context.Context, cfg *serverops.Config) (libbus.Messenger, error) {
	ps, err := libbus.NewPubSub(ctx, &libbus.Config{
		NATSURL:      cfg.NATSURL,
		NATSPassword: cfg.NATSPassword,
		NATSUser:     cfg.NATSUser,
	})
	if err != nil {
		return nil, err
	}
	return ps, nil
}

func main() {
	serverops.DefaultAdminUser = cliSetAdminUser
	if serverops.DefaultAdminUser == "" {
		log.Fatalf("corrupted build! cliSetAdminUser was not injected")
	}
	if cliSetCoreVersion == "" {
		log.Fatalf("corrupted build! cliSetCoreVersion was not injected")
	}
	serverops.CoreVersion = cliSetCoreVersion
	config := &serverops.Config{}
	if err := serverops.LoadConfig(config); err != nil {
		log.Fatalf("failed to load configuration: %v", err)
	}
	if err := serverops.ValidateConfig(config); err != nil {
		log.Fatalf("configuration did not pass validation: %v", err)
	}
	ctx := context.TODO()
	cleanups := []func() error{func() error {
		fmt.Println("cleaning up")
		return nil
	}}
	defer func() {
		for _, cleanup := range cleanups {
			err := cleanup()
			if err != nil {
				log.Printf("cleanup failed: %v", err)
			}
		}
	}()
	fmt.Print("initialize the database")
	dbInstance, err := initDatabase(ctx, config)
	if err != nil {
		log.Fatalf("initializing database failed: %v", err)
	}
	defer dbInstance.Close()

	ps, err := initPubSub(ctx, config)
	if err != nil {
		log.Fatalf("initializing PubSub failed: %v", err)
	}
	if err != nil {
		log.Fatalf("initializing OpenSearch failed: %v", err)
	}
	state, err := runtimestate.New(ctx, dbInstance, ps, runtimestate.WithPools())
	if err != nil {
		log.Fatalf("initializing runtime state failed: %v", err)
	}
	embedder, err := llmrepo.NewEmbedder(ctx, config, dbInstance, state)
	if err != nil {
		log.Fatalf("initializing embedding pool failed: %v", err)
	}
	tokenizerSvc, cleanup, err := ollamatokenizer.NewHTTPClient(ctx, ollamatokenizer.ConfigHTTP{
		BaseURL: config.TokenizerServiceURL,
	})
	if err != nil {
		cleanup()
		log.Fatalf("initializing tokenizer service failed: %v", err)
	}

	execRepo, err := llmrepo.NewExecRepo(ctx, config, dbInstance, state, tokenizerSvc)
	if err != nil {
		log.Fatalf("initializing promptexec failed: %v", err)
	}

	vectorStore, cleanup, err := vectors.New(ctx, config.VectorStoreURL, vectors.Args{
		Timeout: time.Second * 10, // TODO: Make this configurable
		SearchArgs: vectors.SearchArgs{
			Radius:  0.03,
			Epsilon: 0.001,
		},
	})
	cleanups = append(cleanups, cleanup)
	if err != nil {
		log.Fatalf("initializing vector store failed: %v", err)
	}
	kvManager, err := libkv.NewManager(libkv.Config{
		Addr:     config.KVHost,
		Password: config.KVPassword,
	}, time.Hour*24)
	if err != nil {
		log.Fatalf("initializing kv manager failed: %v", err)
	}
	kvExec, err := kvManager.Executor(ctx)
	if err != nil {
		log.Fatalf("initializing kv manager 1 failed: %v", err)
	}
	err = kvExec.SetWithTTL(ctx, "test", []byte("test"), time.Second)
	if err != nil {
		log.Fatalf("initializing kv manager 2 failed: %v", err)
	}
	rag := hooks.NewSearch(embedder, vectorStore, dbInstance)
	webcall := hooks.NewWebCaller()
	// Hook instances
	echocmd := hooks.NewEchoHook()
	settings := kv.NewLocalCache(dbInstance, "")
	breakerSettings := libroutine.NewRoutine(3, time.Second*10)
	triggerChan := make(chan struct{})
	go breakerSettings.Loop(ctx, time.Second*3, triggerChan, settings.ProcessTick, func(err error) {
		log.Printf("SERVER Error in settings.ProcessTick: %v", err)
	})
	tracker := taskengine.NewKVActivityTracker(kvManager)
	stdOuttracker := activitytracker.NewLogActivityTracker(slog.Default())
	serveropsChainedTracker := activitytracker.ChainedTracker{
		tracker,
		stdOuttracker,
	}
	chatManager := chat.New(state, tokenizerSvc, settings)
	chatHook := hooks.NewChatHook(dbInstance, chatManager)
	knowledgeHook := hookrecipes.NewSearchThenResolveHook(hookrecipes.SearchThenResolveHook{
		SearchHook:     rag,
		ResolveHook:    hooks.NewSearchResolveHook(dbInstance),
		DefaultTopK:    1,
		DefaultDist:    40,
		DefaultPos:     0,
		DefaultEpsilon: 0.5,
		DefaultRadius:  40,
	}, activitytracker.NewLogActivityTracker(slog.Default()))
	// Mux for handling commands like /echo
	// transition := hooks.NewTransition("help", serverops.NewLogActivityTracker(slog.Default()))
	printHook := hooks.NewPrint(activitytracker.NewLogActivityTracker(slog.Default()))
	// hookMux := hooks.NewMux(map[string]taskengine.HookRepo{
	// 	"echo":             echocmd,
	// 	"search_knowledge": knowledgeHook,
	// 	"vector_search":    rag,
	// 	"help":             transition,
	// }, serverops.NewLogActivityTracker(slog.Default()))

	// Combine all hooks into one registry
	hooks := hooks.NewSimpleProvider(map[string]taskengine.HookRepo{
		"vector_search":    rag,
		"webhook":          webcall,
		"search_knowledge": knowledgeHook,
		// "command_router":               hookMux,
		"append_user_message":          chatHook,
		"echo":                         echocmd,
		"preappend_message_to_history": chatHook,
		"convert_openai_to_history":    chatHook,
		"convert_history_to_openai":    chatHook,
		"append_system_message":        chatHook,
		"persist_messages":             chatHook,
		"help":                         printHook,
		"print":                        printHook,
	})
	exec, err := taskengine.NewExec(ctx, execRepo, hooks, serveropsChainedTracker)
	if err != nil {
		log.Fatalf("initializing task engine engine failed: %v", err)
	}
	environmentExec, err := taskengine.NewEnv(ctx, serveropsChainedTracker, taskengine.NewAlertSink(kvManager), exec, taskengine.NewSimpleInspector(kvManager))
	if err != nil {
		log.Fatalf("initializing task engine failed: %v", err)
	}
	cleanups = append(cleanups, cleanup)
	apiHandler, cleanup, err := serverapi.New(ctx, config, dbInstance, ps, embedder, execRepo, environmentExec, state, vectorStore, hooks, chatManager, kvManager)
	cleanups = append(cleanups, cleanup)
	if err != nil {
		log.Fatalf("initializing API handler failed: %v", err)
	}
	err = tasksrecipes.InitializeDefaultChains(ctx, config, dbInstance)
	if err != nil {
		log.Fatalf("initializing default tasks failed: %v", err)
	}

	mux := http.NewServeMux()
	mux.Handle("/api/", http.StripPrefix("/api", apiHandler))
	uiURL, err := url.Parse(config.UIBaseURL)
	if err != nil {
		log.Fatalf("failed to parse UI base URL: %v", err)
	}
	uiProxy := httputil.NewSingleHostReverseProxy(uiURL)

	// All other routes will be handled by the UI reverse proxy
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		uiProxy.ServeHTTP(w, r)
	})

	port := config.Port
	log.Printf("starting server on :%s", port)
	if err := http.ListenAndServe(config.Addr+":"+port, mux); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}
