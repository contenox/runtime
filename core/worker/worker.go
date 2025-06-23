package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/contenox/contenox/core/llmexec"
	"github.com/google/uuid"
	"github.com/js402/contenox/contenox/libkv"
)

const (
	jobCheckInterval = 5 * time.Second
	ttl              = 5 * time.Minute
)

type (
	TaskHandler         func(context.Context, *llmexec.Job) (*llmexec.Output, error)
	BackendCapabilities func(context.Context, *llmexec.Job) bool
)

type Worker struct {
	kv       libkv.KVManager
	canTake  map[llmexec.TaskType]BackendCapabilities
	handlers map[llmexec.TaskType]TaskHandler
}

func (w *Worker) ProcessJob(ctx context.Context) error {
	execOnKV, err := w.kv.Operation(ctx)
	if err != nil {
		return err
	}

	keys, err := execOnKV.List(ctx)
	if err != nil {
		return err
	}
	leaserID := []byte("worker-" + uuid.New().String())
	for _, key := range keys {
		if strings.HasSuffix(key, ":pending") {
			baseKey := strings.TrimSuffix(key, ":pending")
			parts := strings.Split(baseKey, ":")
			taskType := llmexec.TaskType(parts[0])
			jobRaw, err := execOnKV.Get(ctx, []byte(key))
			if err != nil {
				return err
			}
			job := &llmexec.Job{}
			if err := json.Unmarshal(jobRaw, job); err != nil {
				return err
			}
			if !w.canTake[taskType](ctx, job) {
				continue
			}
			err = execOnKV.Set(ctx, libkv.KeyValue{
				Key:   []byte(baseKey + ":leased"),
				Value: leaserID,
				TTL:   time.Now().UTC().Add(ttl),
			})
			if err != nil {
				return err
			}
			defer func() {
				err := execOnKV.Delete(ctx, []byte(baseKey+":leased"))
				if err != nil {
					// log.Printf("failed to delete lease: %v", err)
				}
			}()
			err = w.processJob(ctx, execOnKV, key)
			if err != nil {
				return err
			}
			break
		}
	}
	return nil
}

func (w *Worker) processJob(ctx context.Context, exec libkv.KVExec, key string) error {
	baseKey := strings.TrimSuffix(key, ":pending")
	parts := strings.Split(baseKey, ":")

	taskType := llmexec.TaskType(parts[0])
	doneKey := baseKey + ":done"

	// Skip if already processed
	if exists, _ := exec.Exists(ctx, []byte(doneKey)); exists {
		return fmt.Errorf("job already processed")
	}

	// Get job data
	data, err := exec.Get(ctx, []byte(key))
	if err != nil {
		return err
	}

	var job llmexec.Job
	if err := json.Unmarshal(data, &job); err != nil {
		return err
	}

	// Process job
	handler, ok := w.handlers[taskType]
	if !ok {
		return fmt.Errorf("no handler for task type: %s", taskType)
	}

	result, err := handler(ctx, &job)
	if err != nil {
		return err
	}

	// Store result
	resultData, err := json.Marshal(result)
	if err != nil {
		return err
	}

	if err := exec.Set(ctx, libkv.KeyValue{
		Key:   []byte(doneKey),
		Value: resultData,
		TTL:   time.Now().Add(ttl),
	}); err != nil {
		return err
	}

	// Cleanup
	if err := exec.Delete(ctx, []byte(key)); err != nil {
		return err
	}

	return nil
}

func NewOllamaWorker(
	kv libkv.KVManager,
	chat llmexec.LLMChatClient,
	embed llmexec.LLMEmbedClient,
	prompt llmexec.LLMPromptExecClient,
) *Worker {
	return &Worker{
		kv: kv,
		handlers: map[llmexec.TaskType]TaskHandler{
			llmexec.Chat: func(ctx context.Context, job *llmexec.Job) (*llmexec.Output, error) {
				msg, metrics, err := chat.Chat(ctx, job.Messages, job.Args)
				if err != nil {
					return nil, err
				}
				return &llmexec.Output{
					ID:            job.ID,
					TaskType:      job.TaskType,
					OutputMessage: msg,
					Metrics:       metrics,
					Args:          job.Args,
				}, nil
			},
			llmexec.Embed: func(ctx context.Context, job *llmexec.Job) (*llmexec.Output, error) {
				embeddings, err := embed.Embed(ctx, job.Prompt)
				if err != nil {
					return nil, err
				}
				embeddings32 := make([]float32, len(embeddings))
				for i := range embeddings {
					embeddings32[i] = float32(embeddings[i])
				}
				return &llmexec.Output{
					ID:         job.ID,
					TaskType:   job.TaskType,
					Embeddings: embeddings32,
					Args:       job.Args,
				}, nil
			},
			llmexec.Prompt: func(ctx context.Context, job *llmexec.Job) (*llmexec.Output, error) {
				output, metrics, err := prompt.Prompt(ctx, job.Prompt, job.Args)
				if err != nil {
					return nil, err
				}
				return &llmexec.Output{
					ID:       job.ID,
					TaskType: job.TaskType,
					Output:   output,
					Metrics:  metrics,
					Args:     job.Args,
				}, nil
			},
		},
	}
}

func NewVLLMWorker(
	kv libkv.KVManager,
	chat llmexec.LLMChatClient,
	prompt llmexec.LLMPromptExecClient,
) *Worker {
	return &Worker{
		kv: kv,
		handlers: map[llmexec.TaskType]TaskHandler{
			llmexec.Chat: func(ctx context.Context, job *llmexec.Job) (*llmexec.Output, error) {
				msg, metrics, err := chat.Chat(ctx, job.Messages, job.Args)
				if err != nil {
					return nil, err
				}
				return &llmexec.Output{
					ID:            job.ID,
					TaskType:      job.TaskType,
					OutputMessage: msg,
					Metrics:       metrics,
					Args:          job.Args,
				}, nil
			},
			llmexec.Prompt: func(ctx context.Context, job *llmexec.Job) (*llmexec.Output, error) {
				output, metrics, err := prompt.Prompt(ctx, job.Prompt, job.Args)
				if err != nil {
					return nil, err
				}
				return &llmexec.Output{
					ID:       job.ID,
					TaskType: job.TaskType,
					Output:   output,
					Metrics:  metrics,
					Args:     job.Args,
				}, nil
			},
		},
	}
}
