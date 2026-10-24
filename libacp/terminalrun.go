package libacp

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const terminalDetachedTimeout = 5 * time.Second

type TerminalPeer interface {
	CreateTerminal(context.Context, CreateTerminalRequest) (CreateTerminalResponse, error)
	TerminalOutput(context.Context, TerminalOutputRequest) (TerminalOutputResponse, error)
	WaitForTerminalExit(context.Context, WaitForTerminalExitRequest) (WaitForTerminalExitResponse, error)
	KillTerminal(context.Context, KillTerminalRequest) (KillTerminalResponse, error)
	ReleaseTerminal(context.Context, ReleaseTerminalRequest) (ReleaseTerminalResponse, error)
}

var _ TerminalPeer = (*AgentSideConnection)(nil)

type TerminalResult struct {
	Output    string
	Truncated bool
	ExitCode  int
	Signal    *string
	Cancelled bool
	TimedOut  bool
}

func RunTerminal(ctx context.Context, p TerminalPeer, req CreateTerminalRequest, onCreated func(terminalID string)) (TerminalResult, error) {
	createResp, err := p.CreateTerminal(ctx, req)
	if err != nil {
		return TerminalResult{ExitCode: -1}, fmt.Errorf("libacp terminal: create: %w", err)
	}
	termID := createResp.TerminalID

	defer func() {
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), terminalDetachedTimeout)
		defer cancel()
		_, _ = p.ReleaseTerminal(rctx, ReleaseTerminalRequest{SessionID: req.SessionID, TerminalID: termID})
	}()

	if onCreated != nil {
		onCreated(termID)
	}

	exitResp, waitErr := p.WaitForTerminalExit(ctx, WaitForTerminalExitRequest{SessionID: req.SessionID, TerminalID: termID})

	var res TerminalResult
	if waitErr != nil {
		switch {
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			res.TimedOut = true
		case ctx.Err() != nil:
			res.Cancelled = true
		default:
			res.ExitCode = -1
			return res, fmt.Errorf("libacp terminal: wait: %w", waitErr)
		}
		kctx, kcancel := context.WithTimeout(context.WithoutCancel(ctx), terminalDetachedTimeout)
		_, _ = p.KillTerminal(kctx, KillTerminalRequest{SessionID: req.SessionID, TerminalID: termID})
		kcancel()
	}

	octx, ocancel := context.WithTimeout(context.WithoutCancel(ctx), terminalDetachedTimeout)
	outputResp, oerr := p.TerminalOutput(octx, TerminalOutputRequest{SessionID: req.SessionID, TerminalID: termID})
	ocancel()
	if oerr != nil {
		res.ExitCode = -1
		return res, fmt.Errorf("libacp terminal: output: %w", oerr)
	}

	res.Output = outputResp.Output
	res.Truncated = outputResp.Truncated
	res.Signal = exitResp.Signal

	switch {
	case exitResp.ExitCode != nil:
		res.ExitCode = *exitResp.ExitCode
	case outputResp.ExitStatus != nil && outputResp.ExitStatus.ExitCode != nil:
		res.ExitCode = *outputResp.ExitStatus.ExitCode
	}
	if res.Signal == nil && outputResp.ExitStatus != nil {
		res.Signal = outputResp.ExitStatus.Signal
	}
	if res.Signal != nil && res.ExitCode == 0 {
		res.ExitCode = -1
	}
	if res.Cancelled || res.TimedOut {
		res.ExitCode = -1
	}
	return res, nil
}
