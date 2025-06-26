package hooks

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/contenox/runtime-mvp/core/serverops/store"
	"github.com/contenox/runtime-mvp/core/taskengine"
	"github.com/contenox/runtime-mvp/libs/libdb"
)

type SearchResolve struct {
	dbInstance libdb.DBManager
}

// Exec implements taskengine.HookRepo.
func (s *SearchResolve) Exec(ctx context.Context, startTime time.Time, input any, dataType taskengine.DataType, transition string, args *taskengine.HookCall) (int, any, taskengine.DataType, string, error) {
	if dataType != taskengine.DataTypeSearchResults {
		return taskengine.StatusError, nil, dataType, transition, fmt.Errorf("unsupported data type: %v", dataType)
	}
	in, ok := input.([]taskengine.SearchResult)
	if !ok {
		return taskengine.StatusError, nil, dataType, transition, fmt.Errorf("SERVER BUG: invalid input type")
	}
	if len(in) == 0 {
		return taskengine.StatusError, nil, dataType, transition, fmt.Errorf("no results found")
	}
	storeInstance := store.New(s.dbInstance.WithoutTransaction())
	var distanceF *float32
	if distance, ok := args.Args["distance"]; ok {
		conv, err := strconv.ParseFloat(distance, 64)
		if err != nil {
			return taskengine.StatusError, nil, dataType, transition, fmt.Errorf("invalid distance: %v", err)
		}
		a := float32(conv)
		distanceF = &a
	}
	position := 0
	if positionArg, ok := args.Args["position"]; ok {
		a, err := strconv.ParseInt(positionArg, 10, 64)
		if err != nil {
			return taskengine.StatusError, nil, dataType, transition, fmt.Errorf("invalid position: %v", err)
		}
		position = int(a)
	}
	if position >= len(in) {
		return taskengine.StatusError, nil, dataType, transition, fmt.Errorf("position out of range")
	}
	if distanceF != nil && in[position].Distance > *distanceF {
		return taskengine.StatusError, nil, dataType, "", fmt.Errorf("distance too large")
	}

	file, err := storeInstance.GetFileByID(ctx, in[position].ID)
	if err != nil {
		return taskengine.StatusError, nil, dataType, transition, fmt.Errorf("failed to get file: %v", err)
	}
	blob, err := storeInstance.GetBlobByID(ctx, file.BlobsID)
	if err != nil {
		return taskengine.StatusError, nil, dataType, transition, fmt.Errorf("failed to get blob: %v", err)
	}
	if file.Type == "application/json" {
		return taskengine.StatusSuccess, blob.Data, taskengine.DataTypeJSON, "application/json", nil
	}
	if file.Type == "text/plain" || file.Type == "text/plain; charset=utf-8" {
		return taskengine.StatusSuccess, string(blob.Data), taskengine.DataTypeString, "text/plain", nil
	}

	return taskengine.StatusSuccess, blob.Data, taskengine.DataTypeAny, "any", nil
}

// Supports implements taskengine.HookRepo.
func (s *SearchResolve) Supports(ctx context.Context) ([]string, error) {
	return []string{"resolve_search_result"}, nil
}

func NewSearchResolveHook(dbInstance libdb.DBManager) *SearchResolve {
	return &SearchResolve{
		dbInstance: dbInstance,
	}
}

var _ taskengine.HookRepo = (*SearchResolve)(nil)
