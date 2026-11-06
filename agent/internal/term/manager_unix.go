//go:build unix

package term

import (
	"encoding/json"
	"log"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
)

// Manager owns the agent's interactive terminal sessions.
type Manager struct {
	cfg Config
	sem chan struct{}

	mu       sync.Mutex
	sessions map[*session]struct{}
}

func NewManager(cfg Config) *Manager {
	if cfg.MaxSessions <= 0 {
		cfg.MaxSessions = 4
	}
	return &Manager{
		cfg:      cfg,
		sem:      make(chan struct{}, cfg.MaxSessions),
		sessions: make(map[*session]struct{}),
	}
}

// Acquire reserves one session slot. It returns false when the configured cap
// has been reached.
func (m *Manager) Acquire() bool {
	select {
	case m.sem <- struct{}{}:
		return true
	default:
		return false
	}
}

// Release frees a slot acquired with Acquire.
func (m *Manager) Release() { <-m.sem }

// Count reports the number of live sessions.
func (m *Manager) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sessions)
}

type session struct {
	conn *websocket.Conn
	ptmx *os.File
	cmd  *exec.Cmd

	// gorilla/websocket allows only one concurrent writer per connection, but
	// PTY output and terminal control messages are produced by different
	// goroutines. All writes go through this mutex.
	writeMu sync.Mutex

	// lastActivity is a UnixNano timestamp touched on every byte in either
	// direction; the idle watchdog reads it.
	lastActivity atomic.Int64
}

func (s *session) touch() { s.lastActivity.Store(time.Now().UnixNano()) }

func (s *session) write(mt int, data []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_ = s.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return s.conn.WriteMessage(mt, data)
}

func (s *session) sendControl(v interface{}) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	_ = s.write(websocket.TextMessage, b)
}

type controlIn struct {
	Type string `json:"type"`
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

type controlOut struct {
	Type    string `json:"type"`
	Message string `json:"message,omitempty"`
	Code    *int   `json:"code,omitempty"`
}

// Serve runs one interactive session until the PTY or the WebSocket closes.
// Release must be called separately (see Acquire).
func (m *Manager) Serve(conn *websocket.Conn) error {
	cfg := m.cfg
	if cfg.Shell == "" {
		cfg.Shell = "/bin/bash"
	}
	if cfg.WorkDir == "" {
		cfg.WorkDir = "/"
	}

	cmd := exec.Command(cfg.Shell)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	// "memory" keeps history usable inside the session but sends writes to
	// /dev/null, so the target user's own ~/.bash_history is neither read,
	// modified nor deleted. os/exec keeps the last duplicate key.
	if cfg.HistoryMode == "memory" {
		cmd.Env = append(cmd.Env, "HISTFILE=/dev/null")
	}
	cmd.Dir = cfg.WorkDir
	cmd.SysProcAttr = &syscall.SysProcAttr{}
	if cfg.User == "root" {
		// Only possible when the agent itself has the privilege to change uid;
		// the resulting failure is reported to the caller through the error
		// frame below.
		cmd.SysProcAttr.Credential = &syscall.Credential{Uid: 0, Gid: 0}
	}

	// pty.Start gives the child its own session and controlling terminal
	// (Setsid + Setctty), so -pid is also the process group id and a single
	// kill cleans up the whole shell tree.
	ptmx, err := pty.Start(cmd)
	if err != nil {
		b, _ := json.Marshal(controlOut{Type: "error", Message: "start shell: " + err.Error()})
		_ = conn.WriteMessage(websocket.TextMessage, b)
		return err
	}

	s := &session{conn: conn, ptmx: ptmx, cmd: cmd}
	s.touch()
	_ = pty.Setsize(ptmx, &pty.Winsize{Rows: 24, Cols: 80})

	m.mu.Lock()
	m.sessions[s] = struct{}{}
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.sessions, s)
		m.mu.Unlock()
	}()

	done := make(chan struct{})
	var once sync.Once
	stop := func() { once.Do(func() { close(done) }) }

	startedAt := time.Now()

	// Watchdog: enforce the maximum session duration and the idle timeout so
	// an abandoned browser tab cannot leave a shell running forever.
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		idle := time.Duration(cfg.IdleTimeoutSec) * time.Second
		maxDur := time.Duration(cfg.MaxDurationSec) * time.Second
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				last := time.Unix(0, s.lastActivity.Load())
				if maxDur > 0 && time.Since(startedAt) >= maxDur {
					s.sendControl(controlOut{
						Type:    "notice",
						Message: "会话已达到最大时长，连接被关闭",
					})
					stop()
					return
				}
				if idle > 0 && time.Since(last) >= idle {
					s.sendControl(controlOut{
						Type:    "notice",
						Message: "会话空闲超时，连接被关闭",
					})
					stop()
					return
				}
			}
		}
	}()

	// PTY -> browser.
	go func() {
		defer stop()
		buf := make([]byte, 32*1024)
		for {
			n, readErr := ptmx.Read(buf)
			if n > 0 {
				s.touch()
				if writeErr := s.write(websocket.BinaryMessage, buf[:n]); writeErr != nil {
					return
				}
			}
			if readErr != nil {
				return
			}
		}
	}()

	// Browser -> PTY. Binary frames are raw keystrokes, text frames carry
	// control messages (currently only resize).
	go func() {
		defer stop()
		for {
			mt, data, readErr := conn.ReadMessage()
			if readErr != nil {
				return
			}
			s.touch()
			if mt == websocket.TextMessage {
				var msg controlIn
				if err := json.Unmarshal(data, &msg); err != nil {
					continue
				}
				if msg.Type == "resize" && msg.Cols > 0 && msg.Rows > 0 {
					if err := pty.Setsize(ptmx, &pty.Winsize{Cols: msg.Cols, Rows: msg.Rows}); err != nil {
						log.Printf("[agent] terminal resize: %v", err)
					}
				}
				continue
			}
			if len(data) > 0 {
				if _, writeErr := ptmx.Write(data); writeErr != nil {
					return
				}
			}
		}
	}()

	<-done

	// Tear down: close the PTY, kill the whole session/process group, reap.
	_ = ptmx.Close()
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	_ = cmd.Wait()

	code := -1
	if cmd.ProcessState != nil {
		code = cmd.ProcessState.ExitCode()
	}
	s.sendControl(controlOut{Type: "exit", Code: &code})
	return nil
}
