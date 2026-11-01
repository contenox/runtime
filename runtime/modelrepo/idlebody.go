package modelrepo

import (
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

var StreamIdleTimeout = 5 * time.Minute

var ErrStreamIdle = errors.New("stream stalled")

type idleBody struct {
	rc       io.ReadCloser
	idle     time.Duration
	label    string
	timer    *time.Timer
	timedOut atomic.Bool
	once     sync.Once
}

func WithIdleTimeout(rc io.ReadCloser, idle time.Duration, label string) io.ReadCloser {
	if rc == nil || idle <= 0 {
		return rc
	}
	b := &idleBody{rc: rc, idle: idle, label: label}
	b.timer = time.AfterFunc(idle, func() {
		b.timedOut.Store(true)
		_ = rc.Close()
	})
	return b
}

func (b *idleBody) Read(p []byte) (int, error) {
	if b.timedOut.Load() {
		return 0, b.idleErr()
	}
	b.timer.Reset(b.idle)
	n, err := b.rc.Read(p)
	if b.timedOut.Load() {
		return n, b.idleErr()
	}
	return n, err
}

func (b *idleBody) idleErr() error {
	return fmt.Errorf("%w: %s sent nothing for %s, so the request was abandoned; retry, or pick another model if it keeps happening", ErrStreamIdle, b.label, b.idle)
}

func (b *idleBody) Close() error {
	var err error
	b.once.Do(func() {
		b.timer.Stop()
		err = b.rc.Close()
	})
	return err
}
