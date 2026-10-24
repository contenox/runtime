package shellsession

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/contenox/runtime/runtime/vfs"
)

const (
	defaultScrollbackBytes = 64 * 1024
	defaultIdleTimeout     = 15 * time.Minute
	flushInterval          = 60 * time.Millisecond
	runCaptureWindow       = 250 * time.Millisecond
	readChunkBytes         = 32 * 1024
	subscriberBuffer       = 1024
)

type Chunk struct {
	Offset int64
	Data   string
	Reset  bool
}

type RunResult struct {
	Offset   int64
	Snapshot string
	Started  bool
}

type ReadResult struct {
	Content    string
	FromOffset int64
	NextOffset int64
	Exists     bool
}

type Manager interface {
	Run(ctx context.Context, sessionID, line string) (RunResult, error)
	Read(sessionID string, since int64, tailBytes int) ReadResult
	Subscribe(sessionID string, fn func(Chunk)) (cancel func())
	Kill(sessionID string)
	Shutdown()
}

type Config struct {
	CwdResolver     func(ctx context.Context) string
	Workspace       *vfs.Factory
	Shell           string
	ScrollbackBytes int
	IdleTimeout     time.Duration
	ScrubEnv        func([]string) []string
}

type manager struct {
	cfg      Config
	idle     time.Duration
	capacity int

	mu     sync.Mutex
	shells map[string]*shell
	subs   map[string][]*subscriber

	stop     chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

func NewManager(cfg Config) Manager {
	m := &manager{
		cfg:      cfg,
		idle:     cfg.IdleTimeout,
		capacity: cfg.ScrollbackBytes,
		shells:   map[string]*shell{},
		subs:     map[string][]*subscriber{},
		stop:     make(chan struct{}),
	}
	if m.idle == 0 {
		m.idle = defaultIdleTimeout
	}
	if m.capacity <= 0 {
		m.capacity = defaultScrollbackBytes
	}
	if m.idle > 0 {
		m.wg.Add(1)
		go m.reap()
	}
	return m
}

func (m *manager) resolveCwd(ctx context.Context) (string, error) {
	cwd := ""
	if m.cfg.CwdResolver != nil {
		cwd = m.cfg.CwdResolver(ctx)
	}
	return vfs.ResolveSessionCwd(m.cfg.Workspace, cwd, "")
}

func (m *manager) Run(ctx context.Context, sessionID, line string) (RunResult, error) {
	sh, started, err := m.ensureShell(ctx, sessionID)
	if err != nil {
		return RunResult{}, err
	}
	pre := sh.sb.endOffset()
	if err := sh.submit(line); err != nil {
		return RunResult{}, err
	}
	select {
	case <-time.After(runCaptureWindow):
	case <-ctx.Done():
	}
	res := m.Read(sessionID, pre, 0)
	return RunResult{Offset: res.NextOffset, Snapshot: res.Content, Started: started}, nil
}

func (m *manager) Read(sessionID string, since int64, tailBytes int) ReadResult {
	m.mu.Lock()
	sh, ok := m.shells[sessionID]
	m.mu.Unlock()
	if !ok {
		return ReadResult{Exists: false}
	}
	var data []byte
	var from, to int64
	if since >= 0 {
		data, from, to = sh.sb.since(since)
	} else {
		data, from, to = sh.sb.tail(tailBytes)
	}
	return ReadResult{Content: string(data), FromOffset: from, NextOffset: to, Exists: true}
}

func (m *manager) Subscribe(sessionID string, fn func(Chunk)) func() {
	sub := newSubscriber(fn)
	m.mu.Lock()
	m.subs[sessionID] = append(m.subs[sessionID], sub)
	sh, ok := m.shells[sessionID]
	m.mu.Unlock()

	data, from, _ := []byte(nil), int64(0), int64(0)
	if ok {
		data, from, _ = sh.sb.snapshot()
	}
	sub.deliver(Chunk{Offset: from, Data: string(data), Reset: true})

	return func() {
		m.mu.Lock()
		list := m.subs[sessionID]
		for i, s := range list {
			if s == sub {
				m.subs[sessionID] = append(list[:i], list[i+1:]...)
				break
			}
		}
		if len(m.subs[sessionID]) == 0 {
			delete(m.subs, sessionID)
		}
		m.mu.Unlock()
		sub.stop()
	}
}

func (m *manager) Kill(sessionID string) {
	m.killShell(sessionID, true)
}

func (m *manager) Shutdown() {
	m.stopOnce.Do(func() { close(m.stop) })
	m.mu.Lock()
	ids := make([]string, 0, len(m.shells))
	for id := range m.shells {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	for _, id := range ids {
		m.killShell(id, true)
	}
	m.wg.Wait()
}

func (m *manager) ensureShell(ctx context.Context, sessionID string) (*shell, bool, error) {
	m.mu.Lock()
	if sh, ok := m.shells[sessionID]; ok && !sh.closed.Load() {
		m.mu.Unlock()
		return sh, false, nil
	}
	m.mu.Unlock()

	cwd, err := m.resolveCwd(ctx)
	if err != nil {
		return nil, false, err
	}
	pty, err := startPTY(cwd, m.cfg.Shell, m.cfg.ScrubEnv)
	if err != nil {
		return nil, false, err
	}
	sh := &shell{
		id:   sessionID,
		pty:  pty,
		sb:   newScrollback(m.capacity),
		mgr:  m,
		done: make(chan struct{}),
	}
	sh.touch()

	m.mu.Lock()
	if existing, ok := m.shells[sessionID]; ok && !existing.closed.Load() {
		m.mu.Unlock()
		pty.close()
		return existing, false, nil
	}
	m.shells[sessionID] = sh
	m.mu.Unlock()

	m.wg.Add(2)
	go sh.readLoop()
	go sh.flushLoop()
	return sh, true, nil
}

func (m *manager) fanout(sessionID string, c Chunk) {
	m.mu.Lock()
	subs := append([]*subscriber(nil), m.subs[sessionID]...)
	m.mu.Unlock()
	for _, s := range subs {
		s.deliver(c)
	}
}

func (m *manager) killShell(sessionID string, dropSubs bool) {
	m.mu.Lock()
	sh := m.shells[sessionID]
	delete(m.shells, sessionID)
	var subs []*subscriber
	if dropSubs {
		subs = m.subs[sessionID]
		delete(m.subs, sessionID)
	}
	m.mu.Unlock()
	if sh != nil {
		sh.shutdown()
	}
	for _, s := range subs {
		s.stop()
	}
}

func (m *manager) reap() {
	defer m.wg.Done()
	interval := m.idle / 2
	if interval < time.Second {
		interval = time.Second
	}
	if interval > time.Minute {
		interval = time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-m.stop:
			return
		case <-ticker.C:
			now := time.Now()
			m.mu.Lock()
			var idle []string
			for id, sh := range m.shells {
				if now.Sub(sh.lastActivity()) >= m.idle {
					idle = append(idle, id)
				}
			}
			m.mu.Unlock()
			for _, id := range idle {
				m.killShell(id, false)
			}
		}
	}
}

type shell struct {
	id      string
	pty     *ptySession
	sb      *scrollback
	mgr     *manager
	done    chan struct{}
	once    sync.Once
	closed  atomic.Bool
	lastNs  atomic.Int64
	emitted int64
}

func (s *shell) touch()                  { s.lastNs.Store(time.Now().UnixNano()) }
func (s *shell) lastActivity() time.Time { return time.Unix(0, s.lastNs.Load()) }

func (s *shell) submit(line string) error {
	s.touch()
	_, err := s.pty.Write([]byte(line + "\n"))
	return err
}

func (s *shell) readLoop() {
	defer s.mgr.wg.Done()
	buf := make([]byte, readChunkBytes)
	for {
		n, err := s.pty.Read(buf)
		if n > 0 {
			s.sb.append(buf[:n])
			s.touch()
		}
		if err != nil {
			s.shutdown()
			return
		}
	}
}

func (s *shell) flushLoop() {
	defer s.mgr.wg.Done()
	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.done:
			s.flush()
			return
		case <-ticker.C:
			s.flush()
		}
	}
}

func (s *shell) flush() {
	data, from, to := s.sb.since(s.emitted)
	if len(data) == 0 {
		return
	}
	s.emitted = to
	s.mgr.fanout(s.id, Chunk{Offset: from, Data: string(data)})
}

func (s *shell) shutdown() {
	s.once.Do(func() {
		s.closed.Store(true)
		close(s.done)
		s.pty.close()
		go s.pty.wait()
	})
}

var _ Manager = (*manager)(nil)
