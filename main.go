package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/js402/CATE/internal/serverapi"
	"github.com/js402/CATE/internal/serverops"
	"github.com/js402/CATE/internal/serverops/messagerepo"
	"github.com/js402/CATE/internal/serverops/store"
	"github.com/js402/CATE/libs/libbus"
	"github.com/js402/CATE/libs/libdb"
	"github.com/js402/CATE/libs/libroutine"
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
		dbInstance, err = libdb.NewPostgresDBManager(dbURL, store.Schema)
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
	config := &serverops.Config{}
	if err := serverops.LoadConfig(config); err != nil {
		log.Fatalf("failed to load configuration: %v", err)
	}
	if err := serverops.ValidateConfig(config); err != nil {
		log.Fatalf("configuration did not pass validation: %v", err)
	}
	ctx := context.TODO()
	fmt.Print("initialize the database")
	store, err := initDatabase(ctx, config)
	if err != nil {
		log.Fatalf("initializing database failed: %v", err)
	}
	defer store.Close()

	ps, err := initPubSub(ctx, config)
	if err != nil {
		log.Fatalf("initializing PubSub failed: %v", err)
	}
	var bus messagerepo.Store
	libroutine.NewRoutine(10, time.Second*10).ExecuteWithRetry(ctx, time.Second*3, 10, func(ctx context.Context) error {
		bus, err = openSearch(ctx, config)
		return err
	})
	if err != nil {
		log.Fatalf("initializing OpenSearch failed: %v", err)
	}
	apiHandler, err := serverapi.New(ctx, config, store, ps, bus)
	if err != nil {
		log.Fatalf("initializing API handler failed: %v", err)
	}
	port := config.Port
	log.Printf("starting API server on :%s", port)
	if err := http.ListenAndServe(config.Addr+":"+port, apiHandler); err != nil {
		log.Fatalf("API server failed: %v", err)
	}
}

func openSearch(ctx context.Context, config *serverops.Config) (messagerepo.Store, error) {
	return messagerepo.New(ctx, config.OpensearchURL, "messages", time.Second*10)
}
