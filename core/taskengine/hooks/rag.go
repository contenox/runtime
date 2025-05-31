package hooks

import (
	"context"

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

func NewRagHook(embedder llmrepo.ModelRepo, vectorsStore vectors.Store, dbInstance libdb.DBManager, topK int) *RagHook {
	return &RagHook{
		embedder:     embedder,
		vectorsStore: vectorsStore,
		dbInstance:   dbInstance,
		topK:         topK,
	}
}

var _ taskengine.HookRepo = (*RagHook)(nil)

func (h *RagHook) Exec(ctx context.Context, hook *taskengine.HookCall) (int, any, error) {
	data, err := indexrepo.ResolveBlobFromQuery(ctx, h.embedder, h.vectorsStore, h.dbInstance.WithoutTransaction(), hook.Input, h.topK)
	if err != nil {
		return 0, nil, err
	}

	return taskengine.StatusSuccess, data, nil
}
