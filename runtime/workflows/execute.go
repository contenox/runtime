package workflows

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/contenox/runtime/runtime/vfs"
	"github.com/dop251/goja"
)

type Usage struct {
	Provider     string `json:"provider"`
	Model        string `json:"model"`
	Milliseconds int64  `json:"milliseconds"`
	Input        int    `json:"inputTokens,omitempty"`
	Output       int    `json:"outputTokens,omitempty"`
}
type ModelFunc func(context.Context, string) (string, Usage, error)
type Change struct {
	Path       string `json:"path"`
	BeforeHash string `json:"beforeHash"`
	Before     string `json:"before,omitempty"`
	After      string `json:"after,omitempty"`
	Binary     []byte `json:"binary,omitempty"`
}

func (c Change) bytes() []byte {
	if c.Binary != nil {
		return c.Binary
	}
	return []byte(c.After)
}

type Run struct {
	EventNID      int64     `json:"eventNid,omitempty"`
	RequireLatest bool      `json:"requireLatest,omitempty"`
	HasPacket     bool      `json:"hasPacket,omitempty"`
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Revision      string    `json:"revision"`
	Origin        string    `json:"origin"`
	Status        string    `json:"status"`
	Summary       string    `json:"summary"`
	Error         string    `json:"error,omitempty"`
	Created       time.Time `json:"created"`
	Updated       time.Time `json:"updated"`
	Changes       []Change  `json:"changes,omitempty"`
	Model         *Usage    `json:"model,omitempty"`
}

func (s *Service) execute(ctx context.Context, dir string, d Definition, input json.RawMessage, r *Run) error {
	prog, err := compile(d.Name, d.Source)
	if err != nil {
		return err
	}
	if len(input) > 32<<10 || !json.Valid(input) {
		return errors.New("event must be JSON under 32 KiB")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	vm := goja.New()
	vm.SetMaxCallStackSize(256)
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { vm.Interrupt("workflow cancelled") })
	defer stop()
	timer := time.AfterFunc(3*time.Second, func() { vm.Interrupt("JavaScript execution time exceeded") })
	defer timer.Stop()
	// Native I/O observes its own context deadline. Charge the JS budget only while executing JS.
	remaining := 3 * time.Second
	since := time.Now()
	native := func(work func() error) error {
		if !timer.Stop() {
			return errors.New("execution time exceeded")
		}
		remaining -= time.Since(since)
		err := work()
		since = time.Now()
		timer.Reset(remaining)
		return err
	}
	fail := func(err error) {
		if err != nil {
			panic(vm.NewTypeError("%s", err))
		}
	}
	host := vm.NewObject()
	snapshots := map[string][]byte{}
	missing := map[string]bool{}
	load := func(name string) ([]byte, error) {
		file, err := documentPath(name)
		if err != nil {
			return nil, err
		}
		if b, ok := snapshots[name]; ok {
			return b, nil
		}
		if len(snapshots) >= 16 {
			return nil, errors.New("at most sixteen documents per run")
		}
		if strings.EqualFold(path.Ext(name), ".pdf") {
			return nil, fmt.Errorf("unsupported text document %q", name)
		}
		if s.AuthorizeRead == nil {
			return nil, errors.New("workflow document read policy is unavailable")
		}
		if err := native(func() error { return s.AuthorizeRead(ctx, name) }); err != nil {
			return nil, err
		}
		parent, leaf, err := vfs.OpenFileParent(root, file, false)
		var b []byte
		if err == nil {
			defer parent.Close()
			b, err = readDocument(parent, leaf, maxDocument)
		}
		if errors.Is(err, fs.ErrNotExist) {
			missing[name] = true
			b = nil
			err = nil
		}
		if err == nil {
			snapshots[name] = b
		}
		return b, err
	}
	host.Set("requireLatestEvent", func() { r.RequireLatest = true })
	host.Set("read", func(name string) string {
		b, err := load(name)
		fail(err)
		if missing[name] || !utf8.Valid(b) {
			fail(fmt.Errorf("cannot read %s as text", name))
		}
		return string(b)
	})
	host.Set("propose", func(name, content string) {
		if len(content) > maxDocument || len(r.Changes) >= 8 {
			fail(errors.New("proposal exceeds document limits"))
		}
		b, err := load(name)
		fail(err)
		for _, c := range r.Changes {
			if strings.EqualFold(name, c.Path) {
				fail(fmt.Errorf("duplicate proposal for %s", name))
			}
		}
		c := Change{Path: name, After: content}
		if !missing[name] {
			c.BeforeHash = digest(b)
			if !utf8.Valid(b) {
				fail(errors.New("expected UTF-8 document"))
			}
			c.Before = string(b)
		}
		r.Changes = append(r.Changes, c)
	})
	host.Set("ask", func(prompt string) string {
		if s.Model == nil || r.Model != nil || len(prompt) > 32<<10 {
			fail(errors.New("one model request of at most 32 KiB is available per run"))
		}
		var answer string
		var usage Usage
		err := native(func() error { var e error; answer, usage, e = s.Model(ctx, prompt); return e })
		r.Model = &usage
		fail(err)
		if len(answer) > maxDocument {
			fail(errors.New("model response exceeds limit"))
		}
		return answer
	})
	if err := vm.Set("workflow", host); err != nil {
		return err
	}
	// Parse event input inside the VM to give scripts only native JavaScript values.
	arg, err := vm.RunString("JSON.parse(" + string(mustJSON(string(input))) + ")")
	if err != nil {
		return err
	}
	if _, err = vm.RunProgram(prog); err != nil {
		return err
	}
	fn, ok := goja.AssertFunction(vm.Get("run"))
	if !ok {
		return errors.New("define function run(event)")
	}
	result, err := fn(goja.Undefined(), arg)
	if err != nil {
		return err
	}
	if _, ok := result.(goja.String); !ok {
		return errors.New("return a review summary string")
	}
	r.Summary = result.String()
	if len(r.Summary) > 2048 {
		return errors.New("review summary exceeds 2 KiB")
	}
	return nil
}
func mustJSON(v string) []byte { b, _ := json.Marshal(v); return b }
