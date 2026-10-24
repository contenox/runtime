package nativeturn

import (
	"context"

	"github.com/contenox/runtime/libacp"
)

type Event struct {
	Seq    uint64
	Update libacp.SessionNotification
}

type Viewer interface {
	ID() string

	Deliver(ctx context.Context, ev Event) error
}

type journal struct {
	buf   []Event
	start int
	count int
}

func newJournal(capacity int) *journal {
	if capacity < 0 {
		capacity = 0
	}
	return &journal{buf: make([]Event, capacity)}
}

func (j *journal) append(ev Event) {
	c := len(j.buf)
	if c == 0 {
		return
	}
	if j.count < c {
		j.buf[(j.start+j.count)%c] = ev
		j.count++
		return
	}
	j.buf[j.start] = ev
	j.start = (j.start + 1) % c
}

func (j *journal) snapshot() []Event {
	c := len(j.buf)
	out := make([]Event, j.count)
	for i := 0; i < j.count; i++ {
		out[i] = j.buf[(j.start+i)%c]
	}
	return out
}
