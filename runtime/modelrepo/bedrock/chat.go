package bedrock

import (
	"context"
	"fmt"

	"github.com/contenox/runtime/runtime/modelrepo"
)

type bedrockChatClient struct{ bedrockClient }

func (c *bedrockChatClient) Chat(ctx context.Context, messages []modelrepo.Message, args ...modelrepo.ChatArgument) (modelrepo.ChatResult, error) {
	reportErr, reportChange, end := c.tracker.Start(ctx, "chat", "bedrock", "model", c.model.name)
	defer end()

	in, toOriginal, err := buildConverseInput(c.model, messages, chatConfigFromArgs(args))
	if err != nil {
		reportErr(err)
		return modelrepo.ChatResult{}, err
	}
	out, err := c.api.Converse(ctx, in)
	if err != nil {
		err = classifyBedrockError(fmt.Errorf("bedrock converse (model=%s): %w", c.model.name, err))
		reportErr(err)
		return modelrepo.ChatResult{}, err
	}
	res, err := decodeConverse(c.model, out, toOriginal)
	if err != nil {
		reportErr(err)
		return modelrepo.ChatResult{}, err
	}
	reportChange("chat_completed", res)
	return res, nil
}

var _ modelrepo.LLMChatClient = (*bedrockChatClient)(nil)
