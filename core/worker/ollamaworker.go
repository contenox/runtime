package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/contenox/contenox/core/llmexec"
	"github.com/js402/contenox/contenox/libkv"
)

const (
	jobCheckInterval = 5 * time.Second
	ttl              = 5 * time.Minute
)

type OllamaWorker struct {
	kv      libkv.KVManager
	llmExec llmexec.LLMExec
}

func NewWorker(kv libkv.KVManager, llmExec llmexec.LLMExec) *OllamaWorker {
	return &OllamaWorker{
		kv:      kv,
		llmExec: llmExec,
	}
}

func (w *OllamaWorker) Start(ctx context.Context) {
	log.Println("Worker started")
	ticker := time.NewTicker(jobCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("Worker shutting down...")
			return
		case <-ticker.C:
			if err := w.processPendingJobs(ctx); err != nil {
				log.Printf("Error processing jobs: %v", err)
			}
		}
	}
}

func (w *OllamaWorker) processPendingJobs(ctx context.Context) error {
	exec, err := w.kv.Operation(ctx)
	if err != nil {
		return fmt.Errorf("failed to start KV operation: %w", err)
	}

	keys, err := exec.List(ctx)
	if err != nil {
		return fmt.Errorf("failed to list keys: %w", err)
	}

	for _, key := range keys {
		if strings.HasSuffix(key, ":pending") {
			if err := w.processJob(ctx, exec, key); err != nil {
				log.Printf("Failed to process job %q: %v", key, err)
			}
		}
	}
	return nil
}

func (w *OllamaWorker) processJob(ctx context.Context, exec libkv.KVExec, key string) error {
	// Skip if already processed
	doneKey := strings.TrimSuffix(key, ":pending") + ":done"
	exists, err := exec.Exists(ctx, []byte(doneKey))
	if err != nil {
		return fmt.Errorf("failed to check existence of %s: %w", doneKey, err)
	}
	if exists {
		return nil // Already done
	}

	// Lease job
	jobExists, err := exec.Exists(ctx, []byte(key))
	if err != nil {
		return fmt.Errorf("failed to check job existence: %w", err)
	}
	if !jobExists {
		return fmt.Errorf("job key %s not found", key)
	}

	// Extract job ID and type
	parts := strings.Split(strings.TrimSuffix(string(key), ":pending"), ":")
	if len(parts) < 2 {
		return fmt.Errorf("invalid key format: %s", key)
	}
	jobType := parts[0]
	jobID := parts[1]

	// Process based on job type
	switch jobType {
	case "chat":
		return w.processChatJob(ctx, exec, jobID, key, doneKey)
	case "embed":
		return w.processEmbedJob(ctx, exec, jobID, key, doneKey)
	case "prompt":
		return w.processPromptJob(ctx, exec, jobID, key, doneKey)
	default:
		return fmt.Errorf("unknown job type: %s", jobType)
	}
}

func (w *OllamaWorker) processChatJob(ctx context.Context, exec libkv.KVExec, jobID string, key, doneKey string) error {
	rawData, err := exec.Get(ctx, []byte(key))
	if err != nil {
		return fmt.Errorf("failed to read chat job data: %w", err)
	}

	var messages []llmexec.Message
	if err := json.Unmarshal(rawData, &messages); err != nil {
		return fmt.Errorf("failed to unmarshal chat messages: %w", err)
	}

	response, err := w.llmExec.Chat(ctx, jobID, messages)
	if err != nil {
		return fmt.Errorf("llm chat failed: %w", err)
	}

	responseBytes, _ := json.Marshal(response)
	err = exec.Set(ctx, libkv.KeyValue{
		Key:   []byte(doneKey),
		Value: responseBytes,
		TTL:   time.Now().Add(ttl),
	})
	if err != nil {
		return fmt.Errorf("failed to write chat result: %w", err)
	}

	if err := exec.Delete(ctx, []byte(key)); err != nil {
		log.Printf("Failed to delete chat pending key %s: %v", key, err)
	}

	log.Printf("Processed chat job %s successfully", jobID)
	return nil
}

func (w *OllamaWorker) processEmbedJob(ctx context.Context, exec libkv.KVExec, jobID string, key, doneKey string) error {
	rawPrompt, err := exec.Get(ctx, []byte(key))
	if err != nil {
		return fmt.Errorf("failed to read embed job data: %w", err)
	}
	prompt := string(rawPrompt)

	embeddings, err := w.llmExec.Embed(ctx, jobID, prompt)
	if err != nil {
		return fmt.Errorf("llm embed failed: %w", err)
	}

	responseBytes, _ := json.Marshal(embeddings)
	err = exec.Set(ctx, libkv.KeyValue{
		Key:   []byte(doneKey),
		Value: responseBytes,
		TTL:   time.Now().Add(ttl),
	})
	if err != nil {
		return fmt.Errorf("failed to write embed result: %w", err)
	}

	if err := exec.Delete(ctx, []byte(key)); err != nil {
		log.Printf("Failed to delete embed pending key %s: %v", key, err)
	}

	log.Printf("Processed embed job %s successfully", jobID)
	return nil
}

func (w *OllamaWorker) processPromptJob(ctx context.Context, exec libkv.KVExec, jobID string, key, doneKey string) error {
	rawPrompt, err := exec.Get(ctx, []byte(key))
	if err != nil {
		return fmt.Errorf("failed to read prompt job data: %w", err)
	}
	prompt := string(rawPrompt)

	response, err := w.llmExec.Prompt(ctx, jobID, prompt)
	if err != nil {
		return fmt.Errorf("llm prompt failed: %w", err)
	}

	err = exec.Set(ctx, libkv.KeyValue{
		Key:   []byte(doneKey),
		Value: []byte(response),
		TTL:   time.Now().Add(ttl),
	})
	if err != nil {
		return fmt.Errorf("failed to write prompt result: %w", err)
	}

	if err := exec.Delete(ctx, []byte(key)); err != nil {
		log.Printf("Failed to delete prompt pending key %s: %v", key, err)
	}

	log.Printf("Processed prompt job %s successfully", jobID)
	return nil
}
