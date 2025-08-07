package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	libbus "github.com/contenox/bus"
	libdb "github.com/contenox/dbexec"
	libkv "github.com/contenox/kvstore"
	libroutine "github.com/contenox/routine"
	"github.com/contenox/runtime-mvp/core/chat"
	"github.com/contenox/runtime-mvp/core/kv"
	"github.com/contenox/runtime-mvp/core/serverops"
	"github.com/contenox/runtime-mvp/core/serverops/store"
	"github.com/contenox/runtime-mvp/core/serverops/vectors"
	"github.com/contenox/runtime-mvp/core/tasksrecipes"
	"github.com/contenox/runtime-mvp/gateway/serverapi"
	"github.com/contenox/runtime/runtimesdk"
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
	cleanups = append(cleanups, cleanup)
	client, err := runtimesdk.NewClient(runtimesdk.Config{
		BaseURL: config.RuntimeBaseUrl,
		Token:   config.DownstreamToken,
	}, http.DefaultClient)
	if err != nil {
		log.Fatalf("initializing runtime client failed: %v", err)
	}

	chatManager := chat.New(kv.NewLocalCache(dbInstance, "chat-123"))

	apiHandler, cleanup, err := serverapi.New(ctx, config, dbInstance, ps, vectorStore, kvManager, chatManager, client)
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
