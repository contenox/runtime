package contenoxcli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/contenox/runtime/runtime/hitlservice"
	"github.com/contenox/runtime/runtime/localtools"
)

var ErrApprovalAborted = errors.New("run aborted at approval prompt")

const (
	maxArgValueDisplay = 240

	maxDiffLinesDisplay = 120

	inputFlushWindow = 15 * time.Millisecond
)

type CLIApprovalOptions struct {
	AuditLog func(req hitlservice.ApprovalRequest, decision string)
}

func NewCLIAskApproval(w io.Writer) localtools.AskApproval {
	return NewCLIAskApprovalWithOptions(w, CLIApprovalOptions{})
}

func NewCLIAskApprovalWithOptions(w io.Writer, opts CLIApprovalOptions) localtools.AskApproval {
	var (
		mu      sync.Mutex
		aborted bool
	)

	return func(ctx context.Context, req hitlservice.ApprovalRequest) (bool, error) {
		mu.Lock()
		defer mu.Unlock()

		if aborted {
			return false, ErrApprovalAborted
		}

		key := req.ToolsName + "." + req.ToolName

		time.Sleep(250 * time.Millisecond)

		tty, isTerm, err := openControllingTerminal()
		if err != nil || !isTerm {
			fmt.Fprintf(w, "\n  [denied: %s requires approval but no terminal is attached]\n", key)
			if opts.AuditLog != nil {
				opts.AuditLog(req, "denied-no-tty")
			}
			return false, nil
		}
		defer tty.Close()

		flushInput(tty)

		renderApprovalRequest(w, req)

		fmt.Fprintln(w, "────────────────────────────────────────────────────")
		fmt.Fprint(w, "  Approve? [y]es / [N]o / [q]uit: ")

		line, ok, err := readLine(ctx, tty, w)
		if err != nil {
			return false, err
		}

		decision := "denied"
		approved := false
		switch {
		case !ok:
		default:
			switch strings.TrimSpace(strings.ToLower(line)) {
			case "y", "yes":
				approved, decision = true, "approved"
			case "q", "quit", "abort":
				aborted = true
				decision = "aborted"
			}
		}

		if opts.AuditLog != nil {
			opts.AuditLog(req, decision)
		}
		if aborted {
			fmt.Fprintln(w, "  (run aborted)")
			return false, ErrApprovalAborted
		}
		return approved, nil
	}
}

func renderApprovalRequest(w io.Writer, req hitlservice.ApprovalRequest) {
	fmt.Fprintln(w, "\n────────────────────────────────────────────────────")
	fmt.Fprintf(w, "  HITL approval required\n")
	fmt.Fprintf(w, "  Tools : %s\n", req.ToolsName)
	fmt.Fprintf(w, "  Tool  : %s\n", req.ToolName)

	if len(req.Args) > 0 {
		fmt.Fprintln(w, "  Args  :")
		keys := make([]string, 0, len(req.Args))
		for k := range req.Args {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(w, "    %s = %s\n", k, summariseArg(req.Args[k]))
		}
	}

	if req.Diff != "" {
		fmt.Fprintln(w, "  Diff  :")
		lines := strings.Split(req.Diff, "\n")
		shown := lines
		if len(lines) > maxDiffLinesDisplay {
			shown = lines[:maxDiffLinesDisplay]
		}
		for _, line := range shown {
			fmt.Fprintf(w, "    %s\n", line)
		}
		if len(lines) > maxDiffLinesDisplay {
			fmt.Fprintf(w, "\n  ⚠ diff truncated: showing %d of %d lines — %d lines NOT shown below this point.\n",
				maxDiffLinesDisplay, len(lines), len(lines)-maxDiffLinesDisplay)
			fmt.Fprintf(w, "    Approving accepts changes you have not seen.\n")
		}
	}
}

func summariseArg(v any) string {
	s := fmt.Sprintf("%v", v)
	total := len(s)
	lines := strings.Count(s, "\n") + 1

	if total <= maxArgValueDisplay && lines == 1 {
		return s
	}
	head := s
	if len(head) > maxArgValueDisplay {
		head = head[:maxArgValueDisplay]
	}
	head = strings.ReplaceAll(head, "\n", "\\n")
	return fmt.Sprintf("%s… [%d bytes, %d lines — see diff]", head, total, lines)
}

func readLine(ctx context.Context, tty *os.File, w io.Writer) (string, bool, error) {
	type result struct {
		line string
		ok   bool
	}
	ch := make(chan result, 1)
	done := make(chan struct{})

	go func() {
		defer close(done)
		sc := bufio.NewScanner(tty)
		if sc.Scan() {
			ch <- result{line: sc.Text(), ok: true}
			return
		}
		ch <- result{ok: false}
	}()

	select {
	case r := <-ch:
		fmt.Fprintln(w)
		return r.line, r.ok, nil
	case <-ctx.Done():
		fmt.Fprintln(w, "\n  (cancelled)")
		_ = tty.Close()
		<-done
		return "", false, ctx.Err()
	}
}
