package activity

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/contenox/runtime-mvp/core/serverops"
	"github.com/contenox/runtime-mvp/libs/libkv"
	"github.com/google/uuid"
)

type KVActivityTracker struct {
	kvManager libkv.KVManager
}

func NewKVActivityTracker(kvManager libkv.KVManager) *KVActivityTracker {
	return &KVActivityTracker{
		kvManager: kvManager,
	}
}

type TrackedEvent struct {
	ID         string            `json:"id"`
	Operation  string            `json:"operation"`
	Subject    string            `json:"subject"`
	Start      time.Time         `json:"start"`
	End        *time.Time        `json:"end,omitempty"`
	Error      *string           `json:"error,omitempty"`
	EntityID   *string           `json:"entityID,omitempty"`
	EntityData any               `json:"entityData,omitempty"`
	DurationMS *int64            `json:"durationMS,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
	RequestID  string            `json:"requestID,omitempty"`
}

type TrackedRequest struct {
	ID        string `json:"id"`
	HasError  bool   `json:"hasError,omitempty"`
	HasChange bool   `json:"hasChange,omitempty"`
}

func (t *KVActivityTracker) Start(
	ctx context.Context,
	operation string,
	subject string,
	kvArgs ...any,
) (func(error), func(string, any), func()) {
	startTime := time.Now().UTC()
	metadata := extractMetadata(kvArgs...)

	// Initialize event with start information
	event := &TrackedEvent{
		ID:        uuid.New().String(),
		Operation: operation,
		Subject:   subject,
		Start:     startTime,
		Metadata:  metadata,
	}
	if reqID, ok := ctx.Value(serverops.ContextKeyRequestID).(string); ok {
		event.RequestID = reqID
	}
	hasError := false
	// Define lifecycle handlers
	reportErr := func(err error) {
		if err != nil {
			errStr := err.Error()
			event.Error = &errStr
			hasError = true
		}
	}
	reportedChange := false
	reportChange := func(id string, data any) {
		event.EntityID = &id
		event.EntityData = data
		reportedChange = true
	}

	end := func() {
		now := time.Now().UTC()
		event.End = &now
		duration := now.Sub(startTime).Milliseconds()
		event.DurationMS = &duration

		// Prepare event for storage
		data, err := json.Marshal(event)
		if err != nil {
			log.Printf("SERVERBUG: Failed to marshal activity event: %v", err)
			return
		}

		// Store in key-value system
		kv, err := t.kvManager.Operation(ctx)
		if err != nil {
			log.Printf("SERVERBUG: Failed to get KV operation: %v", err)
			return
		}

		// Push to activity log and trim
		if err := kv.LPush(ctx, []byte("activity:log"), data); err != nil {
			log.Printf("SERVERBUG: Failed to push activity event: %v", err)
		}

		// Maintain last 1000 events
		if err := kv.LTrim(ctx, []byte("activity:log"), 0, 999); err != nil {
			log.Printf("SERVERBUG: Failed to trim activity log: %v", err)
		}
		if event.RequestID != "" {
			reqKey := []byte("activity:request:" + event.RequestID)
			if err := kv.LPush(ctx, reqKey, data); err != nil {
				log.Printf("SERVERBUG: Failed to push requestID activity event: %v", err)
			}
			trackedRequest := TrackedRequest{
				ID:        event.RequestID,
				HasError:  hasError,
				HasChange: reportedChange,
			}
			treq, err := json.Marshal(trackedRequest)
			if err != nil {
				log.Printf("SERVERBUG: Failed to marshal tracked request: %v", err)
			}

			if err := kv.SAdd(ctx, []byte("activity:requests"), treq); err != nil {
				log.Printf("SERVERBUG: Failed to track requestID: %v", err)
			}
		}
	}

	return reportErr, reportChange, end
}

func extractMetadata(args ...any) map[string]string {
	meta := make(map[string]string)
	for i := 0; i+1 < len(args); i += 2 {
		key, okKey := args[i].(string)
		val, okVal := args[i+1].(string)
		if okKey && okVal {
			meta[key] = val
		}
	}
	return meta
}

func (t *KVActivityTracker) GetRecentRequestIDs(ctx context.Context, limit int) ([]TrackedRequest, error) {
	if limit <= 0 {
		limit = 100
	}

	kv, err := t.kvManager.Operation(ctx)
	if err != nil {
		return nil, err
	}

	rawItems, err := kv.SMembers(ctx, []byte("activity:requests"))
	if err != nil {
		return nil, err
	}

	var requestIDs []TrackedRequest
	seen := make(map[string]struct{}, len(rawItems))

	for _, raw := range rawItems {
		var req TrackedRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			log.Printf("SERVERBUG: Failed to unmarshal tracked request: %v", err)
			continue
		}
		if _, exists := seen[req.ID]; !exists {
			seen[req.ID] = struct{}{}
			requestIDs = append(requestIDs, req)
		}
	}

	return requestIDs, nil
}

func (t *KVActivityTracker) GetActivityLogs(ctx context.Context, limit int) ([]TrackedEvent, error) {
	kv, err := t.kvManager.Operation(ctx)
	if err != nil {
		return nil, err
	}

	if limit <= 0 {
		limit = 100
	}

	// Get list length
	listLen, err := kv.LLen(ctx, []byte("activity:log"))
	if err != nil {
		return nil, err
	}

	// Determine range
	start := int64(0)
	stop := int64(limit - 1)
	if listLen < stop+1 {
		stop = listLen - 1
	}

	rawItems, err := kv.LRange(ctx, []byte("activity:log"), start, stop)
	if err != nil {
		return nil, err
	}

	var results []TrackedEvent
	for _, raw := range rawItems {
		var evt TrackedEvent
		if err := json.Unmarshal(raw, &evt); err == nil {
			results = append(results, evt)
		}
	}

	return results, nil
}

func (t *KVActivityTracker) GetActivityLogsByRequestID(ctx context.Context, requestID string) ([]TrackedEvent, error) {
	if requestID == "" {
		return nil, nil
	}

	kv, err := t.kvManager.Operation(ctx)
	if err != nil {
		return nil, err
	}

	key := []byte("activity:request:" + requestID)

	rawItems, err := kv.LRange(ctx, key, 0, -1)
	if err != nil {
		return nil, err
	}

	var results []TrackedEvent
	for _, raw := range rawItems {
		var evt TrackedEvent
		if err := json.Unmarshal(raw, &evt); err == nil {
			results = append(results, evt)
		}
	}

	return results, nil
}
