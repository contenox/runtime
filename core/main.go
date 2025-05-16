package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	"github.com/js402/cate/core/llmrepo"
	"github.com/js402/cate/core/runtimestate"
	"github.com/js402/cate/core/serverapi"
	"github.com/js402/cate/core/serverops"
	"github.com/js402/cate/core/serverops/store"
	"github.com/js402/cate/core/serverops/vectors"
	"github.com/js402/cate/core/taskengine"
	"github.com/js402/cate/libs/libbus"
	"github.com/js402/cate/libs/libdb"
	"github.com/js402/cate/libs/libroutine"
)

var cliSetAdminUser string
var cliSetCoreVersion string

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
	exec, err := taskengine.NewExec(ctx, execRepo, nil) // TODO
	if err != nil {
		log.Fatalf("initializing task engine engine failed: %v", err)
	}
	environmentExec, err := taskengine.NewEnv(ctx, serverops.NoopTracker{}, exec)
	if err != nil {
		log.Fatalf("initializing task engine failed: %v", err)
	}
	vectorStore, cleanup, err := vectors.New(ctx, config.VectorStoreURL, vectors.Args{
		Timeout: time.Second * 10, // TODO: Make this configurable
		SearchArgs: vectors.SearchArgs{
			Radius:  0.03,
			Epsilon: 0.001,
		},
	})
	if err != nil {
		log.Fatalf("initializing vector store failed: %v", err)
	}
	cleanups = append(cleanups, cleanup)
	apiHandler, cleanup, err := serverapi.New(ctx, config, dbInstance, ps, embedder, execRepo, environmentExec, state, vectorStore)
	cleanups = append(cleanups, cleanup)
	if err != nil {
		log.Fatalf("initializing API handler failed: %v", err)
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
