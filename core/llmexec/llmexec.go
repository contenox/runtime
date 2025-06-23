package llmexec

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/contenox/contenox/libs/libroutine"
	"github.com/js402/contenox/contenox/libkv"
)

var ErrJobPending = errors.New("job pending")

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type LLMChatClient interface {
	Chat(ctx context.Context, id string, messages []Message) (Message, error)
}

type LLMEmbedClient interface {
	Embed(ctx context.Context, id string, prompt string) ([]float64, error)
}

type LLMPromptExecClient interface {
	Prompt(ctx context.Context, id string, prompt string) (string, error)
}

type LLMExec interface {
	LLMChatClient
	LLMEmbedClient
	LLMPromptExecClient
}

type LLMExecBroker struct {
	kv libkv.KVManager
}

// Chat implements LLMExec.
func (l *LLMExecBroker) Chat(ctx context.Context, id string, messages []Message) (Message, error) {
	execOnKV, err := l.kv.Operation(ctx)
	if err != nil {
		return Message{}, err
	}
	keyPending := []byte("chat:" + id + ":pending")
	jobPending, err := execOnKV.Exists(ctx, keyPending)
	if err != nil {
		return Message{}, err
	}
	if jobPending {
		return Message{}, ErrJobPending
	}
	jobPayload, err := json.Marshal(messages)
	if err != nil {
		return Message{}, err
	}
	err = execOnKV.Set(ctx, libkv.KeyValue{
		Key:   keyPending,
		Value: jobPayload,
		TTL:   time.Now().UTC().Add(time.Minute * 5),
	})
	if err != nil {
		return Message{}, err
	}
	cleanUp := func() {
		execOnKV.Delete(ctx, keyPending)
	}
	defer cleanUp()
	keyDone := []byte("chat:" + id + ":done")
	finalRawMessage := []byte{}
	breaker := libroutine.NewRoutine(10, time.Second*3)
	err = breaker.ExecuteWithRetry(ctx, time.Second, 3, func(ctx context.Context) error {
		done, err := execOnKV.Exists(ctx, keyDone)
		if err != nil {
			return err
		}
		if !done {
			return ErrJobPending
		}
		finalRawMessage, err = execOnKV.Get(ctx, keyDone)
		return err
	})
	if err != nil {
		return Message{}, err
	}
	finalMessage := Message{}
	err = json.Unmarshal(finalRawMessage, &finalMessage)
	if err != nil {
		return Message{}, err
	}
	return finalMessage, nil
}

// Embed implements LLMExec.
func (l *LLMExecBroker) Embed(ctx context.Context, id string, prompt string) ([]float64, error) {
	execOnKV, err := l.kv.Operation(ctx)
	if err != nil {
		return nil, err
	}

	keyPending := []byte("embed:" + id + ":pending")
	jobPending, err := execOnKV.Exists(ctx, keyPending)
	if err != nil {
		return nil, err
	}
	if jobPending {
		return nil, ErrJobPending
	}

	err = execOnKV.Set(ctx, libkv.KeyValue{
		Key:   keyPending,
		Value: []byte(prompt),
		TTL:   time.Now().UTC().Add(time.Minute * 5),
	})
	if err != nil {
		return nil, err
	}

	cleanUp := func() {
		execOnKV.Delete(ctx, keyPending)
	}
	defer cleanUp()

	keyDone := []byte("embed:" + id + ":done")
	var finalResponse []byte

	breaker := libroutine.NewRoutine(10, time.Second*3)
	err = breaker.ExecuteWithRetry(ctx, time.Second, 3, func(ctx context.Context) error {
		done, err := execOnKV.Exists(ctx, keyDone)
		if err != nil {
			return err
		}
		if !done {
			return ErrJobPending
		}
		finalResponse, err = execOnKV.Get(ctx, keyDone)
		return err
	})
	if err != nil {
		return nil, err
	}

	var embeddings []float64
	err = json.Unmarshal(finalResponse, &embeddings)
	if err != nil {
		return nil, err
	}

	return embeddings, nil
}

// Prompt implements LLMExec.
func (l *LLMExecBroker) Prompt(ctx context.Context, id string, prompt string) (string, error) {
	execOnKV, err := l.kv.Operation(ctx)
	if err != nil {
		return "", err
	}

	keyPending := []byte("prompt:" + id + ":pending")
	jobPending, err := execOnKV.Exists(ctx, keyPending)
	if err != nil {
		return "", err
	}
	if jobPending {
		return "", ErrJobPending
	}

	err = execOnKV.Set(ctx, libkv.KeyValue{
		Key:   keyPending,
		Value: []byte(prompt),
		TTL:   time.Now().UTC().Add(time.Minute * 5),
	})
	if err != nil {
		return "", err
	}

	cleanUp := func() {
		execOnKV.Delete(ctx, keyPending)
	}
	defer cleanUp()

	keyDone := []byte("prompt:" + id + ":done")
	var finalResponse []byte

	breaker := libroutine.NewRoutine(10, time.Second*3)
	err = breaker.ExecuteWithRetry(ctx, time.Second, 3, func(ctx context.Context) error {
		done, err := execOnKV.Exists(ctx, keyDone)
		if err != nil {
			return err
		}
		if !done {
			return ErrJobPending
		}
		finalResponse, err = execOnKV.Get(ctx, keyDone)
		return err
	})
	if err != nil {
		return "", err
	}

	return string(finalResponse), nil
}

func NewLLMExecBroker(libkv libkv.KVManager) LLMExec {
	return &LLMExecBroker{
		kv: libkv,
	}
}
