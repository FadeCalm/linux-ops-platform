// Package exec implements the non-interactive command execution engine.
package exec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"

	"linux-ops-platform/agent/internal/config"
)

const (
	StatusPending   = "PENDING"
	StatusRunning   = "RUNNING"
	StatusSuccess   = "SUCCESS"
	StatusFailed    = "FAILED"
	StatusTimeout   = "TIMEOUT"
	StatusCancelled = "CANCELLED"
)

type CreateRequest struct {
	TaskID    string `json:"task_id"`
	Command   string `json:"command"`
	WorkDir   string `json:"work_dir"`
	TimeoutMS int64  `json:"timeout_ms"`
}

// Task is the in-memory record of one execution.
type Task struct {
	ID        string
	Command   string
	WorkDir   string
	TimeoutMS int64

	ctx    context.Context
	cancel context.CancelFunc

	mu         sync.Mutex
	status     string
	exitCode   *int
	errMsg     string
	startedAt  time.Time
	finishedAt *time.Time
	cancelled  bool

	stdout *cappedBuffer
	stderr *cappedBuffer
}

type TaskView struct {
	TaskID     string `json:"task_id"`
	Status     string `json:"status"`
	ExitCode   *int   `json:"exit_code"`
	DurationMS int64  `json:"duration_ms"`
	Error      string `json:"error"`
	Finished   bool   `json:"finished"`
}

type LogsView struct {
	TaskID       string `json:"task_id"`
	Status       string `json:"status"`
	ExitCode     *int   `json:"exit_code"`
	DurationMS   int64  `json:"duration_ms"`
	StdoutOffset int64  `json:"stdout_offset"`
	StderrOffset int64  `json:"stderr_offset"`
	Stdout       []byte `json:"-"`
	Stderr       []byte `json:"-"`
	Truncated    bool   `json:"truncated"`
	Finished     bool   `json:"finished"`
	Error        string `json:"error"`
}

type Engine struct {
	cfg *config.Config
	sem chan struct{}

	mu    sync.RWMutex
	tasks map[string]*Task
}

func New(cfg *config.Config) *Engine {
	return &Engine{
		cfg:   cfg,
		sem:   make(chan struct{}, cfg.MaxConcurrent),
		tasks: map[string]*Task{},
	}
}

// Submit validates and starts a task. Re-submitting an existing task_id is
// idempotent: the current record is returned without re-executing.
func (e *Engine) Submit(req CreateRequest) (*Task, error) {
	if req.TaskID == "" {
		return nil, errors.New("task_id is required")
	}
	if req.Command == "" {
		return nil, errors.New("command is required")
	}
	if len(req.Command) > e.cfg.MaxScriptBytes {
		return nil, fmt.Errorf("command exceeds %d bytes", e.cfg.MaxScriptBytes)
	}
	e.mu.Lock()
	if t, ok := e.tasks[req.TaskID]; ok {
		e.mu.Unlock()
		return t, nil
	}
	timeout := req.TimeoutMS
	if timeout <= 0 {
		timeout = e.cfg.DefaultTimeoutMS
	}
	if timeout > e.cfg.MaxTimeoutMS {
		timeout = e.cfg.MaxTimeoutMS
	}
	ctx, cancel := context.WithCancel(context.Background())
	t := &Task{
		ID: req.TaskID, Command: req.Command, WorkDir: req.WorkDir, TimeoutMS: timeout,
		ctx: ctx, cancel: cancel, status: StatusPending,
		stdout: newCappedBuffer(e.cfg.MaxOutputBytes),
		stderr: newCappedBuffer(e.cfg.MaxOutputBytes),
	}
	e.tasks[req.TaskID] = t
	e.mu.Unlock()
	go e.run(t)
	return t, nil
}

func (e *Engine) run(t *Task) {
	select {
	case e.sem <- struct{}{}:
		defer func() { <-e.sem }()
	case <-t.ctx.Done():
		t.finish(StatusCancelled, nil, "cancelled before start")
		return
	}

	execCtx, cancel := context.WithTimeout(t.ctx, time.Duration(t.TimeoutMS)*time.Millisecond)
	defer cancel()
	t.start()

	cmd := exec.CommandContext(execCtx, "/bin/bash", "-c", t.Command)
	if t.WorkDir != "" {
		cmd.Dir = t.WorkDir
	}
	configureProcessGroup(cmd)

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		t.finish(StatusFailed, nil, "stdout pipe: "+err.Error())
		return
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		t.finish(StatusFailed, nil, "stderr pipe: "+err.Error())
		return
	}
	if err := cmd.Start(); err != nil {
		t.finish(StatusFailed, nil, "start: "+err.Error())
		return
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _, _ = io.Copy(t.stdout, stdoutPipe) }()
	go func() { defer wg.Done(); _, _ = io.Copy(t.stderr, stderrPipe) }()

	waitErr := cmd.Wait()
	wg.Wait()

	exitCode := -1
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}
	status := StatusSuccess
	msg := ""
	switch {
	case errors.Is(execCtx.Err(), context.DeadlineExceeded):
		status = StatusTimeout
		msg = "execution timed out"
	case t.isCancelled():
		status = StatusCancelled
		msg = "cancelled"
	case waitErr != nil:
		status = StatusFailed
		msg = waitErr.Error()
	}
	if status == StatusSuccess && exitCode != 0 {
		status = StatusFailed
	}
	t.finish(status, &exitCode, msg)
}

func (t *Task) start() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.status = StatusRunning
	t.startedAt = time.Now().UTC()
}

func (t *Task) finish(status string, exitCode *int, msg string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now().UTC()
	t.status = status
	t.exitCode = exitCode
	t.errMsg = msg
	t.finishedAt = &now
}

func (t *Task) isCancelled() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cancelled
}

func (t *Task) view() TaskView {
	t.mu.Lock()
	defer t.mu.Unlock()
	v := TaskView{
		TaskID: t.ID, Status: t.status, ExitCode: t.exitCode,
		Error: t.errMsg, Finished: isTerminal(t.status),
	}
	if t.finishedAt != nil {
		v.DurationMS = t.finishedAt.Sub(t.startedAt).Milliseconds()
	}
	return v
}

func (e *Engine) Get(id string) (*TaskView, bool) {
	e.mu.RLock()
	t, ok := e.tasks[id]
	e.mu.RUnlock()
	if !ok {
		return nil, false
	}
	v := t.view()
	return &v, true
}

// Logs returns the accumulated output from the given offsets.
func (e *Engine) Logs(id string, stdoutOffset, stderrOffset int64, limit int) (*LogsView, bool) {
	e.mu.RLock()
	t, ok := e.tasks[id]
	e.mu.RUnlock()
	if !ok {
		return nil, false
	}
	t.mu.Lock()
	status := t.status
	exit := t.exitCode
	started := t.startedAt
	finished := t.finishedAt
	errMsg := t.errMsg
	t.mu.Unlock()

	out := t.stdout.Slice(stdoutOffset, limit)
	errOut := t.stderr.Slice(stderrOffset, limit)
	v := &LogsView{
		TaskID: id, Status: status, ExitCode: exit,
		StdoutOffset: stdoutOffset + int64(len(out)),
		StderrOffset: stderrOffset + int64(len(errOut)),
		Stdout:       out, Stderr: errOut,
		Truncated: t.stdout.Truncated() || t.stderr.Truncated(),
		Finished:  isTerminal(status), Error: errMsg,
	}
	if finished != nil {
		v.DurationMS = finished.Sub(started).Milliseconds()
	}
	return v, true
}

// Cancel requests termination of the task's process group.
func (e *Engine) Cancel(id string) bool {
	e.mu.RLock()
	t, ok := e.tasks[id]
	e.mu.RUnlock()
	if !ok {
		return false
	}
	t.mu.Lock()
	if isTerminal(t.status) {
		t.mu.Unlock()
		return false
	}
	t.cancelled = true
	t.mu.Unlock()
	t.cancel()
	return true
}

func (e *Engine) RunningCount() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	n := 0
	for _, t := range e.tasks {
		t.mu.Lock()
		if t.status == StatusRunning {
			n++
		}
		t.mu.Unlock()
	}
	return n
}

// Cleanup removes finished tasks older than the retention window.
func (e *Engine) Cleanup() {
	cutoff := time.Now().UTC().Add(-time.Duration(e.cfg.TaskRetentionSec) * time.Second)
	e.mu.Lock()
	defer e.mu.Unlock()
	for id, t := range e.tasks {
		t.mu.Lock()
		expired := t.finishedAt != nil && t.finishedAt.Before(cutoff)
		t.mu.Unlock()
		if expired {
			delete(e.tasks, id)
		}
	}
}

func isTerminal(status string) bool {
	switch status {
	case StatusSuccess, StatusFailed, StatusTimeout, StatusCancelled:
		return true
	}
	return false
}
