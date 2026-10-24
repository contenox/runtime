package acpsvc

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/google/uuid"

	libacp "github.com/contenox/runtime/libacp"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/shellsession"
)

const terminalEraseSeq = "\x1b[2K\r"

type bridgeTerminal struct {
	id          string
	internalID  string
	startOffset int64
	startRe     *regexp.Regexp
	endRe       *regexp.Regexp
	byteLimit   int64
	panelGuard  int

	mu       sync.Mutex
	exited   bool
	exitCode *int
	signal   *string

	done     chan struct{}
	doneOnce sync.Once
}

func (bt *bridgeTerminal) finish(code *int, signal *string) {
	bt.mu.Lock()
	if !bt.exited {
		bt.exited = true
		bt.exitCode = code
		bt.signal = signal
	}
	bt.mu.Unlock()
	bt.doneOnce.Do(func() { close(bt.done) })
}

func (bt *bridgeTerminal) isExited() bool {
	bt.mu.Lock()
	defer bt.mu.Unlock()
	return bt.exited
}

func (bt *bridgeTerminal) locate(raw string) (out string, sawStart, sawEnd bool, code *int) {
	lo := 0
	if m := bt.startRe.FindStringIndex(raw); m != nil {
		sawStart = true
		lo = m[1]
	}
	hi := len(raw)
	if m := bt.endRe.FindStringSubmatchIndex(raw); m != nil {
		sawEnd = true
		hi = m[0]
		if v, err := strconv.Atoi(raw[m[2]:m[3]]); err == nil {
			code = &v
		}
	}
	if hi < lo {
		hi = lo
	}
	out = raw[lo:hi]
	out = strings.TrimPrefix(out, terminalEraseSeq)
	out = strings.TrimSuffix(out, "\n")
	return out, sawStart, sawEnd, code
}

func (bt *bridgeTerminal) watch(mgr shellsession.Manager, connDone <-chan struct{}, panel func(string)) {
	signal := make(chan struct{}, 1)
	cancel := mgr.Subscribe(bt.internalID, func(shellsession.Chunk) {
		select {
		case signal <- struct{}{}:
		default:
		}
	})
	defer cancel()

	forwarded := 0
	for {
		raw := mgr.Read(bt.internalID, bt.startOffset, 0).Content
		out, sawStart, sawEnd, code := bt.locate(raw)
		if panel != nil && sawStart {
			end := len(out)
			if !sawEnd {
				if end -= bt.panelGuard; end < 0 {
					end = 0
				}
			}
			if end > forwarded {
				panel(out[forwarded:end])
				forwarded = end
			}
		}
		if sawEnd {
			bt.finish(code, nil)
			return
		}
		select {
		case <-signal:
		case <-bt.done:
			return
		case <-connDone:
			sig := "SIGHUP"
			bt.finish(nil, &sig)
			return
		}
	}
}

func (b *externalBridge) CreateTerminal(_ context.Context, req libacp.CreateTerminalRequest) (libacp.CreateTerminalResponse, error) {
	t := b.transport()
	if t == nil {
		return libacp.CreateTerminalResponse{}, libacp.MethodNotFound(libacp.MethodTerminalCreate)
	}
	mgr := t.deps.ShellSessions
	if mgr == nil {
		return libacp.CreateTerminalResponse{}, libacp.MethodNotFound(libacp.MethodTerminalCreate)
	}
	entry, ok := t.sessionFor(b.upstreamID)
	if !ok || entry.InternalSessionID == "" {
		return libacp.CreateTerminalResponse{}, libacp.NewError(libacp.ErrInvalidParams, "acpsvc terminal: session is not open")
	}
	internalID := entry.InternalSessionID

	t.unsubscribeTerminal(b.upstreamID)

	nonce := strings.ReplaceAll(uuid.NewString(), "-", "")
	startTok := "CTXS" + nonce
	endTok := "CTXE" + nonce

	startOffset := mgr.Read(internalID, 0, 0).NextOffset

	var byteLimit int64
	if req.OutputByteLimit != nil && *req.OutputByteLimit > 0 {
		byteLimit = *req.OutputByteLimit
	}

	line := composeTerminalCommand(req, startTok, endTok)
	runCtx := context.WithValue(t.connCtx, runtimetypes.SessionIDContextKey, internalID)
	if _, err := mgr.Run(runCtx, internalID, line); err != nil {
		return libacp.CreateTerminalResponse{}, libacp.InternalError("acpsvc terminal: run: " + err.Error())
	}

	bt := &bridgeTerminal{
		id:          "ext-term-" + nonce,
		internalID:  internalID,
		startOffset: startOffset,
		startRe:     startMarkerRegexp(startTok),
		endRe:       endMarkerRegexp(endTok),
		byteLimit:   byteLimit,
		panelGuard:  len(endTok) + 16,
		done:        make(chan struct{}),
	}
	b.termMu.Lock()
	if b.terminals == nil {
		b.terminals = make(map[string]*bridgeTerminal)
	}
	b.terminals[bt.id] = bt
	b.termMu.Unlock()

	display := req.Command
	if len(req.Args) > 0 {
		display = req.Command + " " + strings.Join(req.Args, " ")
	}
	t.sendTerminalChunk(b.upstreamID, shellsession.Chunk{Data: "$ " + display + "\n"})

	go bt.watch(mgr, t.connCtx.Done(), func(chunk string) {
		t.sendTerminalChunk(b.upstreamID, shellsession.Chunk{Data: chunk})
	})

	return libacp.CreateTerminalResponse{TerminalID: bt.id}, nil
}

func (b *externalBridge) TerminalOutput(_ context.Context, req libacp.TerminalOutputRequest) (libacp.TerminalOutputResponse, error) {
	t := b.transport()
	if t == nil {
		return libacp.TerminalOutputResponse{}, libacp.MethodNotFound(libacp.MethodTerminalOutput)
	}
	mgr := t.deps.ShellSessions
	if mgr == nil {
		return libacp.TerminalOutputResponse{}, libacp.MethodNotFound(libacp.MethodTerminalOutput)
	}
	bt, err := b.lookupTerminal(req.TerminalID)
	if err != nil {
		return libacp.TerminalOutputResponse{}, err
	}

	raw := mgr.Read(bt.internalID, bt.startOffset, 0).Content
	out, sawStart, _, code := bt.locate(raw)
	truncated := !sawStart
	if bt.byteLimit > 0 && int64(len(out)) > bt.byteLimit {
		out = out[int64(len(out))-bt.byteLimit:]
		truncated = true
	}

	resp := libacp.TerminalOutputResponse{Output: out, Truncated: truncated}
	bt.mu.Lock()
	switch {
	case bt.exited:
		resp.ExitStatus = &libacp.TerminalExitStatus{ExitCode: bt.exitCode, Signal: bt.signal}
	case code != nil:
		resp.ExitStatus = &libacp.TerminalExitStatus{ExitCode: code}
	}
	bt.mu.Unlock()
	return resp, nil
}

func (b *externalBridge) WaitForTerminalExit(ctx context.Context, req libacp.WaitForTerminalExitRequest) (libacp.WaitForTerminalExitResponse, error) {
	bt, err := b.lookupTerminal(req.TerminalID)
	if err != nil {
		return libacp.WaitForTerminalExitResponse{}, err
	}
	select {
	case <-bt.done:
	case <-ctx.Done():
		return libacp.WaitForTerminalExitResponse{}, ctx.Err()
	}
	bt.mu.Lock()
	defer bt.mu.Unlock()
	return libacp.WaitForTerminalExitResponse{ExitCode: bt.exitCode, Signal: bt.signal}, nil
}

func (b *externalBridge) KillTerminal(_ context.Context, req libacp.KillTerminalRequest) (libacp.KillTerminalResponse, error) {
	bt, err := b.lookupTerminal(req.TerminalID)
	if err != nil {
		return libacp.KillTerminalResponse{}, err
	}
	if !bt.isExited() {
		b.interrupt(bt)
		sig := "SIGINT"
		bt.finish(nil, &sig)
	}
	return libacp.KillTerminalResponse{}, nil
}

func (b *externalBridge) ReleaseTerminal(_ context.Context, req libacp.ReleaseTerminalRequest) (libacp.ReleaseTerminalResponse, error) {
	bt, ok := b.removeTerminal(req.TerminalID)
	if !ok {
		return libacp.ReleaseTerminalResponse{}, libacp.NewErrorf(libacp.ErrInvalidParams, "acpsvc terminal: unknown terminal %q", req.TerminalID)
	}
	if !bt.isExited() {
		b.interrupt(bt)
		sig := "SIGINT"
		bt.finish(nil, &sig)
	} else {
		bt.finish(bt.exitCode, bt.signal)
	}
	return libacp.ReleaseTerminalResponse{}, nil
}

func (b *externalBridge) closeAllTerminals() {
	b.termMu.Lock()
	terms := make([]*bridgeTerminal, 0, len(b.terminals))
	for _, bt := range b.terminals {
		terms = append(terms, bt)
	}
	b.terminals = nil
	b.termMu.Unlock()
	sig := "SIGHUP"
	for _, bt := range terms {
		bt.finish(nil, &sig)
	}
}

func (b *externalBridge) lookupTerminal(id string) (*bridgeTerminal, error) {
	b.termMu.Lock()
	defer b.termMu.Unlock()
	if bt, ok := b.terminals[id]; ok {
		return bt, nil
	}
	return nil, libacp.NewErrorf(libacp.ErrInvalidParams, "acpsvc terminal: unknown terminal %q", id)
}

func (b *externalBridge) removeTerminal(id string) (*bridgeTerminal, bool) {
	b.termMu.Lock()
	defer b.termMu.Unlock()
	bt, ok := b.terminals[id]
	if ok {
		delete(b.terminals, id)
	}
	return bt, ok
}

func (b *externalBridge) interrupt(bt *bridgeTerminal) {
	t := b.transport()
	if t == nil {
		return
	}
	mgr := t.deps.ShellSessions
	if mgr == nil || !mgr.Read(bt.internalID, bt.startOffset, 0).Exists {
		return
	}
	runCtx := context.WithValue(t.connCtx, runtimetypes.SessionIDContextKey, bt.internalID)
	_, _ = mgr.Run(runCtx, bt.internalID, "\x03")
}

func composeTerminalCommand(req libacp.CreateTerminalRequest, startTok, endTok string) string {
	var exec string
	if len(req.Args) == 0 {
		exec = "bash -c " + shellQuoteArg(req.Command)
	} else {
		parts := make([]string, 0, 1+len(req.Args))
		parts = append(parts, shellQuoteArg(req.Command))
		for _, a := range req.Args {
			parts = append(parts, shellQuoteArg(a))
		}
		exec = strings.Join(parts, " ")
	}
	if len(req.Env) > 0 {
		env := make([]string, 0, len(req.Env)+1)
		env = append(env, "env")
		for _, e := range req.Env {
			env = append(env, shellQuoteArg(e.Name)+"="+shellQuoteArg(e.Value))
		}
		exec = strings.Join(env, " ") + " " + exec
	}
	if req.Cwd != "" {
		exec = "cd " + shellQuoteArg(req.Cwd) + " && " + exec
	}

	var sb strings.Builder
	sb.WriteString("printf '")
	sb.WriteString(startTok)
	sb.WriteString(`%d\033[2K\r' 0;`)
	sb.WriteString("( ")
	sb.WriteString(exec)
	sb.WriteString(" );__ce=$?;")
	sb.WriteString(`printf '\n`)
	sb.WriteString(endTok)
	sb.WriteString(` %d\033[2K\r' "$__ce"`)
	return sb.String()
}

func shellQuoteArg(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func startMarkerRegexp(tok string) *regexp.Regexp {
	return regexp.MustCompile(regexp.QuoteMeta(tok) + `(\d)`)
}

func endMarkerRegexp(tok string) *regexp.Regexp {
	return regexp.MustCompile(regexp.QuoteMeta(tok) + ` (\d+)`)
}
