package hookrecipes

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/contenox/activitytracker"
	"github.com/contenox/runtime-mvp/core/taskengine"
)

const name = "search_knowledge"

// NewSearchThenResolveHook creates a new SearchThenResolveHook with optional ActivityTracker
func NewSearchThenResolveHook(
	searchThenResolveHook SearchThenResolveHook,
	tracker activitytracker.ActivityTracker,
) taskengine.HookRepo {
	if tracker == nil {
		tracker = activitytracker.NoopTracker{}
	}
	return &SearchThenResolveHook{
		SearchHook:     searchThenResolveHook.SearchHook,
		ResolveHook:    searchThenResolveHook.ResolveHook,
		DefaultTopK:    searchThenResolveHook.DefaultTopK,
		DefaultDist:    searchThenResolveHook.DefaultDist,
		DefaultPos:     searchThenResolveHook.DefaultPos,
		DefaultEpsilon: searchThenResolveHook.DefaultEpsilon,
		DefaultRadius:  searchThenResolveHook.DefaultRadius,
		tracker:        tracker,
	}
}

// SearchThenResolveHook is a recipe that chains:
type SearchThenResolveHook struct {
	SearchHook     taskengine.HookRepo
	ResolveHook    taskengine.HookRepo
	DefaultTopK    int
	DefaultDist    float64
	DefaultPos     int
	DefaultEpsilon float64
	DefaultRadius  float64
	tracker        activitytracker.ActivityTracker
}

var _ taskengine.HookRepo = (*SearchThenResolveHook)(nil)

func (r *SearchThenResolveHook) Supports(ctx context.Context) ([]string, error) {
	return []string{name}, nil
}

func (r *SearchThenResolveHook) Exec(
	ctx context.Context,
	startTime time.Time,
	input any,
	dataType taskengine.DataType,
	transition string,
	hook *taskengine.HookCall,
) (int, any, taskengine.DataType, string, error) {
	reportErr, _, end := r.tracker.Start(ctx, "exec", "search_then_resolve", "hook_type", "search_knowledge")
	defer end()

	topK := r.DefaultTopK
	if kStr := hook.Args["top_k"]; kStr != "" {
		if k, err := strconv.Atoi(kStr); err == nil && k > 0 {
			topK = k
		} else {
			reportErr(errors.New("invalid top_k"))
			return taskengine.StatusError, nil, dataType, transition, errors.New("top_k must be a positive integer")
		}
	}

	epsilon := r.DefaultEpsilon
	if eStr := hook.Args["epsilon"]; eStr != "" {
		if e, err := strconv.ParseFloat(eStr, 64); err == nil && e >= 0 {
			epsilon = e
		} else {
			reportErr(errors.New("invalid epsilon"))
			return taskengine.StatusError, nil, dataType, transition, errors.New("epsilon must be a non-negative number")
		}
	}

	distance := r.DefaultDist
	if dStr := hook.Args["distance"]; dStr != "" {
		if d, err := strconv.ParseFloat(dStr, 64); err == nil {
			distance = d
		} else {
			reportErr(errors.New("invalid distance"))
			return taskengine.StatusError, nil, dataType, transition, errors.New("distance must be a valid number")
		}
	}

	position := r.DefaultPos
	if pStr := hook.Args["position"]; pStr != "" {
		if p, err := strconv.Atoi(pStr); err == nil && p >= 0 {
			position = p
		} else {
			reportErr(errors.New("invalid position"))
			return taskengine.StatusError, nil, dataType, transition, errors.New("position must be a non-negative integer")
		}
	}

	radius := r.DefaultRadius
	if rStr := hook.Args["radius"]; rStr != "" {
		if r, err := strconv.ParseFloat(rStr, 64); err == nil && r >= 0 {
			radius = r
		} else {
			reportErr(errors.New("invalid radius"))
			return taskengine.StatusError, nil, dataType, transition, errors.New("radius must be a non-negative number")
		}
	}

	// Unified prompt extraction function
	getPrompt := func() (string, error) {
		switch dataType {
		case taskengine.DataTypeString:
			prompt, ok := input.(string)
			if !ok {
				return "", fmt.Errorf("SEVERBUG: input is not a string")
			}
			return prompt, nil
		case taskengine.DataTypeChatHistory:
			history, ok := input.(taskengine.ChatHistory)
			if !ok {
				return "", fmt.Errorf("SEVERBUG: input is not a chat history")
			}
			if len(history.Messages) == 0 {
				return "", fmt.Errorf("SEVERBUG: chat history is empty")
			}
			return history.Messages[len(history.Messages)-1].Content, nil
		default:
			return "", fmt.Errorf("unsupported input type %v", dataType.String())
		}
	}

	inStr, err := getPrompt()
	if err != nil {
		reportErr(err)
		return taskengine.StatusError, nil, dataType, transition, err
	}
	var parsedArgs map[string]string
	inStrParsed, args, err := ParsePrefixedArgs(inStr)
	if err != nil {
		reportErr(err)
		return taskengine.StatusError, nil, dataType, transition, err
	}
	input = inStrParsed
	parsedArgs = args

	// Override args from parsed input
	if v, ok := parsedArgs["top_k"]; ok {
		if k, err := strconv.Atoi(v); err == nil && k > 0 {
			topK = k
		}
	}
	if v, ok := parsedArgs["epsilon"]; ok {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 {
			epsilon = f
		}
	}
	if v, ok := parsedArgs["distance"]; ok {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			distance = f
		}
	}
	if v, ok := parsedArgs["position"]; ok {
		if p, err := strconv.Atoi(v); err == nil && p >= 0 {
			position = p
		}
	}
	if v, ok := parsedArgs["radius"]; ok {
		if r, err := strconv.ParseFloat(v, 64); err == nil && r >= 0 {
			radius = r
		}
	}

	// 🔍 Run SearchHook
	status, out, outType, trans, err := r.SearchHook.Exec(
		ctx,
		startTime,
		input,
		dataType,
		transition,
		&taskengine.HookCall{
			Type: "search",
			Args: map[string]string{
				"top_k":    strconv.Itoa(topK),
				"epsilon":  strconv.FormatFloat(epsilon, 'f', -1, 64),
				"radius":   strconv.FormatFloat(radius, 'f', -1, 64),
				"distance": strconv.FormatFloat(distance, 'f', -1, 64),
			},
		},
	)

	if status != taskengine.StatusSuccess || err != nil {
		reportErr(fmt.Errorf("search failed: %w", err))
		return status, out, outType, trans, fmt.Errorf("search failed: %w", err)
	}

	status, out, outType, trans, err = r.ResolveHook.Exec(
		ctx,
		startTime,
		out,
		outType,
		trans,
		&taskengine.HookCall{
			Type: "resolve_search_result",
			Args: map[string]string{
				"distance": strconv.FormatFloat(distance, 'f', -1, 64),
				"position": strconv.Itoa(position),
			},
		},
	)

	if status != taskengine.StatusSuccess || err != nil {
		reportErr(fmt.Errorf("resolve failed: %w", err))
		return status, out, outType, trans, fmt.Errorf("resolve failed: %w", err)
	}

	return status, out, outType, name, nil
}
