package modelrepo_test

import (
	"bufio"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/stretchr/testify/require"
)

type blockingBody struct {
	r      io.Reader
	closed chan struct{}
}

func (b *blockingBody) Read(p []byte) (int, error) {
	if n, err := b.r.Read(p); n > 0 || err == nil {
		return n, nil
	}
	<-b.closed
	return 0, errors.New("use of closed network connection")
}

func (b *blockingBody) Close() error {
	select {
	case <-b.closed:
	default:
		close(b.closed)
	}
	return nil
}

func TestUnit_WithIdleTimeout_AStalledStreamFailsInsteadOfHanging(t *testing.T) {
	body := &blockingBody{r: strings.NewReader("data: first\n"), closed: make(chan struct{})}
	rc := modelrepo.WithIdleTimeout(body, 50*time.Millisecond, "test-provider")

	sc := bufio.NewScanner(rc)
	require.True(t, sc.Scan())
	require.Equal(t, "data: first", sc.Text())

	done := make(chan struct{})
	go func() {
		for sc.Scan() {
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("a stream that stops sending must not block the turn forever")
	}
	require.ErrorIs(t, sc.Err(), modelrepo.ErrStreamIdle)
	require.Contains(t, sc.Err().Error(), "test-provider")
}

func TestUnit_WithIdleTimeout_ActiveStreamIsNotCut(t *testing.T) {
	pr, pw := io.Pipe()
	rc := modelrepo.WithIdleTimeout(pr, 80*time.Millisecond, "test-provider")
	go func() {
		for i := 0; i < 6; i++ {
			_, _ = pw.Write([]byte("data: tick\n"))
			time.Sleep(30 * time.Millisecond)
		}
		_ = pw.Close()
	}()

	sc := bufio.NewScanner(rc)
	lines := 0
	for sc.Scan() {
		lines++
	}
	require.NoError(t, sc.Err())
	require.Equal(t, 6, lines, "the timer measures silence, not total duration")
}

func TestUnit_WithIdleTimeout_CloseIsIdempotent(t *testing.T) {
	body := &blockingBody{r: strings.NewReader(""), closed: make(chan struct{})}
	rc := modelrepo.WithIdleTimeout(body, time.Minute, "p")
	require.NoError(t, rc.Close())
	require.NoError(t, rc.Close())
}
