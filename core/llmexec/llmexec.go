package llmexec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/contenox/contenox/core/serverops"
	"github.com/contenox/contenox/libs/libroutine"
	"github.com/js402/contenox/contenox/libkv"
)

var ErrJobPending = errors.New("job pending")

type TaskType string

const (
	Chat   TaskType = "chat"
	Embed  TaskType = "embed"
	Prompt TaskType = "prompt"
)

type Args struct {
	MaxOutputTokens int      `json:"max_output_tokens"`
	ProviderTypes   []string `json:"provider_types"`
	ModelNames      []string `json:"model_names"`
}

type LLMExecBroker struct {
	kv libkv.KVManager
}

func (b *LLMExecBroker) executeTask(
	ctx context.Context,
	taskType TaskType,
	id string,
	inputData []byte,
) ([]byte, error) {
	execKV, err := b.kv.Operation(ctx)
	if err != nil {
		return nil, err
	}

	pendingKey := fmt.Sprintf("%s:%s:pending", taskType, id)
	if exists, _ := execKV.Exists(ctx, []byte(pendingKey)); exists {
		return nil, ErrJobPending
	}

	if err := execKV.Set(ctx, libkv.KeyValue{
		Key:   []byte(pendingKey),
		Value: inputData,
		TTL:   time.Now().UTC().Add(5 * time.Minute),
	}); err != nil {
		return nil, err
	}
	defer execKV.Delete(ctx, []byte(pendingKey))

	doneKey := fmt.Sprintf("%s:%s:done", taskType, id)
	var result []byte

	breaker := libroutine.NewRoutine(10, 3*time.Second)
	err = breaker.ExecuteWithRetry(ctx, time.Second, 3, func(ctx context.Context) error {
		if exists, _ := execKV.Exists(ctx, []byte(doneKey)); !exists {
			return ErrJobPending
		}
		result, err = execKV.Get(ctx, []byte(doneKey))
		return err
	})

	return result, err
}

func (b *LLMExecBroker) Chat(ctx context.Context, id string, messages []serverops.Message) (serverops.Message, error) {
	data, err := json.Marshal(messages)
	if err != nil {
		return serverops.Message{}, err
	}

	result, err := b.executeTask(ctx, Chat, id, data)
	if err != nil {
		return serverops.Message{}, err
	}

	var msg serverops.Message
	return msg, json.Unmarshal(result, &msg)
}

func (b *LLMExecBroker) Embed(ctx context.Context, id string, prompt string) ([]float64, error) {
	result, err := b.executeTask(ctx, Embed, id, []byte(prompt))
	if err != nil {
		return nil, err
	}

	var embeddings []float64
	return embeddings, json.Unmarshal(result, &embeddings)
}

func (b *LLMExecBroker) Prompt(ctx context.Context, id string, prompt string) (string, error) {
	result, err := b.executeTask(ctx, Prompt, id, []byte(prompt))
	return string(result), err
}

func NewLLMExecBroker(kv libkv.KVManager) *LLMExecBroker {
	return &LLMExecBroker{kv: kv}
}
