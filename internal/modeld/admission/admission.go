package admission

import (
	"context"
	"errors"
	"time"
)

// ErrQueueFull means the worker's waiting queue has reached its configured bound.
var ErrQueueFull = errors.New("admission queue full")

// ErrSuperseded means a newer request replaced work from the same requester.
var ErrSuperseded = errors.New("admission request superseded")

// ErrWorkerNotFound means the worker has not registered with the scheduler.
var ErrWorkerNotFound = errors.New("admission worker not found")

// ErrOwnerEpoch means the request does not belong to the registered worker owner.
var ErrOwnerEpoch = errors.New("admission owner epoch changed")

// ErrNotReady means the worker cannot accept work.
var ErrNotReady = errors.New("admission worker not ready")

// ErrDraining means the worker is retiring and cannot accept work.
var ErrDraining = errors.New("admission worker draining")

// ErrWorkerBusy means an owner cannot be replaced until its execution has ended.
var ErrWorkerBusy = errors.New("admission worker still executing")

// ErrInvalidPermit means the permit was not issued by this scheduler.
var ErrInvalidPermit = errors.New("invalid admission permit")

// Priority is the scheduling class of a request.
type Priority string

const (
	// Autocomplete identifies editor completions ahead of Chat and Recovery.
	Autocomplete Priority = "autocomplete"
	// Foreground identifies interactive work.
	Foreground Priority = "foreground"
	// Background identifies work that can wait behind interactive requests.
	Background Priority = "background"
)

// Config sets explicit queue and fairness bounds; GlobalLimit zero is unlimited.
// ForegroundBurst bounds interactive grants before background work and autocomplete
// grants before other interactive work. QueueCapacity excludes active execution.
type Config struct {
	QueueCapacity   int
	GlobalLimit     int
	ForegroundBurst int
	// MaxQueueWait bounds unaccepted requests; zero leaves the caller's deadline authoritative.
	MaxQueueWait time.Duration
}

// DefaultConfig is the shipped interactive admission policy.
func DefaultConfig() Config {
	return Config{QueueCapacity: 32, ForegroundBurst: 8, MaxQueueWait: 30 * time.Second}
}

// Worker identifies one execution slot and the fencing epoch of its owner.
type Worker struct {
	ID         string
	OwnerEpoch string
	Ready      bool
	Draining   bool
	// Stopped confirms native execution has ended and held permits can retire.
	Stopped bool
}

// Request describes waiting work. Supersede replaces queued work on the worker
// and requests cancellation of active Autocomplete from the same requester.
// Active permits remain held until execution stops and the caller releases them.
type Request struct {
	WorkerID    string
	OwnerEpoch  string
	RequesterID string
	Priority    Priority
	Supersede   bool
}

// Permit grants execution on one worker until explicitly released. Cancellation
// after Acquire returns does not release it; the caller must first stop execution.
type Permit struct {
	ID         uint64 `json:"ID"`
	WorkerID   string `json:"WorkerID"`
	OwnerEpoch string `json:"OwnerEpoch"`
}

// WorkerSnapshot reports one worker's registration, active permit and queue sizes.
type WorkerSnapshot struct {
	Worker
	Suspended             bool
	YieldRequested        bool
	CancellationRequested bool
	Active                *Permit
	QueuedForeground      int
	QueuedBackground      int
}

// Snapshot reports admission state with workers ordered by ID.
type Snapshot struct {
	Resources   Resources
	Policy      Config
	GlobalLimit int
	Active      int
	Workers     []WorkerSnapshot
}

// Service is the admission contract independent of worker execution and transport.
type Service interface {
	Register(context.Context, Worker) error
	Update(context.Context, Worker) error
	Acquire(context.Context, Request) (Permit, error)
	WaitCancellation(context.Context, Permit) error
	Release(context.Context, Permit) error
	Snapshot(context.Context) (Snapshot, error)
}
