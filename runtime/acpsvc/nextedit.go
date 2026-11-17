package acpsvc

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/contenox/runtime/internal/modeld/admission"
	protocolv1 "github.com/contenox/runtime/internal/protocol/v1"
	libacp "github.com/contenox/runtime/libacp"
	"github.com/contenox/runtime/runtime/codecompletion"
	"github.com/contenox/runtime/runtime/inferencesetup"
	"github.com/contenox/runtime/runtime/modelcontract"
	"github.com/google/uuid"
)

const extMethodNextEdit = "_contenox/next_edit"

// nextEditResult is the prediction one inference produced, before it is mapped
// onto its declared message.
type nextEditResult struct {
	Kind        string
	Version     int
	Edits       []modelcontract.TextEdit
	Fingerprint string
}

// nextEditResultWire maps one prediction onto its declared message. A cancelled
// prediction carries the version it was predicted against and no edits, because
// ACP's own vocabulary has no cancellation answer for this operation.
func nextEditResultWire(result nextEditResult) *protocolv1.NextEditResult {
	edits := make([]*protocolv1.NextEditTextEdit, 0, len(result.Edits))
	for _, edit := range result.Edits {
		edits = append(edits, &protocolv1.NextEditTextEdit{
			Start: int32(edit.Start), End: int32(edit.End),
			OldText: edit.OldText, NewText: edit.NewText,
		})
	}
	kind := "edits"
	if result.Kind == "cancelled" {
		kind = "cancelled"
	}
	return &protocolv1.NextEditResult{
		Kind: kind, Version: int32(result.Version), Edits: edits, Fingerprint: result.Fingerprint,
	}
}

// nextEditInput decodes a declared request into the model contract's own input.
func nextEditInput(p *protocolv1.NextEditRequest) modelcontract.NextEditInput {
	history := make([]modelcontract.EditHistory, 0, len(p.GetHistory()))
	for _, entry := range p.GetHistory() {
		history = append(history, modelcontract.EditHistory{Path: entry.GetPath(), Before: entry.GetBefore(), After: entry.GetAfter()})
	}
	context := make([]modelcontract.ContextFile, 0, len(p.GetContext()))
	for _, file := range p.GetContext() {
		context = append(context, modelcontract.ContextFile{Path: file.GetPath(), Text: file.GetText()})
	}
	return modelcontract.NextEditInput{
		Path: p.GetPath(), Before: p.GetBefore(), Current: p.GetCurrent(),
		Cursor: int(p.GetCursor()), Start: int(p.GetStart()), End: int(p.GetEnd()),
		History: history, Context: context,
	}
}

func (t *Transport) handleNextEdit(ctx context.Context, params json.RawMessage) (json.RawMessage, *libacp.Error) {
	if t.deps.Engine == nil || t.deps.OpenInference == nil {
		return nil, libacp.InternalError("engine is not configured")
	}
	p := &protocolv1.NextEditRequest{}
	if rpcErr := unmarshalProtocolRequest("next edit", params, p); rpcErr != nil {
		return nil, rpcErr
	}
	if p.GetClientId() == "" || p.GetDocumentId() == "" || p.GetVersion() <= 0 {
		return nil, libacp.NewError(libacp.ErrInvalidParams, "next edit requires client, document and version identity")
	}
	if len(p.GetCurrent()) > 2*1024*1024 || len(p.GetBefore()) > 2*1024*1024 {
		return nil, libacp.NewError(libacp.ErrInvalidParams, "next-edit snapshots exceed 2 MiB")
	}
	input := nextEditInput(p)
	if err := input.Validate(); err != nil {
		return nil, libacp.NewError(libacp.ErrInvalidParams, err.Error())
	}
	result, err := t.predictNextEdit(ctx, p, input)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, admission.ErrSuperseded) || errors.Is(err, codecompletion.ErrSuperseded) {
			return marshalProtocol(nextEditResultWire(nextEditResult{Kind: "cancelled", Version: int(p.GetVersion())}))
		}
		return nil, libacp.InternalError(err.Error())
	}
	return marshalProtocol(nextEditResultWire(result))
}

func (t *Transport) predictNextEdit(ctx context.Context, p *protocolv1.NextEditRequest, input modelcontract.NextEditInput) (nextEditResult, error) {
	setup, _, closeSetup, err := t.deps.OpenInference(ctx)
	if err != nil {
		return nextEditResult{}, err
	}
	defer closeSetup()
	status, err := setup.VerifiedRole(ctx, inferencesetup.RoleNextEdit)
	if err != nil {
		return nextEditResult{}, err
	}
	fingerprint, err := setup.ConfigurationFingerprint(ctx, status.Assignment)
	if err != nil {
		return nextEditResult{}, err
	}
	format, err := setup.NextEditContract(ctx, status.Assignment)
	if err != nil {
		return nextEditResult{}, err
	}
	profile, err := modelcontract.ResolveNextEdit(format)
	if err != nil {
		return nextEditResult{}, err
	}
	service, clientID, err := t.nextEditService()
	if err != nil {
		return nextEditResult{}, err
	}
	edits, err := service.PredictLatest(ctx, codecompletion.Editor{ClientID: clientID + ":" + p.GetClientId(), DocumentID: p.GetDocumentId()}, codecompletion.NextEditRequest{
		Provider: status.Provider, Model: status.Model, Format: format, Input: input, MaxTokens: profile.MaxTokens,
	})
	if err != nil {
		return nextEditResult{}, err
	}
	current, err := setup.VerifiedRole(ctx, inferencesetup.RoleNextEdit)
	if err != nil {
		return nextEditResult{}, err
	}
	currentFingerprint, err := setup.ConfigurationFingerprint(ctx, current.Assignment)
	if err != nil {
		return nextEditResult{}, err
	}
	if status.Revision != current.Revision || fingerprint != currentFingerprint {
		return nextEditResult{}, errors.New("next-edit configuration changed during inference; retry with the current assignment")
	}
	return nextEditResult{Kind: "edits", Version: int(p.GetVersion()), Edits: edits, Fingerprint: fingerprint}, nil
}

func (t *Transport) nextEditService() (*codecompletion.NextEditService, string, error) {
	t.cfgMu.Lock()
	defer t.cfgMu.Unlock()
	if t.nextEdit == nil {
		service, err := codecompletion.NewNextEdit(t.deps.Engine.Models)
		if err != nil {
			return nil, "", err
		}
		t.nextEdit, t.nextEditClientID = service, uuid.NewString()
	}
	return t.nextEdit, t.nextEditClientID, nil
}
