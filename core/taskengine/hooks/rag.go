package hooks

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/contenox/contenox/core/indexrepo"
	"github.com/contenox/contenox/core/llmrepo"
	"github.com/contenox/contenox/core/serverops/vectors"
	"github.com/contenox/contenox/core/taskengine"
	"github.com/contenox/contenox/libs/libdb"
)

type RagHook struct {
	embedder     llmrepo.ModelRepo
	vectorsStore vectors.Store
	dbInstance   libdb.DBManager
	topK         int
}

func NewRagHook(
	embedder llmrepo.ModelRepo,
	vectorsStore vectors.Store,
	dbInstance libdb.DBManager,
	topK int,
) *RagHook {
	return &RagHook{
		embedder:     embedder,
		vectorsStore: vectorsStore,
		dbInstance:   dbInstance,
		topK:         topK,
	}
}

var _ taskengine.HookRepo = (*RagHook)(nil)

// Supports returns the list of hook names this provider supports.
func (h *RagHook) Supports(ctx context.Context) ([]string, error) {
	return []string{"rag"}, nil
}

// Exec executes the "rag" hook by performing a vector search based on the input string.
func (h *RagHook) Exec(
	ctx context.Context,
	input any,
	dataType taskengine.DataType,
	hook *taskengine.HookCall,
) (int, any, taskengine.DataType, error) {
	// Ensure input is a string
	in, ok := input.(string)
	if !ok && dataType != taskengine.DataTypeString {
		return taskengine.StatusError, nil, taskengine.DataTypeAny, errors.New("input must be a string")
	}

	// Parse optional args from hook.Args
	topK := h.topK
	var epsilon, radius float32

	if hook.Args != nil {
		if kStr := hook.Args["top_k"]; kStr != "" {
			if k, err := strconv.Atoi(kStr); err == nil && k > 0 {
				topK = k
			}
		}
		if eStr := hook.Args["epsilon"]; eStr != "" {
			if e, err := strconv.ParseFloat(eStr, 32); err == nil {
				epsilon = float32(e)
			}
		}
		if rStr := hook.Args["radius"]; rStr != "" {
			if r, err := strconv.ParseFloat(rStr, 32); err == nil {
				radius = float32(r)
			}
		}
	}

	searchArgs := &indexrepo.Args{
		Epsilon: epsilon,
		Radius:  radius,
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
		return taskengine.StatusError, nil, taskengine.DataTypeAny, fmt.Errorf("vector search failed: %w", err)
	}

	// Return both result and its data type
	return taskengine.StatusSuccess, results, taskengine.DataTypeString, nil
}
