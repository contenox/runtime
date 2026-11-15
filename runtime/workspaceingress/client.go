package workspaceingress

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"path/filepath"
	"time"

	"github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/workspaceevents"
)

type Client struct {
	DB          libdbexec.DBManager
	WorkspaceID string
	Events      workspaceevents.Service
	Roots       workspaceevents.Roots
	Origin      string
	HTTP        *http.Client
}

type Binding struct {
	ApprovalURL string `json:"approvalUrl,omitempty"`
	Destination string `json:"destination"`
	Origin      string `json:"origin"`
	Cursor      int64  `json:"cursor"`
	Status      string `json:"status"`
	Gap         int64  `json:"expiredThrough,omitempty"`
	token       string
}
type delivery struct {
	ID, Topic string
	Payload   json.RawMessage
	Sequence  int64
	Received  time.Time
}
type page struct {
	Destination, Receipt    string
	Through, ExpiredThrough int64
	Events                  []delivery
}

func New(db libdbexec.DBManager, workspaceID string, events workspaceevents.Service, roots workspaceevents.Roots, origin string) *Client {
	return &Client{DB: db, WorkspaceID: workspaceID, Events: events, Roots: roots, Origin: origin, HTTP: &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (c *Client) binding(ctx context.Context, scope string) (Binding, error) {
	var b Binding
	err := c.DB.WithoutTransaction().QueryRowContext(ctx, `SELECT destination,origin,token,cursor,status,expired_through,approval_url FROM ingress_bindings WHERE scope=$1`, scope).Scan(&b.Destination, &b.Origin, &b.token, &b.Cursor, &b.Status, &b.Gap, &b.ApprovalURL)
	return b, err
}

func (c *Client) remote(ctx context.Context, method, url, token string, body, out any) (int, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(raw))
	if err != nil {
		return 0, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 409 && out != nil {
		err = json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(out)
		return resp.StatusCode, err
	}
	if resp.StatusCode/100 != 2 {
		return resp.StatusCode, fmt.Errorf("receiver returned HTTP %d", resp.StatusCode)
	}
	if out != nil {
		err = json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(out)
	}
	return resp.StatusCode, err
}

func (c *Client) Run(ctx context.Context) {
	type worker struct {
		stop context.CancelFunc
		done chan struct{}
	}
	workers := map[string]worker{}
	defer func() {
		for _, w := range workers {
			w.stop()
		}
		for _, w := range workers {
			<-w.done
		}
	}()
	t := time.NewTicker(3 * time.Second)
	defer t.Stop()
	for {
		active := map[string]bool{}
		for _, root := range c.Roots.Roots() {
			active[root] = true
			if _, exists := workers[root]; exists {
				continue
			}
			child, stop := context.WithCancel(ctx)
			w := worker{stop, make(chan struct{})}
			workers[root] = w
			go func() { defer close(w.done); c.poll(child, root) }()
		}
		for root, w := range workers {
			if !active[root] {
				w.stop()
				<-w.done
				delete(workers, root)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (c *Client) poll(ctx context.Context, root string) {
	scope := workspaceevents.Scope(c.WorkspaceID, root)
	delay := time.Second
	for ctx.Err() == nil {
		wait := 3 * time.Second
		b, err := c.binding(ctx, scope)
		if err == nil {
			more, err := c.sync(ctx, root, scope, b, false)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				c.DB.WithoutTransaction().ExecContext(ctx, "UPDATE ingress_bindings SET status=$3 WHERE scope=$1 AND destination=$2", scope, b.Destination, err.Error())
				wait = delay + time.Duration(rand.Int64N(int64(delay/2)))
				delay = min(time.Minute, 2*delay)
			} else {
				delay = time.Second
				if more {
					continue
				}
			}
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (c *Client) sync(ctx context.Context, root, scope string, b Binding, acceptGap bool) (bool, error) {
	if b.ApprovalURL != "" {
		var approved struct{ Token, Destination string }
		status, err := c.remote(ctx, "GET", b.Origin+"/v1/devices/approval", b.token, nil, &approved)
		if status == 410 {
			_, err = c.DB.WithoutTransaction().ExecContext(ctx, "DELETE FROM ingress_bindings WHERE scope=$1 AND token=$2", scope, b.token)
			return false, err
		}
		if err != nil || approved.Token == "" {
			return false, err
		}
		if len(approved.Token) != 43 || approved.Destination == "" {
			return false, errors.New("invalid approval response")
		}
		_, err = c.DB.WithoutTransaction().ExecContext(ctx, "UPDATE ingress_bindings SET destination=$3,token=$4,status='paired',approval_url='' WHERE scope=$1 AND token=$2", scope, b.token, approved.Destination, approved.Token)
		return err == nil, err
	}
	var p page
	status, err := c.remote(ctx, "GET", b.Origin+"/v1/deliveries?limit=100", b.token, nil, &p)
	if err != nil {
		return false, err
	}
	if status == 409 {
		if p.ExpiredThrough <= 0 || p.Receipt == "" {
			return false, errors.New("receiver cursor changed")
		}
		if !acceptGap {
			_, err = c.DB.WithoutTransaction().ExecContext(ctx, "UPDATE ingress_bindings SET status='retention gap',expired_through=$3 WHERE scope=$1 AND destination=$2", scope, b.Destination, p.ExpiredThrough)
			return false, err
		}
		if b.Gap != p.ExpiredThrough {
			return false, errors.New("expired range changed, review it again")
		}
		p.Through = p.ExpiredThrough
		_, err = c.DB.WithoutTransaction().ExecContext(ctx, "UPDATE ingress_bindings SET cursor=$3 WHERE scope=$1 AND destination=$2 AND cursor<$3", scope, b.Destination, p.Through)
		if err != nil {
			return false, err
		}
	} else {
		if p.Destination != b.Destination || len(p.Events) > 100 {
			return false, errors.New("unexpected receiver destination or page size")
		}
		if len(p.Events) == 0 {
			if p.Through != b.Cursor {
				return false, errors.New("local and hosted cursors disagree")
			}
			_, err = c.DB.WithoutTransaction().ExecContext(ctx, "UPDATE ingress_bindings SET status='connected',expired_through=0 WHERE scope=$1 AND destination=$2 AND (status<>'connected' OR expired_through<>0)", scope, b.Destination)
			return false, err
		}
		if p.Through != p.Events[len(p.Events)-1].Sequence {
			return false, errors.New("invalid receipt boundary")
		}
		for _, e := range p.Events {
			if err = c.persist(ctx, root, scope, b.Destination, e); err != nil {
				return false, err
			}
		}
	}
	_, err = c.remote(ctx, "POST", b.Origin+"/v1/deliveries/ack", b.token, map[string]any{"through": p.Through, "receipt": p.Receipt}, nil)
	return err == nil, err
}

func (c *Client) persist(ctx context.Context, root, scope, dest string, e delivery) error {
	if _, err := c.Events.GrantedRoot(root); err != nil {
		return err
	}
	if e.ID == "" || e.Sequence < 1 || e.Topic != "github.pull_request.opened" {
		return errors.New("invalid hosted event")
	}
	now := time.Now().UTC()
	event, err := workspaceevents.TopicRecord(scope, e.Topic, "github", e.Payload, now)
	if err != nil {
		return err
	}
	store := runtimetypes.NewEventStore(c.DB)
	if err = store.EnsureEventPartitionExists(ctx, now); err != nil {
		return err
	}
	tx, commit, release, err := c.DB.WithTransaction(ctx)
	if err != nil {
		return err
	}
	defer release()
	res, err := tx.ExecContext(ctx, "UPDATE ingress_bindings SET cursor=$3,status='connected',expired_through=0 WHERE scope=$1 AND destination=$2 AND cursor=$3-1", scope, dest, e.Sequence)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		var id string
		err = tx.QueryRowContext(ctx, "SELECT delivery_id FROM ingress_receipts WHERE scope=$1 AND destination=$2 AND seq=$3", scope, dest, e.Sequence).Scan(&id)
		if err != nil || id != e.ID {
			return errors.New("unexpected hosted delivery sequence")
		}
		return nil
	}
	if err = runtimetypes.AppendEventInTransaction(ctx, tx, &event); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO ingress_receipts(scope,destination,seq,delivery_id,nid) VALUES($1,$2,$3,$4,$5)", scope, dest, e.Sequence, e.ID, event.NID); err != nil {
		return err
	}
	if err = commit(ctx); err != nil {
		return err
	}
	c.Events.AnnounceCommitted(ctx, root, workspaceevents.Event{NID: event.NID, Type: event.Type, Subject: e.Topic, Source: event.Source, Time: now, Data: e.Payload})
	return nil
}

func (c *Client) AddRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /workspace/ingress", c.handle)
	m.HandleFunc("POST /workspace/ingress", c.handle)
	m.HandleFunc("DELETE /workspace/ingress", c.handle)
	m.HandleFunc("POST /workspace/ingress/gap", c.handle)
	m.HandleFunc("POST /workspace/ingress/connect", c.handle)
}

func (c *Client) handle(w http.ResponseWriter, r *http.Request) {
	root, err := c.Events.GrantedRoot(r.URL.Query().Get("root"))
	if err != nil {
		http.Error(w, "select a workspace", 400)
		return
	}
	scope := workspaceevents.Scope(c.WorkspaceID, root)
	ctx := r.Context()
	b, err := c.binding(ctx, scope)
	missing := errors.Is(err, sql.ErrNoRows)
	if err != nil && !missing {
		http.Error(w, "binding unavailable", 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	switch {
	case r.Method == "GET":
		if missing {
			json.NewEncoder(w).Encode(map[string]string{"origin": c.Origin})
			return
		}
		json.NewEncoder(w).Encode(b)
		return
	case r.Method == "DELETE":
		_, err = c.DB.WithoutTransaction().ExecContext(ctx, "DELETE FROM ingress_bindings WHERE scope=$1", scope)
	case r.URL.Path == "/workspace/ingress/connect":
		if !missing {
			http.Error(w, "workspace already paired or awaiting approval", 409)
			return
		}
		var out struct{ Request, Poll string }
		_, err = c.remote(ctx, "POST", c.Origin+"/v1/devices", "", map[string]string{"label": filepath.Base(root)}, &out)
		if err == nil {
			if len(out.Poll) != 43 || len(out.Request) != 36 {
				http.Error(w, "invalid receiver response", 502)
				return
			}
			approval := c.Origin + "/connect?request=" + url.QueryEscape(out.Request)
			_, err = c.DB.WithoutTransaction().ExecContext(ctx, "INSERT INTO ingress_bindings(scope,destination,origin,token,status,approval_url) VALUES($1,'',$2,$3,'awaiting approval',$4)", scope, c.Origin, out.Poll, approval)
			if err == nil {
				json.NewEncoder(w).Encode(map[string]string{"url": approval})
				return
			}
		}
	case r.URL.Path == "/workspace/ingress/gap":
		if missing || b.Gap == 0 {
			http.Error(w, "no gap to acknowledge", 409)
			return
		}
		_, err = c.sync(ctx, root, scope, b, true)
	default:
		if !missing {
			http.Error(w, "workspace already paired", 409)
			return
		}
		var in struct{ Code string }
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048)).Decode(&in) != nil || len(in.Code) != 43 {
			http.Error(w, "invalid pairing code", 400)
			return
		}
		var out struct{ Token, Destination string }
		_, err = c.remote(ctx, "POST", c.Origin+"/v1/pair/redeem", "", in, &out)
		if err == nil {
			if out.Destination == "" || len(out.Token) != 43 {
				http.Error(w, "invalid receiver response", 502)
				return
			}
			_, err = c.DB.WithoutTransaction().ExecContext(ctx, "INSERT INTO ingress_bindings(scope,destination,origin,token) VALUES($1,$2,$3,$4)", scope, out.Destination, c.Origin, out.Token)
		}
	}
	if err != nil {
		http.Error(w, "receiver operation failed", 422)
		return
	}
	w.Write([]byte("true"))
}
