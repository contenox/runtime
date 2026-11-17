package sessionkit

import (
	"errors"

	"github.com/contenox/runtime/internal/transport"
)

// WithExecution attaches the successful native allocation to the existing session report.
func WithExecution(session transport.Session, execution transport.SessionExecution) (transport.Session, error) {
	if session == nil {
		return nil, errors.New("native execution session is required")
	}
	if execution.Backend == "" {
		return nil, errors.New("native execution backend is required")
	}
	return &executionSession{Session: session, execution: *transport.CopyExecutionReport(execution)}, nil
}

type executionSession struct {
	transport.Session
	execution transport.SessionExecution
}

func (s *executionSession) ExplainContext() transport.ContextReport {
	report := s.Session.ExplainContext()
	report.Execution = transport.CopyExecutionReport(s.execution)
	return report
}
