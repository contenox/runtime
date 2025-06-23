package llmexec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/contenox/contenox/libs/libroutine"
	"github.com/js402/contenox/contenox/libkv"
)

var (
	ErrJobPending      = errors.New("job pending")
	ErrInvalidTaskType = errors.New("invalid task type")
)

type TaskType string

const (
	Chat   TaskType = "chat"
	Embed  TaskType = "embed"
	Prompt TaskType = "prompt"
)

type Job struct {
	ID       string    `json:"id"`
	TaskType TaskType  `json:"task_type"`
	Messages []Message `json:"messages"`
	Prompt   string    `json:"prompt"`
	Args
}

type Metrics struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type Args struct {
	MaxOutputTokens int      `json:"max_output_tokens"`
	ProviderTypes   []string `json:"provider_types"`
	ModelNames      []string `json:"model_names"`
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Client interfaces for different capabilities
type LLMChatClient interface {
	Chat(ctx context.Context, Messages []Message, args Args) (Message, Metrics, error)
}

type LLMEmbedClient interface {
	Embed(ctx context.Context, prompt string) ([]float64, error)
}

type LLMStreamClient interface {
	Stream(ctx context.Context, prompt string, args Args) (<-chan string, error)
}

type LLMPromptExecClient interface {
	Prompt(ctx context.Context, prompt string, args Args) (string, Metrics, error)
}

type Output struct {
	ID            string    `json:"id"`
	TaskType      TaskType  `json:"task_type"`
	OutputMessage Message   `json:"output_message"`
	Embeddings    []float32 `json:"embeddings"`
	Prompt        string    `json:"prompt"`
	Output        string    `json:"output"`
	Metrics
	Args
}

type Broker struct {
	kv libkv.KVManager
}

func (b *Broker) executeTask(
	ctx context.Context,
	taskType TaskType,
	id string,
	job Job,
) (*Output, error) {
	execKV, err := b.kv.Operation(ctx)
	if err != nil {
		return nil, err
	}

	pendingKey := fmt.Sprintf("%s:%s:pending", taskType, id)
	if exists, _ := execKV.Exists(ctx, []byte(pendingKey)); exists {
		return nil, ErrJobPending
	}
	inputData, err := json.Marshal(job)
	if err != nil {
		return nil, err
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
	if err != nil {
		return nil, err
	}
	output := &Output{}
	if err := json.Unmarshal(result, output); err != nil {
		return nil, err
	}
	return output, nil
}

func (b *Broker) Chat(ctx context.Context, id string, messages []Message, args Args) (Message, Metrics, error) {
	job := &Job{
		ID:       id,
		Messages: messages,
		Args:     args,
	}
	if job.TaskType != Chat {
		return Message{}, Metrics{}, ErrInvalidTaskType
	}

	result, err := b.executeTask(ctx, Chat, id, *job)
	if err != nil {
		return Message{}, Metrics{}, err
	}

	return result.OutputMessage, result.Metrics, nil
}

func (b *Broker) Embed(ctx context.Context, id string, prompt string) ([]float32, error) {
	job := &Job{
		ID:       id,
		Prompt:   prompt,
		TaskType: Embed,
	}
	result, err := b.executeTask(ctx, Embed, id, *job)
	if err != nil {
		return nil, err
	}

	return result.Embeddings, nil
}

func (b *Broker) Prompt(ctx context.Context, id string, prompt string) (string, Metrics, error) {
	job := &Job{
		ID:       id,
		Prompt:   prompt,
		TaskType: Prompt,
	}
	result, err := b.executeTask(ctx, Prompt, id, *job)
	if err != nil {
		return "", Metrics{}, err
	}

	return result.Output, result.Metrics, nil
}

func NewLLMExecBroker(kv libkv.KVManager) *Broker {
	return &Broker{kv: kv}
}
