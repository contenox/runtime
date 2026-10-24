package chatservice

import (
	"context"
	"fmt"

	"github.com/contenox/runtime/runtime/taskengine"
)

type ChainExecutor interface {
	Execute(ctx context.Context, chain *taskengine.TaskChainDefinition, input any, inputType taskengine.DataType) (any, taskengine.DataType, []taskengine.CapturedStateUnit, error)
}

func CompactHistory(ctx context.Context, exec ChainExecutor, chain *taskengine.TaskChainDefinition, history []taskengine.Message, keep int) ([]taskengine.Message, error) {
	sysEnd := 0
	for sysEnd < len(history) && history[sysEnd].Role == "system" {
		sysEnd++
	}
	if len(history)-sysEnd <= keep {
		return nil, fmt.Errorf("session too short to summarize (have %d non-system messages, keep=%d)", len(history)-sysEnd, keep)
	}
	compactEnd := len(history) - keep
	toCompact := taskengine.ChatHistory{Messages: history[sysEnd:compactEnd]}

	out, _, _, err := exec.Execute(ctx, chain, toCompact, taskengine.DataTypeChatHistory)
	if err != nil {
		return nil, fmt.Errorf("compaction chain failed: %w", err)
	}
	compactHist, ok := out.(taskengine.ChatHistory)
	if !ok || len(compactHist.Messages) == 0 {
		return nil, fmt.Errorf("compaction returned empty result")
	}
	summaryContent := compactHist.Messages[len(compactHist.Messages)-1].Content

	summaryTimestamp := history[compactEnd-1].Timestamp

	spliced := make([]taskengine.Message, 0, sysEnd+1+keep)
	spliced = append(spliced, history[:sysEnd]...)
	spliced = append(spliced, taskengine.Message{
		Role:      "user",
		Content:   fmt.Sprintf("<compact-summary>\n%s\n</compact-summary>", summaryContent),
		Timestamp: summaryTimestamp,
	})
	spliced = append(spliced, history[compactEnd:]...)
	return spliced, nil
}
