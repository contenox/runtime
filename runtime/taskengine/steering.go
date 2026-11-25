package taskengine

import (
	"context"
	"time"

	"github.com/contenox/runtime/runtime/turninput"
)

type steeringKey struct{}
type steeringSource struct {
	chain string
	next  turninput.Pending
}

// WithSteering supplies user input to the conversational chain. Routing and
// auxiliary chains running under the same request keep their own prompts.
func WithSteering(ctx context.Context, chain string, next turninput.Pending) context.Context {
	return context.WithValue(ctx, steeringKey{}, steeringSource{chain, next})
}

func prepareSteering(ctx context.Context, input *ChatHistory) ([]turninput.Message, func()) {
	source, ok := ctx.Value(steeringKey{}).(steeringSource)
	scope, _ := taskEventScopeFromContext(ctx)
	if !ok || scope.ChainID != source.chain {
		return nil, nil
	}
	batch, delivered := source.next()
	for _, msg := range batch {
		// An automatic follow-up already ends with its first queued message.
		if n := len(input.Messages); n > 0 && input.Messages[n-1].ID == msg.ID {
			continue
		}
		input.Messages = append(input.Messages, Message{
			ID: msg.ID, Role: "user", Content: msg.Text, Images: msg.Images, Audio: msg.Audio, Timestamp: time.Now().UTC(),
		})
		input.InputTokens = 0
	}
	return batch, delivered
}

func containsSteering(history []Message, batch []turninput.Message) bool {
	if len(history) < len(batch) {
		return false
	}
	tail := history[len(history)-len(batch):]
	for i, msg := range batch {
		if tail[i].ID != msg.ID {
			return false
		}
	}
	return true
}
