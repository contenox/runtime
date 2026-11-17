package slot

import (
	"context"
	"crypto/sha256"
	"encoding/gob"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/contenox/runtime/internal/modeld/admission"
	"github.com/contenox/runtime/internal/modeld/llama"
	"github.com/contenox/runtime/internal/modeld/modelstore"
	"github.com/contenox/runtime/internal/modeld/snapshotauth"
	"github.com/contenox/runtime/internal/transport"
)

const snapshotTimeout = 10 * time.Second
const snapshotTTL = 24 * time.Hour
const snapshotMaxBytes int64 = 4 << 30

// WithSnapshotContext stops cache writes when the daemon loses ownership.
func WithSnapshotContext(ctx context.Context) Option {
	return func(s *Service) { s.snapshotContext = ctx }
}

type snapshotRecord struct {
	Key      string
	Snapshot transport.SessionSnapshot
}

type snapshotFile struct {
	path string
	size int64
	time time.Time
}

type snapshotWriter struct {
	writer    io.Writer
	remaining int64
	ctx       context.Context
}

type snapshotReader struct {
	reader io.Reader
	ctx    context.Context
}

func (r snapshotReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func (w *snapshotWriter) Write(p []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	if int64(len(p)) > w.remaining {
		return 0, fmt.Errorf("snapshot exceeds disk budget")
	}
	n, err := w.writer.Write(p)
	w.remaining -= int64(n)
	return n, err
}

func (s *Service) snapshotDir() string {
	if s.snapshotContext.Err() != nil {
		return ""
	}
	if os.Getenv("CONTENOX_WARM_SNAPSHOT_DISABLE") != "" {
		return ""
	}
	root := os.Getenv("CONTENOX_WARM_SNAPSHOT_DIR")
	if root == "" {
		dataRoot := s.snapshotRoot
		if dataRoot == "" {
			dataRoot = s.dataRoot
		}
		if dataRoot == "" {
			return ""
		}
		root = filepath.Join(dataRoot, "modeld-snapshots")
	}
	return filepath.Join(root, s.backendName, "slot-v1")
}

func (s *Service) snapshotKey(ctx context.Context, req transport.OpenSessionRequest, info transport.ModelInfo) string {
	if s.snapshotDir() == "" {
		return ""
	}
	executable, err := os.Executable()
	if err != nil {
		return ""
	}
	paths := []string{executable, req.Path}
	if stat, err := os.Stat(req.Path); err != nil {
		return ""
	} else if !stat.IsDir() {
		for _, sidecar := range []string{llama.ChatTemplatePathFor(req.Path), modelstore.ResolveMMProj(req.Path)} {
			if sidecar != "" {
				paths = append(paths, sidecar)
			}
		}
	}
	for _, adapter := range req.Adapters {
		paths = append(paths, adapter.Path)
	}
	h := sha256.New()
	for _, root := range paths {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			stat, err := os.Stat(path)
			if err != nil {
				return err
			}
			if !stat.Mode().IsRegular() {
				return fmt.Errorf("non-regular model asset")
			}
			signature := fmt.Sprintf("%s:%d:%d", path, stat.Size(), stat.ModTime().UnixNano())
			digest, ok := s.snapshotHashes[signature]
			if !ok {
				file, err := os.Open(path)
				if err != nil {
					return err
				}
				content := sha256.New()
				_, err = io.Copy(content, snapshotReader{reader: file, ctx: ctx})
				closeErr := file.Close()
				if err != nil || closeErr != nil {
					return errors.Join(err, closeErr)
				}
				digest = hex.EncodeToString(content.Sum(nil))
				s.snapshotHashes[signature] = digest
			}
			fmt.Fprintf(h, "%s\x00%s\x00", path, digest)
			return nil
		})
		if err != nil {
			s.snapshotEvent("snapshot_identity_failed", err)
			return ""
		}
	}
	req.Fence = transport.Fence{}
	req.ReclaimableBytes = 0
	if scope := snapshotauth.Scope(ctx); scope != "" {
		// Only durable identity crosses a reconnect. The slot and session
		// handles continue to use the server-fenced live namespace.
		req.Config.SessionNamespace = "local-snapshot/" + scope
		req.Config.AdmissionPermit = admission.Permit{}
		if req.Config.Execution != nil {
			execution := *req.Config.Execution
			execution.ClientID, execution.RequesterID = "", ""
			req.Config.Execution = &execution
		}
	}
	identity, err := json.Marshal(struct {
		Request transport.OpenSessionRequest
		Runtime string
		Context int
		Device  string
	}{req, info.RuntimeDigest, info.EffectiveContext, info.DeviceID})
	if err != nil {
		return ""
	}
	h.Write(identity)
	return hex.EncodeToString(h.Sum(nil))
}

func (s *Service) snapshotEvent(event string, err error) {
	reportErr, reportChange, end := s.tracker.Start(context.Background(), "modeld_slot", event)
	defer end()
	if err != nil {
		reportErr(err)
	} else {
		reportChange(event, nil)
	}
}

func (s *Service) captureSnapshot(parent context.Context, old *activeSlot) {
	dir := s.snapshotDir()
	if dir == "" || old.snapshotKey == "" {
		return
	}
	ctx, cancel := context.WithTimeout(parent, snapshotTimeout)
	defer cancel()
	stop := context.AfterFunc(s.snapshotContext, cancel)
	defer stop()
	if s.snapshotContext.Err() != nil {
		return
	}
	if ctx.Err() != nil {
		return
	}
	snap, err := old.sess.Snapshot(ctx)
	if err != nil {
		s.snapshotEvent("snapshot_capture_failed", err)
		return
	}
	if len(snap.State) == 0 {
		return
	}
	snap.StateID = ""
	if err := os.MkdirAll(dir, 0700); err != nil {
		s.snapshotEvent("snapshot_capture_failed", err)
		return
	}
	s.pruneSnapshots(dir)
	file, err := os.CreateTemp(dir, ".pending-")
	if err != nil {
		s.snapshotEvent("snapshot_capture_failed", err)
		return
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if _, err = file.Write(make([]byte, sha256.Size)); err == nil {
		h := sha256.New()
		writer := &snapshotWriter{writer: io.MultiWriter(file, h), remaining: snapshotMaxBytes - sha256.Size, ctx: ctx}
		err = gob.NewEncoder(writer).Encode(snapshotRecord{Key: old.snapshotKey, Snapshot: snap})
		if err == nil {
			_, err = file.WriteAt(h.Sum(nil), 0)
		}
	}
	if err == nil {
		err = file.Sync()
	}
	if err == nil {
		err = file.Close()
	}
	if err == nil {
		err = ctx.Err()
	}
	if err == nil {
		err = os.Rename(file.Name(), filepath.Join(dir, old.snapshotKey+".snap"))
	}
	if err != nil {
		s.snapshotEvent("snapshot_capture_failed", err)
		return
	}
	s.pruneSnapshots(dir)
	s.snapshotEvent("snapshot_captured", nil)
}

func (s *Service) readSnapshot(ctx context.Context, key string) (transport.SessionSnapshot, bool) {
	dir := s.snapshotDir()
	if dir == "" || key == "" {
		return transport.SessionSnapshot{}, false
	}
	s.pruneSnapshots(dir)
	path := filepath.Join(dir, key+".snap")
	file, err := os.Open(path)
	if err != nil {
		return transport.SessionSnapshot{}, false
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil || !stat.Mode().IsRegular() || stat.Size() > snapshotMaxBytes {
		return transport.SessionSnapshot{}, false
	}
	var expected [sha256.Size]byte
	_, err = io.ReadFull(file, expected[:])
	h := sha256.New()
	if err == nil {
		_, err = io.Copy(h, snapshotReader{reader: io.LimitReader(file, snapshotMaxBytes), ctx: ctx})
	}
	if err == nil && hex.EncodeToString(h.Sum(nil)) != hex.EncodeToString(expected[:]) {
		err = fmt.Errorf("snapshot checksum mismatch")
	}
	var record snapshotRecord
	if err == nil {
		_, err = file.Seek(sha256.Size, io.SeekStart)
	}
	if err == nil {
		err = gob.NewDecoder(snapshotReader{reader: io.LimitReader(file, snapshotMaxBytes), ctx: ctx}).Decode(&record)
	}
	if err == nil && (record.Key != key || len(record.Snapshot.State) == 0) {
		err = fmt.Errorf("snapshot identity or state missing")
	}
	if err != nil {
		_ = os.Remove(path)
		s.snapshotEvent("snapshot_discarded", err)
		return transport.SessionSnapshot{}, false
	}
	_ = os.Chtimes(path, s.now(), s.now())
	return record.Snapshot, true
}

func (s *Service) pruneSnapshots(dir string) {
	entries, _ := os.ReadDir(dir)
	var files []snapshotFile
	var total int64
	for _, entry := range entries {
		if s.snapshotContext.Err() != nil {
			return
		}
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), ".pending-") {
			// Another child sharing this directory may still be writing.
			if info, err := entry.Info(); err == nil && s.now().Sub(info.ModTime()) > 2*snapshotTimeout {
				_ = os.Remove(filepath.Join(dir, entry.Name()))
			}
			continue
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".snap") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if s.now().Sub(info.ModTime()) > snapshotTTL || info.Size() > snapshotMaxBytes {
			_ = os.Remove(path)
			continue
		}
		files = append(files, snapshotFile{path, info.Size(), info.ModTime()})
		total += info.Size()
	}
	sort.Slice(files, func(i, j int) bool { return files[i].time.Before(files[j].time) })
	for _, file := range files {
		if total <= snapshotMaxBytes {
			break
		}
		if os.Remove(file.path) == nil {
			total -= file.size
		}
	}
}

func (s *Service) openWithSnapshot(ctx context.Context, req transport.OpenSessionRequest, info transport.ModelInfo) (transport.Session, string, transport.ModelInfo, error) {
	sess, err := s.backend.OpenSession(ctx, req)
	if err != nil {
		return sess, "", info, err
	}
	report := sess.ExplainContext()
	if report.FatalError != "" || report.Closed {
		return sess, "", info, fmt.Errorf("native session report unavailable: closed=%t error=%s", report.Closed, report.FatalError)
	}
	if report.Execution != nil {
		info = report.Execution.Capacity
	}
	key := s.snapshotKey(ctx, req, info)
	restoreCtx, cancel := context.WithTimeout(ctx, snapshotTimeout)
	defer cancel()
	snap, ok := s.readSnapshot(restoreCtx, key)
	if !ok {
		s.snapshotEvent("snapshot_cold_open", nil)
		return sess, key, info, nil
	}
	err = sess.Restore(restoreCtx, snap)
	cancel()
	if err == nil {
		s.snapshotEvent("snapshot_restored", nil)
		return sess, key, info, nil
	}
	s.snapshotEvent("snapshot_restore_failed", err)
	if dir := s.snapshotDir(); dir != "" {
		_ = os.Remove(filepath.Join(dir, key+".snap"))
	}
	if err := sess.Close(); err != nil {
		return nil, key, info, err
	}
	sess, err = s.backend.OpenSession(ctx, req)
	if err == nil {
		report := sess.ExplainContext()
		if report.FatalError != "" || report.Closed {
			return sess, key, info, fmt.Errorf("native session report unavailable: closed=%t error=%s", report.Closed, report.FatalError)
		}
		if report.Execution != nil {
			info = report.Execution.Capacity
			key = s.snapshotKey(ctx, req, info)
		}
	}
	return sess, key, info, err
}

// Shutdown captures idle native state and closes the resident session after serving stops.
func (s *Service) Shutdown(ctx context.Context) error {
	unlock, err := s.lockOperation(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	s.stopped = true
	embedErr := s.releaseEmbeddings()
	s.mu.Lock()
	old := s.active
	s.active = nil
	s.generation++
	s.state = transport.SlotShuttingDown
	s.mu.Unlock()
	if old == nil {
		return embedErr
	}
	s.captureSnapshot(ctx, old)
	return errors.Join(embedErr, old.sess.Close())
}
