package hookrecipes

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/contenox/runtime-mvp/core/taskengine"
)

func NewSearchThenResolveHook(searchThenResolveHook SearchThenResolveHook) taskengine.HookRepo {
	return &SearchThenResolveHook{
		SearchHook:     searchThenResolveHook.SearchHook,
		ResolveHook:    searchThenResolveHook.ResolveHook,
		DefaultTopK:    searchThenResolveHook.DefaultTopK,
		DefaultDist:    searchThenResolveHook.DefaultDist,
		DefaultPos:     searchThenResolveHook.DefaultPos,
		DefaultEpsilon: searchThenResolveHook.DefaultEpsilon,
		DefaultRadius:  searchThenResolveHook.DefaultRadius,
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
}

var _ taskengine.HookRepo = (*SearchThenResolveHook)(nil)

func (r *SearchThenResolveHook) Supports(ctx context.Context) ([]string, error) {
	return []string{"search_knowledge"}, nil
}

func (r *SearchThenResolveHook) Exec(
	ctx context.Context,
	startTime time.Time,
	input any,
	dataType taskengine.DataType,
	transition string,
	hook *taskengine.HookCall,
) (int, any, taskengine.DataType, string, error) {
	topK := r.DefaultTopK
	if kStr := hook.Args["top_k"]; kStr != "" {
		if k, err := strconv.Atoi(kStr); err == nil && k > 0 {
			topK = k
		} else {
			return taskengine.StatusError, nil, dataType, transition, errors.New("top_k must be a positive integer")
		}
	}

	epsilon := r.DefaultEpsilon
	if eStr := hook.Args["epsilon"]; eStr != "" {
		if e, err := strconv.ParseFloat(eStr, 64); err == nil && e >= 0 {
			epsilon = e
		} else {
			return taskengine.StatusError, nil, dataType, transition, errors.New("epsilon must be a non-negative number")
		}
	}

	distance := r.DefaultDist
	if dStr := hook.Args["distance"]; dStr != "" {
		if d, err := strconv.ParseFloat(dStr, 64); err == nil {
			distance = float64(d)
		} else {
			return taskengine.StatusError, nil, dataType, transition, errors.New("distance must be a valid number")
		}
	}

	position := r.DefaultPos
	if pStr := hook.Args["position"]; pStr != "" {
		if p, err := strconv.Atoi(pStr); err == nil && p >= 0 {
			position = p
		} else {
			return taskengine.StatusError, nil, dataType, transition, errors.New("position must be a non-negative integer")
		}
	}

	radius := r.DefaultRadius
	if rStr := hook.Args["radius"]; rStr != "" {
		if r, err := strconv.ParseFloat(rStr, 64); err == nil && r >= 0 {
			radius = float64(r)
		} else {
			return taskengine.StatusError, nil, dataType, transition, errors.New("radius must be a non-negative number")
		}
	}

	_, ok := input.(string)
	if !ok && dataType != taskengine.DataTypeString {
		return taskengine.StatusError, nil, taskengine.DataTypeAny, transition, errors.New("input must be a string")
	}
	if inStr, ok := input.(string); ok {
		inStr, parsedArgs, err := ParsePrefixedArgs(inStr)
		if err != nil {
			return taskengine.StatusError, nil, dataType, transition, err
		}
		input = inStr // updated input after trimming parsed args

		// Override args
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
	}

	status, out, typ, trans, err := r.SearchHook.Exec(
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
		return status, out, typ, trans, fmt.Errorf("search failed: %w", err)
	}

	status, out, typ, trans, err = r.ResolveHook.Exec(
		ctx,
		startTime,
		out,
		typ,
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
		return status, out, typ, trans, fmt.Errorf("resolve failed: %w", err)
	}

	return status, out, typ, trans, nil
}
