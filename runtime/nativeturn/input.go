package nativeturn

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"iter"
	"slices"

	"github.com/contenox/runtime/libacp"
	"github.com/contenox/runtime/runtime/turninput"
	"github.com/google/uuid"
)

// The first invocation uses the original prompt. A subsequent invocation starts a
// follow-up with first as its user message. Both consume input at model boundaries.
type InteractiveFunc func(ctx context.Context, emit func(context.Context, libacp.SessionNotification), first *turninput.Message, pending turninput.Pending) Result

// Enqueue returns false if the turn has already finished. The caller can then
// submit a regular prompt once its outstanding prompt response has arrived.
func (r *Registry) Enqueue(sid libacp.SessionID, input turninput.Message, notice *libacp.SessionUpdate) (bool, error) {
	r.mu.Lock()
	ts := r.sessions[sid]
	closed := r.closed
	r.mu.Unlock()
	if closed {
		return false, ErrClosed
	}
	if ts == nil {
		return false, nil
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if ts.finished {
		return false, nil
	}
	if ts.interactive == nil || ts.turnCtx.Err() != nil {
		return false, errors.New("this turn cannot accept steering")
	}
	input.ID = "steer-" + uuid.NewString()
	ts.pending = append(ts.pending, input)
	ts.notifyInputLocked(input, "queued")
	for update := range AttachmentChunks(input) {
		ts.emitLocked(ts.turnCtx, libacp.SessionNotification{SessionID: sid, Update: update})
	}
	if notice != nil {
		ts.emitLocked(ts.turnCtx, libacp.SessionNotification{SessionID: sid, Update: *notice})
	}
	return true, nil
}

// Peek during prompt construction and acknowledge only at model dispatch.
func (ts *turnSession) nextInput() ([]turninput.Message, func()) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if len(ts.pending) == 0 {
		return nil, nil
	}
	batch := slices.Clone(ts.pending)
	return batch, func() {
		ts.mu.Lock()
		defer ts.mu.Unlock()
		if len(ts.pending) >= len(batch) && ts.pending[0].ID == batch[0].ID {
			clear(ts.pending[:len(batch)])
			ts.pending = ts.pending[len(batch):]
			for _, input := range batch {
				ts.notifyInputLocked(input, "delivered")
			}
		}
	}
}

// AttachmentChunks gives live and replayed attachments the same position within
// their message, including when two attachments have identical bytes.
func AttachmentChunks(input turninput.Message) iter.Seq[libacp.SessionUpdate] {
	return func(yield func(libacp.SessionUpdate) bool) {
		part := func(index int, block libacp.ContentBlock) bool {
			meta, _ := json.Marshal(map[string]int{"contenox.attachmentIndex": index})
			return yield(libacp.SessionUpdate{SessionUpdate: libacp.SessionUpdateUserMessageChunk, MessageID: input.ID, Content: &block, Meta: meta})
		}
		for i, img := range input.Images {
			if !part(i, libacp.NewImageContent(base64.StdEncoding.EncodeToString(img.Data), img.MimeType)) {
				return
			}
		}
		for i, clip := range input.Audio {
			if !part(i, libacp.NewAudioContent(base64.StdEncoding.EncodeToString(clip.Data), clip.MimeType)) {
				return
			}
		}
	}
}

func (ts *turnSession) notifyInputLocked(input turninput.Message, status string) {
	update := libacp.NewUserMessageChunk(input.Text)
	update.MessageID = input.ID
	update.Meta, _ = json.Marshal(map[string]string{"contenox.delivery": status})
	ts.emitLocked(context.WithoutCancel(ts.turnCtx), libacp.SessionNotification{SessionID: ts.sessionID, Update: update})
}
