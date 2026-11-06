// Package models contains the persistence models for the management backend.
// Only the tables required by milestone M1-M3 are declared here; see
// database/schema.sql and docs/architecture.md for the full ER model.
package models

import "time"

// Task / target status values. Kept as constants so that the scheduler,
// API layer and frontend share one vocabulary.
const (
	StatusPending      = "PENDING"
	StatusDispatching  = "DISPATCHING"
	StatusRunning      = "RUNNING"
	StatusSuccess      = "SUCCESS"
	StatusFailed       = "FAILED"
	StatusTimeout      = "TIMEOUT"
	StatusCancelled    = "CANCELLED"
	StatusUnknown      = "UNKNOWN"
	StatusOnline       = "online"
	StatusOffline      = "offline"
	StatusUnknownAgent = "unknown"
)

// ServerGroup groups managed hosts.
type ServerGroup struct {
	ID          int64     `gorm:"primaryKey" json:"id"`
	Name        string    `gorm:"size:128;not null;uniqueIndex" json:"name"`
	Description string    `gorm:"size:512" json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ServerTag is a free-form label attached to servers.
type ServerTag struct {
	ID        int64     `gorm:"primaryKey" json:"id"`
	Name      string    `gorm:"size:64;not null;uniqueIndex" json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Server is the canonical managed host record.
type Server struct {
	ID            int64      `gorm:"primaryKey" json:"id"`
	Name          string     `gorm:"size:128;not null" json:"name"`
	IP            string     `gorm:"size:128;not null" json:"ip"`
	AgentPort     int        `gorm:"not null;default:19090" json:"agent_port"`
	Hostname      string     `gorm:"size:255" json:"hostname"`
	OS            string     `gorm:"size:64" json:"os"`
	Arch          string     `gorm:"size:64" json:"arch"`
	GroupID       *int64     `gorm:"index" json:"group_id"`
	Status        string     `gorm:"size:32;not null;default:'unknown';index" json:"status"`
	AgentVersion  string     `gorm:"size:64" json:"agent_version"`
	LastHeartbeat *time.Time `gorm:"column:last_heartbeat_at" json:"last_heartbeat_at"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`

	Tags []ServerTag `gorm:"many2many:servers_tags;" json:"tags,omitempty"`
}

// Agent holds agent-reported runtime information (1:1 with Server).
type Agent struct {
	ID         int64      `gorm:"primaryKey" json:"id"`
	ServerID   int64      `gorm:"not null;uniqueIndex" json:"server_id"`
	Version    string     `gorm:"size:64" json:"version"`
	OS         string     `gorm:"size:64" json:"os"`
	Arch       string     `gorm:"size:64" json:"arch"`
	Hostname   string     `gorm:"size:255" json:"hostname"`
	ExtraJSON  string     `gorm:"type:text" json:"extra_json"`
	LastSeenAt *time.Time `json:"last_seen_at"`
}

// ExecutionTask is the parent (batch) task.
type ExecutionTask struct {
	ID             string     `gorm:"type:char(36);primaryKey" json:"id"`
	Command        string     `gorm:"type:mediumtext;not null" json:"command"`
	WorkDir        string     `gorm:"size:512" json:"work_dir"`
	TimeoutMS      int64      `gorm:"not null" json:"timeout_ms"`
	Status         string     `gorm:"size:32;not null;index" json:"status"`
	Total          int        `json:"total"`
	Succeeded      int        `json:"succeeded"`
	Failed         int        `json:"failed"`
	Cancelled      int        `json:"cancelled"`
	IdempotencyKey *string    `gorm:"size:128;uniqueIndex" json:"idempotency_key,omitempty"`
	CreatedBy      string     `gorm:"size:64;not null;default:'anonymous'" json:"created_by"`
	CreatedAt      time.Time  `gorm:"index" json:"created_at"`
	FinishedAt     *time.Time `json:"finished_at"`
}

// ExecutionTaskTarget is the per-server child task.
type ExecutionTaskTarget struct {
	ID           string     `gorm:"type:char(36);primaryKey" json:"id"`
	ParentTaskID string     `gorm:"type:char(36);not null;index;uniqueIndex:idx_parent_server" json:"parent_task_id"`
	ServerID     int64      `gorm:"not null;index;uniqueIndex:idx_parent_server" json:"server_id"`
	ServerName   string     `gorm:"size:128" json:"server_name"`
	AgentAddr    string     `gorm:"size:255" json:"agent_addr"`
	Status       string     `gorm:"size:32;not null;index" json:"status"`
	ExitCode     *int       `json:"exit_code"`
	DurationMS   int64      `json:"duration_ms"`
	Error        string     `gorm:"type:text" json:"error"`
	LogDir       string     `gorm:"size:512" json:"log_dir"`
	StdoutOffset int64      `json:"stdout_offset"`
	StderrOffset int64      `json:"stderr_offset"`
	Seq          int64      `json:"seq"`
	StartedAt    *time.Time `json:"started_at"`
	FinishedAt   *time.Time `json:"finished_at"`
	CreatedAt    time.Time  `json:"created_at"`
}

// ExecutionLogIndex records where a chunk of output lives on disk.
type ExecutionLogIndex struct {
	ID         int64     `gorm:"primaryKey" json:"id"`
	TargetID   string    `gorm:"type:char(36);not null;index" json:"target_id"`
	Stream     string    `gorm:"size:16;not null" json:"stream"` // stdout|stderr
	FilePath   string    `gorm:"size:512;not null" json:"file_path"`
	ByteOffset int64     `json:"byte_offset"`
	Length     int64     `json:"length"`
	Seq        int64     `json:"seq"`
	CreatedAt  time.Time `json:"created_at"`
}

// AuditEvent records operations. In phase 1 there is no user auth, so the
// operator is always "anonymous"; it must never be forged.
type AuditEvent struct {
	ID         int64     `gorm:"primaryKey" json:"id"`
	Action     string    `gorm:"size:64;not null;index" json:"action"`
	Resource   string    `gorm:"size:64" json:"resource"`
	ResourceID string    `gorm:"size:128;index" json:"resource_id"`
	Operator   string    `gorm:"size:64;not null;default:'anonymous'" json:"operator"`
	DetailJSON string    `gorm:"type:text" json:"detail_json"`
	CreatedAt  time.Time `gorm:"index" json:"created_at"`
}

// AllModels is the list passed to GORM AutoMigrate.
func AllModels() []interface{} {
	return []interface{}{
		&ServerGroup{},
		&ServerTag{},
		&Server{},
		&Agent{},
		&ExecutionTask{},
		&ExecutionTaskTarget{},
		&ExecutionLogIndex{},
		&AuditEvent{},
	}
}
