package worker

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"time"

	"github.com/contenox/contenox/core/llmexec"
	"github.com/contenox/contenox/core/modelprovider"
	"github.com/contenox/contenox/core/serverops"
	"github.com/js402/contenox/contenox/libkv"
)

const (
	jobCheckInterval = 5 * time.Second
	ttl              = 5 * time.Minute
)

type JobHandler func(context.Context, []byte) ([]byte, error)

type CommonWorker struct {
	kv       libkv.KVManager
	handlers map[llmexec.TaskType]JobHandler
}

func (w *CommonWorker) Start(ctx context.Context) {
	log.Println("Worker started")
	ticker := time.NewTicker(jobCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("Worker shutting down...")
			return
		case <-ticker.C:
			w.processJobs(ctx)
		}
	}
}

func (w *CommonWorker) processJobs(ctx context.Context) {
	exec, err := w.kv.Operation(ctx)
	if err != nil {
		log.Printf("KV operation failed: %v", err)
		return
	}

	keys, _ := exec.List(ctx)
	for _, key := range keys {
		if strings.HasSuffix(key, ":pending") {
			w.processJob(ctx, exec, key)
		}
	}
}

func (w *CommonWorker) processJob(ctx context.Context, exec libkv.KVExec, key string) {
	key = strings.TrimSuffix(key, ":pending")
	parts := strings.Split(key, ":")
	if len(parts) < 2 {
		return
	}

	taskType := llmexec.TaskType(parts[0])
	jobID := parts[1]
	doneKey := key + ":done"

	if exists, _ := exec.Exists(ctx, []byte(doneKey)); exists {
		return
	}

	handler, ok := w.handlers[taskType]
	if !ok {
		log.Printf("No handler for task type: %s", taskType)
		return
	}

	pendingKey := key + ":pending"
	data, err := exec.Get(ctx, []byte(pendingKey))
	if err != nil {
		log.Printf("Failed to get job data: %v", err)
		return
	}

	result, err := handler(ctx, data)
	if err != nil {
		log.Printf("Job processing failed: %v", err)
		return
	}

	if err := exec.Set(ctx, libkv.KeyValue{
		Key:   []byte(doneKey),
		Value: result,
		TTL:   time.Now().Add(ttl),
	}); err != nil {
		log.Printf("Failed to set result: %v", err)
		return
	}

	exec.Delete(ctx, []byte(pendingKey))
	log.Printf("Processed %s job: %s", taskType, jobID)
}

func NewOllamaWorker(
	kv libkv.KVManager,
	chat *modelprovider.OllamaChatClient,
	embed *modelprovider.OllamaEmbedClient,
	prompt *modelprovider.OllamaPromptClient,
) *CommonWorker {
	return &CommonWorker{
		kv: kv,
		handlers: map[llmexec.TaskType]JobHandler{
			llmexec.Chat: func(ctx context.Context, data []byte) ([]byte, error) {
				var messages []serverops.Message
				if err := json.Unmarshal(data, &messages); err != nil {
					return nil, err
				}
				resp, err := chat.Chat(ctx, messages)
				if err != nil {
					return nil, err
				}
				return json.Marshal(resp)
			},
			llmexec.Embed: func(ctx context.Context, data []byte) ([]byte, error) {
				embeddings, err := embed.Embed(ctx, string(data))
				if err != nil {
					return nil, err
				}
				return json.Marshal(embeddings)
			},
			llmexec.Prompt: func(ctx context.Context, data []byte) ([]byte, error) {
				prompt, err := prompt.Prompt(ctx, string(data))
				if err != nil {
					return nil, err
				}
				return []byte(prompt), nil
			},
		},
	}
}

func NewVLLMWorker(
	kv libkv.KVManager,
	chat *modelprovider.VLLMChatClient,
	prompt *modelprovider.VLLMPromptClient,
) *CommonWorker {
	return &CommonWorker{
		kv: kv,
		handlers: map[llmexec.TaskType]JobHandler{
			llmexec.Chat: func(ctx context.Context, data []byte) ([]byte, error) {
				var messages []serverops.Message
				if err := json.Unmarshal(data, &messages); err != nil {
					return nil, err
				}
				resp, err := chat.Chat(ctx, messages)
				if err != nil {
					return nil, err
				}
				return json.Marshal(resp)
			},
			llmexec.Prompt: func(ctx context.Context, data []byte) ([]byte, error) {
				resp, err := prompt.Prompt(ctx, string(data))
				return []byte(resp), err
			},
		},
	}
}
