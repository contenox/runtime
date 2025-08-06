package hooks

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	libdb "github.com/contenox/dbexec"
	"github.com/contenox/runtime-mvp/core/indexrepo"
	"github.com/contenox/runtime-mvp/core/serverops/vectors"
	"github.com/contenox/runtime/llmrepo"
	"github.com/contenox/runtime/taskengine"
)

type Search struct {
	embedder     llmrepo.ModelRepo
	vectorsStore vectors.Store
	dbInstance   libdb.DBManager
}

func NewSearch(
	embedder llmrepo.ModelRepo,
	vectorsStore vectors.Store,
	dbInstance libdb.DBManager,
) taskengine.HookRepo {
	return &Search{
		embedder:     embedder,
		vectorsStore: vectorsStore,
		dbInstance:   dbInstance,
	}
}

var _ taskengine.HookRepo = (*Search)(nil)

// Supports returns the list of hook names this provider supports.
func (h *Search) Supports(ctx context.Context) ([]string, error) {
	return []string{"vector_search"}, nil
}

// Exec executes the "search" hook by performing a vector search based on the input string.
func (h *Search) Exec(
	ctx context.Context,
	startTime time.Time,
	input any,
	dataType taskengine.DataType,
	transition string,
	hook *taskengine.HookCall,
) (int, any, taskengine.DataType, string, error) {
	if input == nil {
		return taskengine.StatusError, nil, taskengine.DataTypeAny, transition, errors.New("input must be a string")
	}
	if hook == nil {
		return taskengine.StatusError, nil, taskengine.DataTypeAny, transition, errors.New("SERVER BUG: hook must be provided")
	}
	// Ensure input is a string
	in, ok := input.(string)
	if !ok && dataType != taskengine.DataTypeString {
		return taskengine.StatusError, nil, taskengine.DataTypeAny, transition, errors.New("input must be a string")
	}

	topK := 1
	var epsilon, radius float32
	var argsSet bool
	if hook.Args != nil {
		if kStr := hook.Args["top_k"]; kStr != "" {
			if k, err := strconv.Atoi(kStr); err == nil && k > 0 {
				topK = k
			} else {
				return taskengine.StatusError, nil, taskengine.DataTypeAny, transition, errors.New("top_k must be a positive integer")
			}
			if topK <= 0 {
				return taskengine.StatusError, nil, taskengine.DataTypeAny, transition, errors.New("top_k must be a positive integer")
			}
		}
		if eStr := hook.Args["epsilon"]; eStr != "" {
			if e, err := strconv.ParseFloat(eStr, 32); err == nil {
				argsSet = true
				epsilon = float32(e)
			} else {
				return taskengine.StatusError, nil, taskengine.DataTypeAny, transition, errors.New("epsilon must be a float")
			}
		}
		if rStr := hook.Args["radius"]; rStr != "" {
			if r, err := strconv.ParseFloat(rStr, 32); err == nil {
				if !argsSet {
					return taskengine.StatusError, nil, taskengine.DataTypeAny, transition, errors.New("radius requires epsilon")
				}
				radius = float32(r)
			} else {
				return taskengine.StatusError, nil, taskengine.DataTypeAny, transition, errors.New("radius must be a float")
			}
		}
	}
	var searchArgs *indexrepo.Args
	if argsSet {
		searchArgs = &indexrepo.Args{
			Epsilon: epsilon,
			Radius:  radius,
		}
	}

	results, err := indexrepo.ExecuteVectorSearch(
		ctx,
		h.embedder,
		h.vectorsStore,
		h.dbInstance.WithoutTransaction(),
		[]string{in},
		topK,
		searchArgs,
	)
	if err != nil {
		return taskengine.StatusError, nil, taskengine.DataTypeAny, transition, fmt.Errorf("vector search failed: %w", err)
	}
	convertedResults := make([]taskengine.SearchResult, len(results))
	for i := range results {
		convertedResults[i] = taskengine.SearchResult{
			ID:           results[i].ID,
			Distance:     results[i].Distance,
			ResourceType: results[i].ResourceType,
		}
	}

	return taskengine.StatusSuccess, convertedResults, taskengine.DataTypeSearchResults, fmt.Sprint(len(results)), nil
}
