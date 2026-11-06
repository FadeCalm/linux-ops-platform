// Package scheduler dispatches tasks to agents, follows their progress by
// pulling log/status by cursor, persists output, fans frames out over
// WebSocket and maintains the task state machine.
package scheduler

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"linux-ops-platform/backend/internal/agentclient"
	"linux-ops-platform/backend/internal/config"
	"linux-ops-platform/backend/internal/logstore"
	"linux-ops-platform/backend/internal/models"
	"linux-ops-platform/backend/internal/ws"

	"gorm.io/gorm"
)

const maxConsecutivePollFailures = 20

type runState struct {
	cancel     context.CancelFunc
	userCancel bool
}

type Scheduler struct {
	cfg   *config.Config
	db    *gorm.DB
	agent *agentclient.Client
	store *logstore.Store
	hub   *ws.Hub
	sem   chan struct{}

	rootCtx context.Context
	stop    context.CancelFunc
	wg      sync.WaitGroup

	mu      sync.Mutex
	running map[string]*runState

	parentMu sync.Mutex
}

func New(cfg *config.Config, db *gorm.DB, agent *agentclient.Client, store *logstore.Store, hub *ws.Hub) *Scheduler {
	return &Scheduler{
		cfg:     cfg,
		db:      db,
		agent:   agent,
		store:   store,
		hub:     hub,
		sem:     make(chan struct{}, cfg.Scheduler.DispatchConcurrency),
		running: map[string]*runState{},
	}
}

// Start launches heartbeats and recovers unfinished targets from a previous
// process lifetime.
func (s *Scheduler) Start(ctx context.Context) {
	s.rootCtx, s.stop = context.WithCancel(ctx)
	go s.heartbeatLoop()
	s.Recover()
}

func (s *Scheduler) Stop() {
	if s.stop != nil {
		s.stop()
	}
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		log.Println("[scheduler] stop timeout, leaving running tasks for recovery")
	}
}

// Submit launches a goroutine per freshly created target.
func (s *Scheduler) Submit(targets []models.ExecutionTaskTarget) {
	for i := range targets {
		id := targets[i].ID
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.runTarget(id, false)
		}()
	}
}

// Recover resumes any target left non-terminal by a previous run.
func (s *Scheduler) Recover() {
	var targets []models.ExecutionTaskTarget
	if err := s.db.Where("status IN ?", []string{models.StatusPending, models.StatusDispatching, models.StatusRunning}).Find(&targets).Error; err != nil {
		log.Printf("[scheduler] recover query failed: %v", err)
		return
	}
	if len(targets) == 0 {
		return
	}
	log.Printf("[scheduler] recovering %d unfinished target(s)", len(targets))
	for i := range targets {
		id := targets[i].ID
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.runTarget(id, true)
		}()
	}
}

// CancelTarget asks a target's run goroutine to cancel. It returns true when
// the target was active in this process.
func (s *Scheduler) CancelTarget(targetID string) bool {
	s.mu.Lock()
	st, ok := s.running[targetID]
	if ok {
		st.userCancel = true
	}
	s.mu.Unlock()
	if ok {
		st.cancel()
	}
	return ok
}

// CancelTask cancels every active target of a task and returns how many were
// cancelled in-process.
func (s *Scheduler) CancelTask(taskID string) int {
	var targets []models.ExecutionTaskTarget
	s.db.Where("parent_task_id = ? AND status IN ?", taskID,
		[]string{models.StatusPending, models.StatusDispatching, models.StatusRunning}).Find(&targets)
	n := 0
	for i := range targets {
		if s.CancelTarget(targets[i].ID) {
			n++
		} else {
			// Not active (e.g. still waiting in a previous process): mark so a
			// later recovery does not execute it. Best effort.
			s.db.Model(&models.ExecutionTaskTarget{}).Where("id = ?", targets[i].ID).
				Updates(map[string]any{"status": models.StatusCancelled, "finished_at": time.Now().UTC()})
		}
	}
	if n > 0 || len(targets) > 0 {
		s.refreshParent(taskID)
	}
	return n
}

func (s *Scheduler) register(targetID string) (*runState, context.Context) {
	base, cancel := context.WithCancel(s.rootCtx)
	st := &runState{cancel: cancel}
	s.mu.Lock()
	s.running[targetID] = st
	s.mu.Unlock()
	return st, base
}

func (s *Scheduler) unregister(targetID string, st *runState) {
	s.mu.Lock()
	if cur, ok := s.running[targetID]; ok && cur == st {
		delete(s.running, targetID)
	}
	s.mu.Unlock()
	st.cancel()
}

func (s *Scheduler) runTarget(targetID string, resume bool) {
	var target models.ExecutionTaskTarget
	if err := s.db.First(&target, "id = ?", targetID).Error; err != nil {
		log.Printf("[scheduler] target %s not found: %v", targetID, err)
		return
	}
	// The in-process sequence counter resets on restart; continue above the
	// persisted value so reconnecting clients do not treat new frames as
	// duplicates. Do this before any early finalize path.
	s.hub.EnsureSeq(target.ID, target.Seq)

	var parent models.ExecutionTask
	if err := s.db.First(&parent, "id = ?", target.ParentTaskID).Error; err != nil {
		log.Printf("[scheduler] parent %s not found: %v", target.ParentTaskID, err)
		return
	}
	var server models.Server
	if err := s.db.First(&server, "id = ?", target.ServerID).Error; err != nil {
		s.finalize(&target, models.StatusFailed, nil, 0, "server not found")
		return
	}

	st, base := s.register(targetID)
	defer s.unregister(targetID, st)

	// CancelTask may have marked this target while its goroutine was starting.
	// Re-read the row now that we are registered: if it is already terminal,
	// do not execute it (this closes the cancel-right-after-submit race).
	var statuses []string
	s.db.Model(&models.ExecutionTaskTarget{}).Where("id = ?", target.ID).Pluck("status", &statuses)
	if len(statuses) > 0 && isTerminalStatus(statuses[0]) {
		target.Status = statuses[0]
		s.hub.Publish(ws.Frame{Type: "status", TaskID: target.ParentTaskID, TargetID: target.ID,
			ServerID: target.ServerID, ServerName: target.ServerName, Status: statuses[0],
			TS: time.Now().UTC().Format(time.RFC3339)})
		return
	}

	// Acquire a global execution slot, but stay cancellable while queued.
	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	case <-base.Done():
		if st.userCancel {
			s.finalize(&target, models.StatusCancelled, nil, 0, "cancelled before dispatch")
		}
		return
	}

	// Remaining budget for a recovered task.
	timeout := time.Duration(parent.TimeoutMS) * time.Millisecond
	if resume {
		elapsed := time.Since(parent.CreatedAt)
		timeout -= elapsed
		if timeout <= 0 {
			s.cancelAtAgent(target)
			s.finalize(&target, models.StatusTimeout, nil, elapsed.Milliseconds(), "timeout exceeded during recovery")
			return
		}
	}
	ctx, cancel := context.WithTimeout(base, timeout)
	defer cancel()

	if resume {
		tsk, err := s.agent.GetTask(ctx, target.AgentAddr, target.ID)
		if err != nil {
			if agentclient.IsNotFound(err) {
				s.finalize(&target, models.StatusUnknown, nil, 0, "agent lost task state; outcome unknown")
			} else {
				s.finalize(&target, models.StatusUnknown, nil, 0, "agent unreachable on recovery; outcome unknown")
			}
			return
		}
		if tsk.Finished {
			s.finalize(&target, mapAgentStatus(tsk.Status), tsk.ExitCode, tsk.DurationMS, tsk.Error)
			return
		}
		s.markRunning(&target)
	} else {
		s.setStatus(target.ID, models.StatusDispatching)
		req := agentclient.CreateTaskRequest{
			TaskID: target.ID, Command: parent.Command, WorkDir: parent.WorkDir, TimeoutMS: parent.TimeoutMS,
		}
		if _, err := s.agent.CreateTask(ctx, target.AgentAddr, req); err != nil {
			if ctx.Err() != nil {
				s.handleContextDone(ctx, st, &target, &parent)
				return
			}
			s.finalize(&target, models.StatusFailed, nil, 0, "dispatch failed: "+err.Error())
			return
		}
		s.markRunning(&target)
	}

	s.pollLoop(ctx, st, &target, &parent)
}

func (s *Scheduler) pollLoop(ctx context.Context, st *runState, target *models.ExecutionTaskTarget, parent *models.ExecutionTask) {
	ticker := time.NewTicker(time.Duration(s.cfg.Scheduler.PollIntervalMS) * time.Millisecond)
	defer ticker.Stop()
	failures := 0

	for {
		select {
		case <-ctx.Done():
			s.handleContextDone(ctx, st, target, parent)
			return
		case <-ticker.C:
		}

		resp, err := s.agent.GetLogs(ctx, target.AgentAddr, target.ID, target.StdoutOffset, target.StderrOffset)
		if err != nil {
			if ctx.Err() != nil {
				s.handleContextDone(ctx, st, target, parent)
				return
			}
			failures++
			if failures >= maxConsecutivePollFailures {
				s.finalize(target, models.StatusUnknown, nil, 0, "lost contact with agent; outcome unknown")
				return
			}
			continue
		}
		failures = 0
		s.processLogs(target, parent.ID, resp)

		if resp.Finished {
			msg := resp.Error
			if resp.Truncated && msg == "" {
				msg = "output truncated at agent limit"
			}
			s.finalize(target, mapAgentStatus(resp.Status), resp.ExitCode, resp.DurationMS, msg)
			return
		}
	}
}

func (s *Scheduler) processLogs(target *models.ExecutionTaskTarget, taskID string, resp *agentclient.LogsResponse) {
	now := time.Now().UTC().Format(time.RFC3339)
	if resp.Stdout != "" {
		if raw, err := base64.StdEncoding.DecodeString(resp.Stdout); err == nil && len(raw) > 0 {
			start := target.StdoutOffset
			f := ws.Frame{Type: "stdout", TaskID: taskID, TargetID: target.ID, ServerID: target.ServerID,
				ServerName: target.ServerName, Stream: "stdout", Data: resp.Stdout, TS: now}
			seq := s.hub.Publish(f)
			_ = s.store.Append(target.ID, "stdout", taskID, raw, start, seq, false)
			target.StdoutOffset += int64(len(raw))
			target.Seq = seq
		}
	}
	if resp.Stderr != "" {
		if raw, err := base64.StdEncoding.DecodeString(resp.Stderr); err == nil && len(raw) > 0 {
			start := target.StderrOffset
			f := ws.Frame{Type: "stderr", TaskID: taskID, TargetID: target.ID, ServerID: target.ServerID,
				ServerName: target.ServerName, Stream: "stderr", Data: resp.Stderr, TS: now}
			seq := s.hub.Publish(f)
			_ = s.store.Append(target.ID, "stderr", taskID, raw, start, seq, false)
			target.StderrOffset += int64(len(raw))
			target.Seq = seq
		}
	}
	s.db.Model(&models.ExecutionTaskTarget{}).Where("id = ?", target.ID).Updates(map[string]any{
		"stdout_offset": target.StdoutOffset,
		"stderr_offset": target.StderrOffset,
		"seq":           target.Seq,
	})
}

func (s *Scheduler) handleContextDone(ctx context.Context, st *runState, target *models.ExecutionTaskTarget, parent *models.ExecutionTask) {
	switch {
	case st.userCancel:
		s.cancelAtAgent(*target)
		s.finalize(target, models.StatusCancelled, nil, 0, "cancelled by operator")
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		s.cancelAtAgent(*target)
		s.finalize(target, models.StatusTimeout, nil, 0, "execution timed out")
	default:
		// Scheduler shutdown: leave the row as RUNNING so Recover() resumes it.
		log.Printf("[scheduler] target %s left running for recovery", target.ID)
	}
}

func (s *Scheduler) cancelAtAgent(target models.ExecutionTaskTarget) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.agent.CancelTask(ctx, target.AgentAddr, target.ID); err != nil {
		log.Printf("[scheduler] cancel %s: %v", target.ID, err)
	}
}

func (s *Scheduler) markRunning(target *models.ExecutionTaskTarget) {
	now := time.Now().UTC()
	if target.StartedAt == nil {
		target.StartedAt = &now
	}
	seq := s.hub.Publish(ws.Frame{Type: "status", TaskID: target.ParentTaskID, TargetID: target.ID,
		ServerID: target.ServerID, ServerName: target.ServerName, Status: models.StatusRunning, TS: now.Format(time.RFC3339)})
	target.Seq = seq
	s.db.Model(&models.ExecutionTaskTarget{}).Where("id = ?", target.ID).Updates(map[string]any{
		"status": models.StatusRunning, "started_at": target.StartedAt,
		"agent_addr": target.AgentAddr, "log_dir": s.store.TargetDir(target.ParentTaskID, target.ID),
		"seq": seq,
	})
	s.refreshParent(target.ParentTaskID)
}

func (s *Scheduler) setStatus(targetID, status string) {
	s.db.Model(&models.ExecutionTaskTarget{}).Where("id = ?", targetID).Update("status", status)
}

func (s *Scheduler) finalize(target *models.ExecutionTaskTarget, status string, exitCode *int, durationMS int64, msg string) {
	now := time.Now().UTC()
	if durationMS <= 0 && target.StartedAt != nil {
		durationMS = now.Sub(*target.StartedAt).Milliseconds()
	}
	seq := s.hub.Publish(ws.Frame{Type: "status", TaskID: target.ParentTaskID, TargetID: target.ID,
		ServerID: target.ServerID, ServerName: target.ServerName, Status: status,
		ExitCode: exitCode, DurationMS: durationMS, Error: msg, TS: now.Format(time.RFC3339)})
	target.Seq = seq
	vals := map[string]any{
		"status": status, "duration_ms": durationMS, "error": msg, "finished_at": now, "seq": seq,
	}
	if exitCode != nil {
		vals["exit_code"] = *exitCode
	} else {
		vals["exit_code"] = nil
	}
	if err := s.db.Model(&models.ExecutionTaskTarget{}).Where("id = ?", target.ID).Updates(vals).Error; err != nil {
		log.Printf("[scheduler] finalize %s: %v", target.ID, err)
	}
	_ = s.store.Flush(target.ID, seq)
	s.refreshParent(target.ParentTaskID)
}

// refreshParent recomputes parent aggregate counters and status.
func (s *Scheduler) refreshParent(taskID string) {
	s.parentMu.Lock()
	defer s.parentMu.Unlock()

	var targets []models.ExecutionTaskTarget
	if err := s.db.Where("parent_task_id = ?", taskID).Find(&targets).Error; err != nil {
		return
	}
	statuses := make([]string, 0, len(targets))
	for _, t := range targets {
		statuses = append(statuses, t.Status)
	}
	agg := AggregateStatuses(statuses)
	vals := map[string]any{
		"status": agg.Status, "total": agg.Total, "succeeded": agg.Succeeded,
		"failed": agg.Failed, "cancelled": agg.Cancelled,
	}
	if agg.Finished {
		vals["finished_at"] = time.Now().UTC()
	}
	s.db.Model(&models.ExecutionTask{}).Where("id = ?", taskID).Updates(vals)
	s.hub.Publish(ws.Frame{Type: "task_status", TaskID: taskID, Status: agg.Status, TS: time.Now().UTC().Format(time.RFC3339)})
}

func mapAgentStatus(st string) string {
	switch st {
	case models.StatusSuccess, models.StatusFailed, models.StatusTimeout, models.StatusCancelled:
		return st
	case models.StatusRunning:
		return models.StatusRunning
	default:
		return models.StatusUnknown
	}
}

// ---- health / heartbeat ----

func (s *Scheduler) heartbeatLoop() {
	s.pollAllServers()
	ticker := time.NewTicker(time.Duration(s.cfg.Scheduler.HeartbeatIntervalSec) * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.rootCtx.Done():
			return
		case <-ticker.C:
			s.pollAllServers()
		}
	}
}

func (s *Scheduler) pollAllServers() {
	var servers []models.Server
	if err := s.db.Find(&servers).Error; err != nil {
		return
	}
	for i := range servers {
		s.CheckServer(s.rootCtx, &servers[i])
	}
}

// CheckServer probes one agent and updates the server/agent records. Callers
// use it both for the periodic heartbeat and for a manual health check.
func (s *Scheduler) CheckServer(ctx context.Context, server *models.Server) (*agentclient.InfoResponse, error) {
	addr := serverAddr(server)
	hctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	info, err := s.agent.Info(hctx, addr)
	now := time.Now().UTC()
	if err != nil {
		s.db.Model(&models.Server{}).Where("id = ?", server.ID).
			Updates(map[string]any{"status": models.StatusOffline})
		return nil, err
	}
	s.db.Model(&models.Server{}).Where("id = ?", server.ID).Updates(map[string]any{
		"status":            models.StatusOnline,
		"agent_version":     info.Version,
		"hostname":          info.Hostname,
		"os":                info.OS,
		"arch":              info.Arch,
		"last_heartbeat_at": now,
	})
	s.upsertAgent(server.ID, info, now)
	return info, nil
}

func (s *Scheduler) upsertAgent(serverID int64, info *agentclient.InfoResponse, now time.Time) {
	var ag models.Agent
	err := s.db.Where("server_id = ?", serverID).First(&ag).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		ag = models.Agent{
			ServerID: serverID, Version: info.Version, OS: info.OS, Arch: info.Arch,
			Hostname: info.Hostname, LastSeenAt: &now,
		}
		s.db.Create(&ag)
		return
	}
	if err == nil {
		s.db.Model(&models.Agent{}).Where("server_id = ?", serverID).Updates(map[string]any{
			"version": info.Version, "os": info.OS, "arch": info.Arch,
			"hostname": info.Hostname, "last_seen_at": now,
		})
	}
}

func serverAddr(s *models.Server) string {
	return fmt.Sprintf("%s:%d", s.IP, s.AgentPort)
}

// ServerAddr is exported for API handlers that need the agent address.
func ServerAddr(s *models.Server) string { return serverAddr(s) }
