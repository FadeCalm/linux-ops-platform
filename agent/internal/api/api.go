// Package api exposes the agent's HTTP API. Phase 1 has no authentication;
// the agent must not be publicly reachable.
package api

import (
	"encoding/base64"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"time"

	"linux-ops-platform/agent/internal/config"
	"linux-ops-platform/agent/internal/exec"
	"linux-ops-platform/agent/internal/term"
	"linux-ops-platform/agent/internal/update"
)

type Server struct {
	cfg     *config.Config
	engine  *exec.Engine
	term    *term.Manager
	updater *update.Manager
	version string
	started time.Time
}

func New(cfg *config.Config, engine *exec.Engine, version string) *Server {
	return &Server{
		cfg:    cfg,
		engine: engine,
		term: term.NewManager(term.Config{
			Shell:          cfg.Terminal.Shell,
			WorkDir:        cfg.Terminal.WorkDir,
			User:           cfg.Terminal.User,
			MaxSessions:    cfg.Terminal.MaxSessions,
			IdleTimeoutSec: cfg.Terminal.IdleTimeoutSec,
			MaxDurationSec: cfg.Terminal.MaxDurationSec,
		}),
		updater: update.New(cfg.UpdateBaseURL, version),
		version: version,
		started: time.Now(),
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", s.health)
	mux.HandleFunc("GET /api/v1/info", s.info)
	mux.HandleFunc("GET /api/v1/version", s.versionHandler)
	mux.HandleFunc("POST /api/v1/tasks", s.createTask)
	mux.HandleFunc("GET /api/v1/tasks/{id}", s.getTask)
	mux.HandleFunc("GET /api/v1/tasks/{id}/logs", s.getLogs)
	mux.HandleFunc("POST /api/v1/tasks/{id}/cancel", s.cancelTask)

	mux.HandleFunc("GET /api/v1/fs/list", s.fsList)
	mux.HandleFunc("GET /api/v1/fs/stat", s.fsStat)
	mux.HandleFunc("GET /api/v1/fs/read", s.fsRead)
	mux.HandleFunc("PUT /api/v1/fs/write", s.fsWrite)
	mux.HandleFunc("GET /api/v1/fs/download", s.fsDownload)
	mux.HandleFunc("PUT /api/v1/fs/upload", s.fsUpload)
	mux.HandleFunc("POST /api/v1/fs/op", s.fsOp)

	mux.HandleFunc("GET /api/v1/ws/terminal", s.terminalWS)

	mux.HandleFunc("GET /api/v1/update/check", s.updateCheck)
	mux.HandleFunc("POST /api/v1/update", s.updateApply)
	return mux
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "ok", "uptime_sec": int64(time.Since(s.started).Seconds()),
	})
}

func (s *Server) info(w http.ResponseWriter, r *http.Request) {
	hostname, _ := os.Hostname()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"hostname": hostname, "os": runtime.GOOS, "arch": runtime.GOARCH,
		"version": s.version, "uptime_sec": int64(time.Since(s.started).Seconds()),
		"running_tasks": s.engine.RunningCount(), "max_concurrent": s.cfg.MaxConcurrent,
		"terminal_sessions": s.term.Count(),
	})
}

func (s *Server) versionHandler(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"version": s.version})
}

func (s *Server) createTask(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, int64(s.cfg.MaxScriptBytes)+(1<<20))
	var req exec.CreateRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	t, err := s.engine.Submit(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	view, _ := s.engine.Get(t.ID)
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) getTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	view, ok := s.engine.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) getLogs(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	so, _ := strconv.ParseInt(r.URL.Query().Get("stdout_offset"), 10, 64)
	se, _ := strconv.ParseInt(r.URL.Query().Get("stderr_offset"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 1<<20 {
		limit = 256 * 1024
	}
	v, ok := s.engine.Logs(id, so, se, limit)
	if !ok {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"task_id": v.TaskID, "status": v.Status, "exit_code": v.ExitCode,
		"duration_ms": v.DurationMS, "stdout_offset": v.StdoutOffset,
		"stderr_offset": v.StderrOffset,
		"stdout":        base64.StdEncoding.EncodeToString(v.Stdout),
		"stderr":        base64.StdEncoding.EncodeToString(v.Stderr),
		"truncated":     v.Truncated, "finished": v.Finished, "error": v.Error,
	})
}

func (s *Server) cancelTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.engine.Get(id); !ok {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}
	cancelled := s.engine.Cancel(id)
	writeJSON(w, http.StatusOK, map[string]interface{}{"cancelling": cancelled})
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("[agent] write response: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
