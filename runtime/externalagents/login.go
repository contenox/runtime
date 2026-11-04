package externalagents

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/contenox/runtime/apiframework"
	"github.com/contenox/runtime/libsandbox"
	"github.com/contenox/runtime/runtime/agenthost"
	"github.com/google/uuid"
)

const (
	LoginRunning   = "running"
	LoginWaiting   = "waiting_for_code"
	LoginSucceeded = "succeeded"
	LoginFailed    = "failed"
	LoginCancelled = "cancelled"
)

const (
	loginTimeout     = 15 * time.Minute
	loginCodeTimeout = 2 * time.Minute
)

var loginURLPattern = regexp.MustCompile(`https://[^\s"'<>]+`)

type LoginStatus struct {
	ID       string    `json:"id"`
	Agent    string    `json:"agent"`
	MethodID string    `json:"methodId"`
	State    string    `json:"state"`
	URL      string    `json:"url,omitempty"`
	Output   string    `json:"output,omitempty"`
	Problem  *Problem  `json:"problem,omitempty"`
	Started  time.Time `json:"startedAt"`
}

type loginJob struct {
	mu     sync.Mutex
	status LoginStatus
	out    boundedBuffer
	stdin  io.WriteCloser
	cancel context.CancelFunc
	done   chan struct{}
}

func (j *loginJob) snapshot() LoginStatus {
	j.mu.Lock()
	defer j.mu.Unlock()
	st := j.status
	st.Output = tail(j.out.String(), 2000)
	return st
}

func (j *loginJob) set(fn func(*LoginStatus)) {
	j.mu.Lock()
	fn(&j.status)
	j.mu.Unlock()
}

func (s *Service) StartLogin(ctx context.Context, name, methodID string) (LoginStatus, error) {
	a, err := s.get(ctx, name)
	if err != nil {
		return LoginStatus{}, err
	}
	cfg, err := a.ExternalACPConfig()
	if err != nil {
		return LoginStatus{}, err
	}
	shakeCtx, cancelShake := context.WithTimeout(ctx, checkTimeout)
	hs, problem := s.shake(shakeCtx, *cfg, false)
	cancelShake()
	if problem != nil {
		return LoginStatus{}, fmt.Errorf("%s: %w", problem.Message, apiframework.ErrUnprocessableEntity)
	}
	method, ok := hs.authMethod[methodID]
	if !ok {
		return LoginStatus{}, fmt.Errorf("agent %q offers no sign-in method %q: %w", name, methodID, apiframework.ErrInvalidParameterValue)
	}
	if method.Type != "terminal" {
		return LoginStatus{}, fmt.Errorf("sign-in method %q is of type %q, which Contenox cannot run yet: %w", methodID, method.Type, apiframework.ErrUnprocessableEntity)
	}

	s.cancelLogin(name)

	cwd, err := s.checkWorkspace()
	if err != nil {
		return LoginStatus{}, err
	}
	run := *cfg
	run.Cwd = cwd
	run.Args = append(append([]string(nil), cfg.Args...), method.Args...)
	env := map[string]string{"NO_BROWSER": "1", "BROWSER": "none"}
	for k, v := range cfg.Env {
		env[k] = v
	}
	for k, v := range method.Env {
		env[k] = v
	}
	run.Env = env

	runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), loginTimeout)
	cmd, err := (&agenthost.ExternalACPAgent{Config: run}).Command(runCtx)
	if err != nil {
		cancel()
		return LoginStatus{}, fmt.Errorf("%s: %w", startProblem(err, nil).Message, apiframework.ErrUnprocessableEntity)
	}
	hidden(cmd)
	job := &loginJob{
		status: LoginStatus{ID: uuid.NewString(), Agent: name, MethodID: methodID, State: LoginRunning, Started: time.Now().UTC()},
		out:    boundedBuffer{limit: 16384},
		cancel: cancel,
		done:   make(chan struct{}),
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return LoginStatus{}, err
	}
	job.stdin = stdin
	watcher := &urlWatcher{job: job}
	cmd.Stdout, cmd.Stderr = watcher, watcher
	startErr := cmd.Start()
	if hookErr := libsandbox.Started(cmd, startErr); hookErr != nil && startErr == nil {
		cancel()
		_ = cmd.Wait()
		return LoginStatus{}, fmt.Errorf("%v: %w", hookErr, apiframework.ErrUnprocessableEntity)
	}
	if startErr != nil {
		cancel()
		return LoginStatus{}, fmt.Errorf("%s: %w", startProblem(startErr, nil).Message, apiframework.ErrUnprocessableEntity)
	}

	s.mu.Lock()
	s.logins[name] = job
	s.mu.Unlock()

	go func() {
		select {
		case <-runCtx.Done():
			_ = cmd.Process.Kill()
		case <-job.done:
		}
	}()
	go func() {
		err := cmd.Wait()
		cancel()
		job.set(func(st *LoginStatus) {
			switch {
			case st.State == LoginCancelled:
			case err == nil:
				st.State = LoginSucceeded
			case errors.Is(runCtx.Err(), context.DeadlineExceeded):
				st.State = LoginFailed
				st.Problem = &Problem{Kind: "login_timed_out", Message: "sign-in was not finished in time"}
			default:
				st.State = LoginFailed
				st.Problem = &Problem{Kind: "login_failed", Message: "sign-in did not succeed", Detail: tail(job.out.String(), 2000)}
			}
		})
		close(job.done)
	}()

	deadline := time.After(10 * time.Second)
	for {
		st := job.snapshot()
		if st.URL != "" || st.State != LoginRunning {
			return st, nil
		}
		select {
		case <-job.done:
			return job.snapshot(), nil
		case <-deadline:
			return job.snapshot(), nil
		case <-time.After(100 * time.Millisecond):
		}
	}
}

type urlWatcher struct {
	job *loginJob
}

func (w *urlWatcher) Write(p []byte) (int, error) {
	n, _ := w.job.out.Write(p)
	all := w.job.out.String()
	w.job.set(func(st *LoginStatus) {
		if st.URL == "" {
			if u := loginURLPattern.FindString(all); u != "" {
				st.URL = strings.TrimRight(u, ".,)")
			}
		}
		if st.State == LoginRunning && st.URL != "" && strings.Contains(strings.ToLower(all), "code") {
			st.State = LoginWaiting
		}
	})
	return n, nil
}

func (s *Service) job(name string) (*loginJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job := s.logins[name]
	if job == nil {
		return nil, fmt.Errorf("no sign-in is running for %q: %w", name, apiframework.ErrNotFound)
	}
	return job, nil
}

func (s *Service) LoginStatus(name string) (LoginStatus, error) {
	job, err := s.job(name)
	if err != nil {
		return LoginStatus{}, err
	}
	return job.snapshot(), nil
}

func (s *Service) SubmitLoginCode(ctx context.Context, name, code string) (LoginStatus, error) {
	job, err := s.job(name)
	if err != nil {
		return LoginStatus{}, err
	}
	code = strings.TrimSpace(code)
	if code == "" {
		return LoginStatus{}, fmt.Errorf("code is required: %w", apiframework.ErrMissingParameter)
	}
	if st := job.snapshot(); st.State != LoginRunning && st.State != LoginWaiting {
		return st, nil
	}
	if _, err := io.WriteString(job.stdin, code+"\n"); err != nil {
		return LoginStatus{}, fmt.Errorf("hand the code to the agent: %w", err)
	}
	select {
	case <-job.done:
	case <-time.After(loginCodeTimeout):
	case <-ctx.Done():
	}
	return job.snapshot(), nil
}

func (s *Service) CancelLogin(name string) (LoginStatus, error) {
	job, err := s.job(name)
	if err != nil {
		return LoginStatus{}, err
	}
	s.cancelLogin(name)
	return job.snapshot(), nil
}

func (s *Service) cancelLogin(name string) {
	s.mu.Lock()
	job := s.logins[name]
	s.mu.Unlock()
	if job == nil {
		return
	}
	job.set(func(st *LoginStatus) {
		if st.State == LoginRunning || st.State == LoginWaiting {
			st.State = LoginCancelled
		}
	})
	job.cancel()
}
