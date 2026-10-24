//go:build windows

package shellsession

import "errors"

type ptySession struct{}

var ErrUnsupported = errors.New("shellsession: shell sessions are not supported on this platform")

func startPTY(cwd, shell string, scrub func([]string) []string) (*ptySession, error) {
	return nil, ErrUnsupported
}

func (p *ptySession) Read(b []byte) (int, error)  { return 0, ErrUnsupported }
func (p *ptySession) Write(b []byte) (int, error) { return 0, ErrUnsupported }
func (p *ptySession) close()                      {}
func (p *ptySession) wait()                       {}

func defaultShell() string { return "" }
