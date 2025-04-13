package main

import (
	"context"
	"fmt"
	"log"
	"net/http"

	"github.com/js402/CATE/internal/serverapi"
	"github.com/js402/CATE/internal/serverops"
)

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

	apiHandler, err := serverapi.New(ctx, config, store, ps, bus)
	if err != nil {
		log.Fatalf("initializing API handler failed: %v", err)
	}

	mux := http.NewServeMux()
	mux.Handle("/api/", http.StripPrefix("/api", apiHandler))

	port := config.Port
	log.Printf("starting server on :%s", port)
	if err := http.ListenAndServe(config.Addr+":"+port, mux); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}
