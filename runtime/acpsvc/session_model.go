package acpsvc

import (
	"context"
	"encoding/json"
	"strings"

	libacp "github.com/contenox/runtime/libacp"
	"github.com/contenox/runtime/runtime/runtimetypes"
)

const acpSessionModelKVPrefix = "acp:session_model:"

type sessionModelRecord struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

func (t *Transport) persistSessionModel(ctx context.Context, sid libacp.SessionID, provider, model string) {
	if t.deps.DB == nil || sid == "" || strings.TrimSpace(model) == "" {
		return
	}
	raw, err := json.Marshal(sessionModelRecord{Provider: provider, Model: model})
	if err != nil {
		return
	}
	store := runtimetypes.New(t.deps.DB.WithoutTransaction())
	if err := store.SetKV(ctx, acpSessionModelKVPrefix+string(sid), raw); err != nil {
		reportErr, _, end := t.tracker().Start(ctx, "persist_model", "acp_session", "session_id", string(sid))
		reportErr(err)
		end()
	}
}

func (t *Transport) persistSessionModelFor(ctx context.Context, sess *sessionEntry) {
	if sess == nil {
		return
	}
	sid, ok := t.acpSessionForContenoxID(sess.InternalSessionID)
	if !ok {
		return
	}
	t.persistSessionModel(ctx, sid, sess.providerOrDefault(t.provider()), sess.modelOrDefault(t.model()))
}

func (t *Transport) restoreSessionModel(ctx context.Context, store runtimetypes.Store, sid libacp.SessionID, entry *sessionEntry) {
	var rec sessionModelRecord
	if err := store.GetKV(ctx, acpSessionModelKVPrefix+string(sid), &rec); err != nil {
		return
	}
	if strings.TrimSpace(rec.Model) == "" {
		return
	}
	entry.setModelSelection(rec.Provider, rec.Model)
}
