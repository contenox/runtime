package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"time"

	"github.com/contenox/runtime-mvp/core/chat"
	"github.com/contenox/runtime-mvp/core/hookrecipes"
	"github.com/contenox/runtime-mvp/core/hooks"
	"github.com/contenox/runtime-mvp/core/kv"
	"github.com/contenox/runtime-mvp/core/llmrepo"
	"github.com/contenox/runtime-mvp/core/runtimestate"
	"github.com/contenox/runtime-mvp/core/serverapi"
	"github.com/contenox/runtime-mvp/core/serverops"
	"github.com/contenox/runtime-mvp/core/serverops/store"
	"github.com/contenox/runtime-mvp/core/serverops/vectors"
	"github.com/contenox/runtime-mvp/core/services/telegramservice"
	"github.com/contenox/runtime-mvp/core/services/tokenizerservice"
	"github.com/contenox/runtime-mvp/core/taskengine"
	"github.com/contenox/runtime-mvp/libs/libbus"
	"github.com/contenox/runtime-mvp/libs/libdb"
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
	execRepo, err := llmrepo.NewExecRepo(ctx, config, dbInstance, state)
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
	rag := hooks.NewSearch(embedder, vectorStore, dbInstance)
	webcall := hooks.NewWebCaller()
	// Hook instances
	echocmd := hooks.NewEchoHook()
	tokenizerSvc, cleanup, err := tokenizerservice.NewGRPCTokenizer(ctx, tokenizerservice.ConfigGRPC{
		ServerAddress: config.TokenizerServiceURL,
	})
	if err != nil {
		cleanup()
		log.Fatalf("initializing tokenizer service failed: %v", err)
	}
	settings := kv.NewLocalCache(dbInstance, "")
	breakerSettings := libroutine.NewRoutine(3, time.Second*10)
	triggerChan := make(chan struct{})
	go breakerSettings.Loop(ctx, time.Second*3, triggerChan, settings.ProcessTick, func(err error) {
		log.Printf("SERVER Error in settings.ProcessTick: %v", err)
	})
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
	})
	// Mux for handling commands like /echo
	hookMux := hooks.NewMux(map[string]taskengine.HookRepo{
		"echo":             echocmd,
		"search_knowledge": knowledgeHook,
	})

	// Combine all hooks into one registry
	hooks := hooks.NewSimpleProvider(map[string]taskengine.HookRepo{
		"vector_search":             rag,
		"webhook":                   webcall,
		"command_router":            hookMux,
		"append_user_message":       chatHook,
		"convert_openai_to_history": chatHook,
		"convert_history_to_openai": chatHook,
		"append_system_message":     chatHook,
		"execute_model_on_messages": chatHook,
		"persist_messages":          chatHook,
	})
	exec, err := taskengine.NewExec(ctx, execRepo, hooks)
	if err != nil {
		log.Fatalf("initializing task engine engine failed: %v", err)
	}
	environmentExec, err := taskengine.NewEnv(ctx, serverops.NewLogActivityTracker(slog.Default()), exec)
	if err != nil {
		log.Fatalf("initializing task engine failed: %v", err)
	}
	cleanups = append(cleanups, cleanup)
	apiHandler, cleanup, err := serverapi.New(ctx, config, dbInstance, ps, embedder, execRepo, environmentExec, state, vectorStore, hooks, chatManager)
	cleanups = append(cleanups, cleanup)
	if err != nil {
		log.Fatalf("initializing API handler failed: %v", err)
	}
	if config.TelegramToken != "" {
		bootOffset := 0
		if config.TelegramBootOffset != "" {
			bootOffset, err = strconv.Atoi(config.TelegramBootOffset)
			if err != nil {
				log.Fatalf("parsing Telegram boot offset failed: %v", err)
			}
		}
		telegramWorker, err := telegramservice.New(ctx, config.TelegramToken, bootOffset, environmentExec, dbInstance)
		telegramservice.WithActivityTracker(telegramWorker, serverops.NewLogActivityTracker(slog.Default()))
		if err != nil {
			log.Fatalf("initializing Telegram worker failed: %v", err)
		}
		var BotErr error
		triggerChan := make(chan struct{})
		breakerTelegram := libroutine.NewRoutine(10, time.Second)
		go breakerTelegram.Loop(ctx, time.Second, triggerChan, func(ctx context.Context) error {
			BotErr = telegramWorker.ProcessTick(ctx)
			if BotErr != nil {
				return fmt.Errorf("initializing Telegram worker ProcessTick failed: %v", BotErr)
			}
			return nil
		}, func(err error) {
			if err != nil {
				slog.Error("Telegram worker ProcessTick failed", "error", err)
			}
		})
		breakerPullTelegram := libroutine.NewRoutine(10, time.Second)
		go breakerPullTelegram.Loop(ctx, time.Second, triggerChan, func(ctx context.Context) error {
			BotErr = telegramWorker.ReceiveTick(ctx)
			if BotErr != nil {
				return fmt.Errorf("initializing Telegram worker ReceiveTick failed: %v", BotErr)
			}
			return nil
		}, func(err error) {
			if err != nil {
				slog.Error("Telegram worker ReceiveTick failed", "error", err)
			}
		})
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
