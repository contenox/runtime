package workflows

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sync"
	"time"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/workspaceevents"
	"github.com/google/uuid"
)

type Service struct {
	DB            libdb.DBManager
	WorkspaceID   string
	Model         ModelFunc
	AuthorizeRead func(context.Context, string) error
	edits         sync.Mutex
}

const prefix = "workflow-run/"

func (s *Service) scope(dir string) string { return workspaceevents.Scope(s.WorkspaceID, dir) }

type Invocation struct {
	Name, Revision, ID, Origin string
	EventNID                   int64
	Payload                    json.RawMessage
}

func (s *Service) Run(ctx context.Context, dir string, invocation Invocation) (Run, error) {
	name, revision, id, origin := invocation.Name, invocation.Revision, invocation.ID, invocation.Origin
	input := invocation.Payload
	if id == "" {
		id = uuid.NewString()
	}
	d, err := Load(dir, name)
	if err != nil {
		return Run{}, err
	}
	if revision == "" || revision != d.Revision {
		return Run{}, fmt.Errorf("%w: review and save the current workflow revision", ErrConflict)
	}
	now := time.Now().UTC()
	r := Run{EventNID: invocation.EventNID, ID: id, Name: name, Revision: revision, Origin: origin, Status: "running", Created: now, Updated: now}
	b, err := json.Marshal(r)
	if err != nil {
		return r, err
	}
	res, err := s.DB.WithoutTransaction().ExecContext(ctx, `INSERT INTO kv(key,workspace_id,value,revision,generation,created_at,updated_at) VALUES($1,$2,$3,0,$4,$5,$5) ON CONFLICT(key,workspace_id) DO NOTHING`, prefix+id, s.scope(dir), b, uuid.NewString(), now)
	if err != nil {
		return r, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return r, err
	}
	if n == 0 {
		return s.Get(ctx, dir, id)
	}
	if err = s.execute(ctx, dir, d, input, &r); err != nil {
		r.Status = "failed"
		r.Error = err.Error()
		r.Changes = nil
	} else if len(r.Changes) == 0 {
		r.Status = "completed"
	} else {
		r.Status = "pending"
	}
	finish := context.WithoutCancel(ctx)
	tx, commit, release, err := s.DB.WithTransaction(finish)
	if err != nil {
		return r, err
	}
	defer release()
	if len(r.Changes) > 0 {
		packet, err := json.Marshal(r.Changes)
		if err != nil {
			return r, err
		}
		if err = runtimetypes.New(tx).SetWorkspaceKV(finish, s.scope(dir), "workflow-packet/"+id, packet); err != nil {
			return r, err
		}
		r.HasPacket = true
	}
	if err = s.persist(finish, tx, dir, &r, 0); err != nil {
		return r, err
	}
	return r, commit(finish)
}

func (s *Service) persist(ctx context.Context, exec libdb.Exec, dir string, r *Run, rev int64) error {
	r.Updated = time.Now().UTC()
	header := *r
	if header.HasPacket {
		header.Changes = nil
	}
	b, err := json.Marshal(header)
	if err != nil {
		return err
	}
	res, err := exec.ExecContext(ctx, `UPDATE kv SET value=$1, updated_at=$2, revision=revision+1 WHERE key=$3 AND workspace_id=$4 AND revision=$5`, b, r.Updated, prefix+r.ID, s.scope(dir), rev)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n != 1 {
		return ErrConflict
	}
	return err
}
func (s *Service) load(ctx context.Context, exec libdb.Exec, dir, id string) (Run, int64, error) {
	var r Run
	var b []byte
	var rev int64
	err := exec.QueryRowContext(ctx, `SELECT value,revision FROM kv WHERE key=$1 AND workspace_id=$2`, prefix+id, s.scope(dir)).Scan(&b, &rev)
	if errors.Is(err, sql.ErrNoRows) {
		err = fs.ErrNotExist
	}
	if err == nil {
		err = json.Unmarshal(b, &r)
	}
	if err == nil && r.HasPacket {
		err = runtimetypes.New(exec).GetWorkspaceKV(ctx, s.scope(dir), "workflow-packet/"+id, &r.Changes)
	}
	r.markInterrupted()
	return r, rev, err
}
func (r *Run) markInterrupted() {
	// A terminated process can leave a model request unfinished. Never replay that call silently.
	if r.Status == "running" && time.Since(r.Created) > 3*time.Minute {
		r.Status = "failed"
		r.Error = "Execution was interrupted. Start a new run."
	}
}
func (s *Service) Get(ctx context.Context, dir, id string) (Run, error) {
	r, _, e := s.load(ctx, s.DB.WithoutTransaction(), dir, id)
	return r, e
}
func (s *Service) Recent(ctx context.Context, dir string) ([]Run, error) {
	rows, err := s.DB.WithoutTransaction().QueryContext(ctx, `SELECT value FROM kv WHERE workspace_id=$1 AND key LIKE 'workflow-run/%' ORDER BY created_at DESC,key DESC LIMIT 25`, s.scope(dir))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	runs := []Run{}
	for rows.Next() {
		var raw []byte
		var r Run
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &r); err != nil {
			return nil, err
		}
		r.markInterrupted()
		r.Changes = nil
		runs = append(runs, r)
	}
	return runs, rows.Err()
}
func (s *Service) Decide(ctx context.Context, dir, id, decision string) (Run, error) {
	if decision != "approve" && decision != "reject" {
		return Run{}, errors.New("choose approve or reject")
	}
	r, rev, err := s.load(ctx, s.DB.WithoutTransaction(), dir, id)
	if err != nil {
		return r, err
	}
	if decision == "reject" && r.Status == "rejected" || decision == "approve" && r.Status == "approved" {
		return r, nil
	}
	if decision == "reject" {
		if r.Status != "pending" {
			return r, ErrConflict
		}
		r.Status = "rejected"
		return r, s.persist(ctx, s.DB.WithoutTransaction(), dir, &r, rev)
	}
	if r.Status != "applying" && r.Status != "pending" {
		return r, ErrConflict
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return r, err
	}
	defer root.Close()
	resuming := r.Status == "applying"
	batch, err := stageProposal(root, r.Changes, resuming)
	if err != nil {
		return r, err
	}
	defer batch.close()
	if r.Status == "pending" {
		if r.RequireLatest && r.EventNID > 0 {
			recent, err := runtimetypes.NewEventStore(s.DB).ListRecentEvents(ctx, s.scope(dir), 0, 1, runtimetypes.EventQuery{Type: workspaceevents.TypeEventFired, Subject: r.Origin})
			if err != nil {
				return r, err
			}
			if len(recent) != 1 || recent[0].NID != r.EventNID {
				return r, fmt.Errorf("%w: a newer event superseded this proposal; review the latest run", ErrConflict)
			}
		}
		r.Status = "applying"
		if err = s.persist(ctx, s.DB.WithoutTransaction(), dir, &r, rev); err != nil {
			return r, err
		}
		rev++
	}
	// The durable applying state survives a process exit between file replacements.
	// Serialize decisions across runtimes sharing this database before touching files.
	tx, commit, release, err := s.DB.WithTransaction(ctx)
	if err != nil {
		return r, err
	}
	defer release()
	if err = s.persist(ctx, tx, dir, &r, rev); err != nil {
		return r, err
	}
	if applyErr := batch.apply(); applyErr != nil {
		if undoErr := batch.undo(); undoErr != nil {
			return r, errors.Join(applyErr, undoErr)
		}
		if !resuming {
			r.Status = "pending"
			if err := s.persist(ctx, tx, dir, &r, rev+1); err != nil {
				return r, errors.Join(applyErr, err)
			}
		}
		return r, errors.Join(applyErr, commit(ctx))
	}
	r.Status = "approved"
	if err = s.persist(ctx, tx, dir, &r, rev+1); err != nil {
		return r, err
	}
	return r, commit(ctx)
}
