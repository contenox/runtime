package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/contenox/contenox/core/modelprovider"
	"github.com/contenox/contenox/core/serverops"
	"github.com/js402/contenox/contenox/libkv"
)

type VLLMWorker struct {
	kv        libkv.KVManager
	promptCL  *modelprovider.VLLMPromptClient
	chatCL    *modelprovider.VLLMChatClient
	modelName string
}

func NewVLLMWorker(
	kv libkv.KVManager,
	promptCL *modelprovider.VLLMPromptClient,
	chatCL *modelprovider.VLLMChatClient,
	modelName string,
) *VLLMWorker {
	return &VLLMWorker{
		kv:        kv,
		promptCL:  promptCL,
		chatCL:    chatCL,
		modelName: modelName,
	}
}

func (w *VLLMWorker) Start(ctx context.Context) {
	log.Printf("Starting vLLM worker for model %q", w.modelName)
	ticker := time.NewTicker(jobCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("Shutting down vLLM worker...")
			return
		case <-ticker.C:
			if err := w.processPendingJobs(ctx); err != nil {
				log.Printf("Error processing jobs: %v", err)
			}
		}
	}
}

func (w *VLLMWorker) processPendingJobs(ctx context.Context) error {
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

func (w *VLLMWorker) processJob(ctx context.Context, exec libkv.KVExec, key string) error {
	keyStr := string(key)
	doneKey := strings.TrimSuffix(keyStr, ":pending") + ":done"

	// Skip if already done
	exists, err := exec.Exists(ctx, []byte(doneKey))
	if err != nil {
		return fmt.Errorf("failed to check existence of %s: %w", doneKey, err)
	}
	if exists {
		return nil // Already processed
	}

	// Lease job: Check if pending key exists
	jobExists, err := exec.Exists(ctx, []byte(keyStr))
	if err != nil {
		return fmt.Errorf("failed to check job existence: %w", err)
	}
	if !jobExists {
		return fmt.Errorf("job key %s not found", keyStr)
	}

	// Extract job type and ID
	parts := strings.Split(strings.TrimSuffix(keyStr, ":pending"), ":")
	if len(parts) < 2 {
		return fmt.Errorf("invalid key format: %s", keyStr)
	}
	jobType := parts[0]
	jobID := parts[1]

	switch jobType {
	case "chat":
		return w.processChatJob(ctx, exec, jobID, keyStr, doneKey)
	case "prompt":
		return w.processPromptJob(ctx, exec, jobID, keyStr, doneKey)
	default:
		return fmt.Errorf("unsupported job type: %s", jobType)
	}
}

func (w *VLLMWorker) processChatJob(ctx context.Context, exec libkv.KVExec, jobID, key, doneKey string) error {
	rawData, err := exec.Get(ctx, []byte(key))
	if err != nil {
		return fmt.Errorf("failed to read chat job data: %w", err)
	}

	var messages []serverops.Message
	if err := json.Unmarshal(rawData, &messages); err != nil {
		return fmt.Errorf("failed to unmarshal chat messages: %w", err)
	}

	response, err := w.chatCL.Chat(ctx, messages)
	if err != nil {
		return fmt.Errorf("vLLM chat failed: %w", err)
	}

	responseBytes, _ := json.Marshal(response)
	err = exec.Set(ctx, libkv.KeyValue{
		Key:   []byte(doneKey),
		Value: responseBytes,
		TTL:   time.Now().UTC().Add(ttl),
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

func (w *VLLMWorker) processPromptJob(ctx context.Context, exec libkv.KVExec, jobID, key, doneKey string) error {
	rawPrompt, err := exec.Get(ctx, []byte(key))
	if err != nil {
		return fmt.Errorf("failed to read prompt job data: %w", err)
	}
	prompt := string(rawPrompt)

	response, err := w.promptCL.Prompt(ctx, prompt)
	if err != nil {
		return fmt.Errorf("vLLM prompt failed: %w", err)
	}

	err = exec.Set(ctx, libkv.KeyValue{
		Key:   []byte(doneKey),
		Value: []byte(response),
		TTL:   time.Now().UTC().Add(ttl),
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
