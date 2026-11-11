package workspaceevents

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/contenox/runtime/liblease"
	"github.com/contenox/runtime/libroutine"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/vfs"
	"github.com/fsnotify/fsnotify"
)

var ErrDirectoryLimit = errors.New("workspaceevents: directory watch limit reached; deeper changes are not reported")

type SkipFunc func(root, rel string, isDir bool) bool

type WatcherConfig struct {
	WorkspaceID string
	Owner       string
	LeaseDir    string
	Tracker     libtracker.ActivityTracker
	Skip        SkipFunc
	Transient   func(rel string) bool
	Reconcile   time.Duration
	LeaseTTL    time.Duration
	Debounce    time.Duration
	MaxDelay    time.Duration
	MaxDirs     int
}

func (c WatcherConfig) withDefaults() WatcherConfig {
	if c.Reconcile <= 0 {
		c.Reconcile = 2 * time.Second
	}
	if c.LeaseTTL <= 0 {
		c.LeaseTTL = 30 * time.Second
	}
	if c.Debounce <= 0 {
		c.Debounce = 100 * time.Millisecond
	}
	if c.MaxDelay <= 0 {
		c.MaxDelay = time.Second
	}
	if c.MaxDirs <= 0 {
		c.MaxDirs = 20000
	}
	if c.Tracker == nil {
		c.Tracker = libtracker.NoopTracker{}
	}
	return c
}

type Watcher struct {
	cfg    WatcherConfig
	events Service
	roots  Roots

	mu      sync.Mutex
	watches map[string]*rootWatch
	leases  map[string]*liblease.Lease
	renewed map[string]time.Time
	cancel  context.CancelFunc
	done    chan struct{}
}

func NewWatcher(events Service, roots Roots, cfg WatcherConfig) *Watcher {
	return &Watcher{cfg: cfg.withDefaults(), events: events, roots: roots, watches: map[string]*rootWatch{}, leases: map[string]*liblease.Lease{}, renewed: map[string]time.Time{}}
}

func LeaseFileName(workspaceID, root string) string {
	sum := sha256.Sum256([]byte(workspaceID + "|" + rootKey(root)))
	return "watch-" + hex.EncodeToString(sum[:8]) + ".lease"
}

func (w *Watcher) leasePath(root string) string {
	return filepath.Join(w.cfg.LeaseDir, LeaseFileName(w.cfg.WorkspaceID, root))
}

func (w *Watcher) Start(ctx context.Context) {
	w.mu.Lock()
	if w.cancel != nil {
		w.mu.Unlock()
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	w.cancel = cancel
	w.done = make(chan struct{})
	w.mu.Unlock()
	go w.run(runCtx)
}

func (w *Watcher) Stop() {
	w.mu.Lock()
	cancel, done := w.cancel, w.done
	w.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
}

func (w *Watcher) Watching() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]string, 0, len(w.watches))
	for root := range w.watches {
		out = append(out, root)
	}
	sort.Strings(out)
	return out
}

func (w *Watcher) run(ctx context.Context) {
	defer close(w.done)
	routine := libroutine.NewRoutine(5, 30*time.Second)
	routine.Loop(ctx, w.cfg.Reconcile, nil, w.reconcile, func(error) {})
	w.shutdown()
}

func (w *Watcher) reconcile(ctx context.Context) error {
	wanted := map[string]bool{}
	if w.roots != nil {
		for _, root := range w.roots.Roots() {
			wanted[root] = true
		}
	}
	w.mu.Lock()
	current := map[string]*rootWatch{}
	for root, rw := range w.watches {
		current[root] = rw
	}
	w.mu.Unlock()

	for root, rw := range current {
		if !wanted[root] {
			w.stopRoot(root, rw)
			w.releaseLease(context.WithoutCancel(ctx), root)
		}
	}
	var failures []error
	for root := range wanted {
		rw, watching := current[root]
		held, err := w.holdLease(ctx, root)
		if err != nil {
			failures = append(failures, err)
		}
		switch {
		case held && !watching:
			w.startRoot(ctx, root)
		case !held && watching:
			w.stopRoot(root, rw)
		}
	}
	return errors.Join(failures...)
}

func (w *Watcher) holdLease(ctx context.Context, root string) (bool, error) {
	w.mu.Lock()
	lease, renewed := w.leases[root], w.renewed[root]
	w.mu.Unlock()
	if lease != nil && !lease.Expired() && time.Since(renewed) < w.cfg.LeaseTTL/3 {
		return true, nil
	}
	reportErr, _, end := w.cfg.Tracker.Start(ctx, "hold", "workspace_watch_lease", "root", root)
	defer end()
	if lease != nil {
		err := lease.RenewContext(ctx)
		switch {
		case err == nil:
			w.mu.Lock()
			w.renewed[root] = time.Now()
			w.mu.Unlock()
			return true, nil
		case errors.Is(err, liblease.ErrLost):
			w.forgetLease(root, lease)
			return false, nil
		default:
			reportErr(err)
			if lease.Expired() {
				w.forgetLease(root, lease)
				return false, err
			}
			return true, err
		}
	}
	if err := os.MkdirAll(w.cfg.LeaseDir, 0o700); err != nil {
		reportErr(err)
		return false, err
	}
	acquired, err := liblease.AcquireContext(ctx, w.leasePath(root), w.cfg.LeaseTTL, liblease.WithMeta(map[string]string{"owner": w.cfg.Owner, "root": root}))
	if errors.Is(err, liblease.ErrHeld) {
		return false, nil
	}
	if err != nil {
		reportErr(err)
		return false, err
	}
	w.mu.Lock()
	w.leases[root] = acquired
	w.renewed[root] = time.Now()
	w.mu.Unlock()
	return true, nil
}

func (w *Watcher) forgetLease(root string, lease *liblease.Lease) {
	w.mu.Lock()
	if w.leases[root] == lease {
		delete(w.leases, root)
		delete(w.renewed, root)
	}
	w.mu.Unlock()
}

func (w *Watcher) releaseLease(ctx context.Context, root string) {
	w.mu.Lock()
	lease := w.leases[root]
	w.mu.Unlock()
	if lease == nil {
		return
	}
	w.forgetLease(root, lease)
	releaseCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := lease.ReleaseContext(releaseCtx); err != nil {
		reportErr, _, end := w.cfg.Tracker.Start(ctx, "release", "workspace_watch_lease", "root", root)
		reportErr(err)
		end()
	}
}

func (w *Watcher) shutdown() {
	w.mu.Lock()
	watches := w.watches
	w.watches = map[string]*rootWatch{}
	roots := make([]string, 0, len(w.leases))
	for root := range w.leases {
		roots = append(roots, root)
	}
	w.mu.Unlock()
	for _, rw := range watches {
		rw.stop()
	}
	for _, root := range roots {
		w.releaseLease(context.Background(), root)
	}
}

func (w *Watcher) startRoot(ctx context.Context, root string) {
	reportErr, _, end := w.cfg.Tracker.Start(ctx, "start", "workspace_watch", "root", root)
	defer end()
	real, err := vfs.ResolveRoot(root)
	if err != nil {
		reportErr(fmt.Errorf("workspaceevents: cannot resolve a root to watch: %w", err))
		return
	}
	fsw, err := fsnotify.NewBufferedWatcher(4096)
	if err != nil {
		reportErr(fmt.Errorf("workspaceevents: cannot create a file watcher: %w", err))
		return
	}
	watchCtx, cancel := context.WithCancel(ctx)
	rw := &rootWatch{
		owner:   w,
		root:    root,
		real:    real,
		fsw:     fsw,
		cancel:  cancel,
		done:    make(chan struct{}),
		dirs:    map[string]bool{},
		pending: map[string]*pendingPath{},
	}
	rw.addTree(real, false)
	w.mu.Lock()
	w.watches[root] = rw
	w.mu.Unlock()
	go rw.loop(watchCtx)
}

func (w *Watcher) stopRoot(root string, rw *rootWatch) {
	rw.stop()
	w.mu.Lock()
	if w.watches[root] == rw {
		delete(w.watches, root)
	}
	w.mu.Unlock()
}

func (w *Watcher) skipped(root, rel string, isDir bool) bool {
	if w.cfg.Skip == nil {
		return false
	}
	parts := strings.Split(rel, "/")
	for i := range parts {
		dir := i < len(parts)-1 || isDir
		if w.cfg.Skip(root, strings.Join(parts[:i+1], "/"), dir) {
			return true
		}
	}
	return false
}

type pendingPath struct {
	path      string
	first     int
	created   bool
	wrote     bool
	removed   bool
	renamed   bool
	createSeq int
	renameSeq int
	isDir     bool
}

type rootWatch struct {
	owner  *Watcher
	root   string
	real   string
	fsw    *fsnotify.Watcher
	cancel context.CancelFunc
	done   chan struct{}

	dirs     map[string]bool
	capped   bool
	pending  map[string]*pendingPath
	seq      int
	rescan   bool
	stopOnce sync.Once
}

func (rw *rootWatch) stop() {
	rw.stopOnce.Do(func() {
		rw.cancel()
		<-rw.done
	})
}

func (rw *rootWatch) relative(path string) (string, bool) {
	rel, err := filepath.Rel(rw.real, path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

func (rw *rootWatch) transient(path string) bool {
	if rw.owner.cfg.Transient == nil {
		return false
	}
	rel, ok := rw.relative(path)
	return ok && rw.owner.cfg.Transient(rel)
}

func (rw *rootWatch) addTree(dir string, report bool) {
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			if report {
				if rel, ok := rw.relative(path); ok && !rw.owner.skipped(rw.real, rel, false) {
					rw.markCreated(path, false)
				}
			}
			return nil
		}
		if path != rw.real {
			rel, ok := rw.relative(path)
			if !ok || rw.owner.skipped(rw.real, rel, true) {
				return filepath.SkipDir
			}
			if report && path != dir {
				rw.markCreated(path, true)
			}
		}
		if rw.dirs[path] {
			return nil
		}
		if len(rw.dirs) >= rw.owner.cfg.MaxDirs {
			if !rw.capped {
				rw.capped = true
				rw.report("watch", fmt.Errorf("%w (limit %d)", ErrDirectoryLimit, rw.owner.cfg.MaxDirs))
			}
			return filepath.SkipDir
		}
		if err := rw.fsw.Add(path); err != nil {
			rw.report("watch_dir", fmt.Errorf("workspaceevents: cannot watch %s: %w", path, err))
			return nil
		}
		rw.dirs[path] = true
		return nil
	})
}

func (rw *rootWatch) entry(path string) *pendingPath {
	p, seen := rw.pending[path]
	if !seen {
		rw.seq++
		p = &pendingPath{path: path, first: rw.seq}
		rw.pending[path] = p
	}
	rw.seq++
	return p
}

func (rw *rootWatch) markCreated(path string, isDir bool) {
	p := rw.entry(path)
	p.created = true
	if p.createSeq == 0 {
		p.createSeq = rw.seq
	}
	p.isDir = p.isDir || isDir
}

func (rw *rootWatch) forgetTree(dir string) {
	prefix := dir + string(filepath.Separator)
	for path := range rw.dirs {
		if path == dir || strings.HasPrefix(path, prefix) {
			delete(rw.dirs, path)
		}
	}
}

func (rw *rootWatch) loop(ctx context.Context) {
	defer close(rw.done)
	defer rw.fsw.Close()
	var timer *time.Timer
	var fire <-chan time.Time
	var firstAt time.Time
	arm := func() {
		now := time.Now()
		if fire == nil {
			firstAt = now
			timer = time.NewTimer(rw.owner.cfg.Debounce)
			fire = timer.C
			return
		}
		wait := rw.owner.cfg.Debounce
		if remaining := firstAt.Add(rw.owner.cfg.MaxDelay).Sub(now); remaining < wait {
			wait = max(remaining, 0)
		}
		timer.Reset(wait)
	}
	for {
		select {
		case <-ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return
		case ev, open := <-rw.fsw.Events:
			if !open {
				return
			}
			if rw.record(ev) {
				arm()
			}
		case err, open := <-rw.fsw.Errors:
			if !open {
				return
			}
			if errors.Is(err, fsnotify.ErrEventOverflow) {
				rw.rescan = true
				arm()
				continue
			}
			rw.report("watch", err)
		case <-fire:
			fire = nil
			rw.flush(ctx)
		}
	}
}

func (rw *rootWatch) record(ev fsnotify.Event) bool {
	if ev.Op == fsnotify.Chmod || ev.Op == 0 {
		return false
	}
	path := filepath.Clean(ev.Name)
	rel, ok := rw.relative(path)
	if !ok {
		return false
	}
	info, statErr := os.Lstat(path)
	isDir := statErr == nil && info.IsDir()
	if statErr != nil {
		isDir = rw.dirs[path]
	}
	if rw.owner.skipped(rw.real, rel, isDir) {
		return false
	}
	p := rw.entry(path)
	if ev.Has(fsnotify.Create) {
		p.created = true
		if p.createSeq == 0 {
			p.createSeq = rw.seq
		}
		if isDir {
			rw.addTree(path, true)
		}
	}
	if ev.Has(fsnotify.Write) {
		p.wrote = true
	}
	if ev.Has(fsnotify.Remove) {
		p.removed = true
	}
	if ev.Has(fsnotify.Rename) {
		p.renamed = true
		p.renameSeq = rw.seq
	}
	p.isDir = p.isDir || isDir
	return true
}

type emitted struct {
	order int
	in    Input
}

func statData(info os.FileInfo, extra map[string]any) json.RawMessage {
	data := map[string]any{
		"isDir": info.IsDir(),
		"mtime": info.ModTime().UTC().Format(time.RFC3339Nano),
	}
	if !info.IsDir() {
		data["size"] = info.Size()
	}
	for k, v := range extra {
		data[k] = v
	}
	raw, _ := json.Marshal(data)
	return raw
}

type batchResult struct {
	events []emitted
	forget []string
}

func resolveBatch(paths []*pendingPath, exists map[string]os.FileInfo, transient func(string) bool, relative func(string) (string, bool)) batchResult {
	var res batchResult
	var sources, targets []*pendingPath
	for _, p := range paths {
		if _, here := exists[p.path]; p.renamed && !here {
			sources = append(sources, p)
		}
		if _, here := exists[p.path]; p.created && here {
			targets = append(targets, p)
		}
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].renameSeq < sources[j].renameSeq })
	moveTo := map[string]*pendingPath{}
	moveFrom := map[string]*pendingPath{}
	for _, src := range sources {
		for _, dst := range targets {
			if moveFrom[dst.path] != nil || dst.createSeq <= src.renameSeq {
				continue
			}
			moveTo[src.path] = dst
			moveFrom[dst.path] = src
			break
		}
	}

	for _, p := range paths {
		if moveTo[p.path] != nil || transient(p.path) {
			continue
		}
		info, here := exists[p.path]
		if src := moveFrom[p.path]; src != nil {
			if src.created || transient(src.path) {
				res.events = append(res.events, emitted{p.first, Input{Type: TypeFSModified, Subject: p.path, Data: statData(info, nil)}})
				continue
			}
			from, _ := relative(src.path)
			res.events = append(res.events, emitted{p.first, Input{Type: TypeFSMoved, Subject: p.path, Data: statData(info, map[string]any{"from": from})}})
			if src.isDir {
				res.forget = append(res.forget, src.path)
			}
			continue
		}
		switch {
		case here && p.created:
			res.events = append(res.events, emitted{p.first, Input{Type: TypeFSCreated, Subject: p.path, Data: statData(info, nil)}})
		case here && !info.IsDir() && (p.wrote || p.removed || p.renamed):
			res.events = append(res.events, emitted{p.first, Input{Type: TypeFSModified, Subject: p.path, Data: statData(info, nil)}})
		case !here && p.created:
			if p.isDir {
				res.forget = append(res.forget, p.path)
			}
		case !here && (p.removed || p.renamed):
			raw, _ := json.Marshal(map[string]any{"isDir": p.isDir})
			res.events = append(res.events, emitted{p.first, Input{Type: TypeFSDeleted, Subject: p.path, Data: raw}})
			if p.isDir {
				res.forget = append(res.forget, p.path)
			}
		}
	}
	return res
}

func (rw *rootWatch) flush(ctx context.Context) {
	pending := rw.pending
	rw.pending = map[string]*pendingPath{}
	rescan := rw.rescan
	rw.rescan = false

	paths := make([]*pendingPath, 0, len(pending))
	for _, p := range pending {
		paths = append(paths, p)
	}
	sort.Slice(paths, func(i, j int) bool { return paths[i].first < paths[j].first })

	exists := map[string]os.FileInfo{}
	for _, p := range paths {
		if info, err := os.Lstat(p.path); err == nil {
			exists[p.path] = info
		}
	}

	res := resolveBatch(paths, exists, rw.transient, rw.relative)
	for _, dir := range res.forget {
		rw.forgetTree(dir)
	}
	out := res.events
	if rescan {
		rw.addTree(rw.real, false)
		out = append(out, emitted{0, Input{Type: TypeFSRescan}})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].order < out[j].order })
	if len(out) == 0 {
		return
	}
	ins := make([]Input, 0, len(out))
	for _, e := range out {
		e.in.Source = SourceWatcher
		ins = append(ins, e.in)
	}
	_, _ = rw.owner.events.AppendBatch(ctx, rw.root, ins)
}

func (rw *rootWatch) report(operation string, err error) {
	reportErr, _, end := rw.owner.cfg.Tracker.Start(context.Background(), operation, "workspace_files", "root", rw.root)
	reportErr(err)
	end()
}
