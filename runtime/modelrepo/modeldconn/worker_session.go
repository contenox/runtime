package modeldconn

import (
	"errors"
	"github.com/contenox/runtime/internal/transport"
	"sync"
)

type workerSession struct {
	transport.Session
	once    sync.Once
	release func() error
	retire  func() error
	err     error
}

func (s *workerSession) Close() error {
	s.once.Do(func() {
		s.err = s.Session.Close()
		if s.err == nil {
			s.err = s.release()
		} else if s.retire != nil {
			stopErr := s.retire()
			s.err = errors.Join(s.err, stopErr)
			if stopErr == nil {
				s.err = errors.Join(s.err, s.release())
			}
		}
	})
	return s.err
}
