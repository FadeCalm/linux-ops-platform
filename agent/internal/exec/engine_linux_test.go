//go:build linux

package exec

import (
	"testing"
	"time"

	"linux-ops-platform/agent/internal/config"
)

func waitForFinish(t *testing.T, e *Engine, id string, d time.Duration) TaskView {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if v, ok := e.Get(id); ok && v.Finished {
			return *v
		}
		time.Sleep(20 * time.Millisecond)
	}
	v, _ := e.Get(id)
	t.Fatalf("task %s did not finish; last=%+v", id, v)
	return TaskView{}
}

func TestEngineEchoSuccess(t *testing.T) {
	e := New(config.Default())
	if _, err := e.Submit(CreateRequest{TaskID: "t1", Command: "echo hello", TimeoutMS: 5000}); err != nil {
		t.Fatal(err)
	}
	v := waitForFinish(t, e, "t1", 5*time.Second)
	if v.Status != StatusSuccess {
		t.Fatalf("status = %s, want SUCCESS", v.Status)
	}
	if v.ExitCode == nil || *v.ExitCode != 0 {
		t.Fatalf("exit code = %v, want 0", v.ExitCode)
	}
	logs, _ := e.Logs("t1", 0, 0, 1024)
	if string(logs.Stdout) != "hello\n" {
		t.Fatalf("stdout = %q, want %q", logs.Stdout, "hello\n")
	}
}

func TestEngineNonZeroExit(t *testing.T) {
	e := New(config.Default())
	e.Submit(CreateRequest{TaskID: "t2", Command: "echo bad >&2; exit 3", TimeoutMS: 5000})
	v := waitForFinish(t, e, "t2", 5*time.Second)
	if v.Status != StatusFailed {
		t.Fatalf("status = %s, want FAILED", v.Status)
	}
	if v.ExitCode == nil || *v.ExitCode != 3 {
		t.Fatalf("exit code = %v, want 3", v.ExitCode)
	}
	logs, _ := e.Logs("t2", 0, 0, 1024)
	if string(logs.Stderr) != "bad\n" {
		t.Fatalf("stderr = %q, want bad", logs.Stderr)
	}
}

func TestEngineTimeout(t *testing.T) {
	e := New(config.Default())
	e.Submit(CreateRequest{TaskID: "t3", Command: "sleep 5", TimeoutMS: 300})
	v := waitForFinish(t, e, "t3", 5*time.Second)
	if v.Status != StatusTimeout {
		t.Fatalf("status = %s, want TIMEOUT", v.Status)
	}
}

func TestEngineCancel(t *testing.T) {
	e := New(config.Default())
	e.Submit(CreateRequest{TaskID: "t4", Command: "sleep 5", TimeoutMS: 10000})
	time.Sleep(150 * time.Millisecond)
	if !e.Cancel("t4") {
		// It may have just started; cancel should still be accepted.
		t.Log("cancel returned false")
	}
	v := waitForFinish(t, e, "t4", 5*time.Second)
	if v.Status != StatusCancelled {
		t.Fatalf("status = %s, want CANCELLED", v.Status)
	}
}

func TestEngineIdempotentSubmit(t *testing.T) {
	e := New(config.Default())
	a, _ := e.Submit(CreateRequest{TaskID: "t5", Command: "echo one", TimeoutMS: 5000})
	b, _ := e.Submit(CreateRequest{TaskID: "t5", Command: "echo two", TimeoutMS: 5000})
	if a != b {
		t.Fatal("second submit should return the same task record")
	}
	waitForFinish(t, e, "t5", 5*time.Second)
	logs, _ := e.Logs("t5", 0, 0, 1024)
	if string(logs.Stdout) != "one\n" {
		t.Fatalf("stdout = %q, want %q (second submit must not re-execute)", logs.Stdout, "one\n")
	}
}
