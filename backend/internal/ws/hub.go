// Package ws implements a small in-process pub/sub hub that fans task output
// frames out to WebSocket subscribers and keeps a bounded replay buffer so a
// reconnecting browser can resume from its last seen sequence number.
package ws

import (
	"sync"
)

// Frame is the WebSocket JSON message exchanged with the frontend.
type Frame struct {
	Type       string           `json:"type"` // snapshot|stdout|stderr|status|task_status|error
	TaskID     string           `json:"task_id,omitempty"`
	TargetID   string           `json:"target_id,omitempty"`
	ServerID   int64            `json:"server_id,omitempty"`
	ServerName string           `json:"server_name,omitempty"`
	Seq        int64            `json:"seq,omitempty"`
	TS         string           `json:"ts,omitempty"`
	Stream     string           `json:"stream,omitempty"`
	Data       string           `json:"data,omitempty"` // base64-encoded stdout/stderr bytes
	Status     string           `json:"status,omitempty"`
	ExitCode   *int             `json:"exit_code,omitempty"`
	DurationMS int64            `json:"duration_ms,omitempty"`
	Error      string           `json:"error,omitempty"`
	Targets    []TargetSnapshot `json:"targets,omitempty"`
}

type TargetSnapshot struct {
	TargetID   string `json:"target_id"`
	ServerID   int64  `json:"server_id"`
	ServerName string `json:"server_name"`
	Status     string `json:"status"`
	Seq        int64  `json:"seq"`
}

// Client is a single WebSocket subscriber.
type Client struct {
	Hub    *Hub
	TaskID string
	Send   chan Frame
	done   chan struct{}
	once   sync.Once
}

func (c *Client) Close() {
	c.once.Do(func() { close(c.done) })
}

func (c *Client) Done() <-chan struct{} { return c.done }

type ring struct {
	frames []Frame
	next   int
	full   bool
}

func newRing(n int) *ring { return &ring{frames: make([]Frame, n)} }

func (r *ring) add(f Frame) {
	r.frames[r.next] = f
	r.next = (r.next + 1) % len(r.frames)
	if r.next == 0 {
		r.full = true
	}
}

// since returns frames with Seq > after, in order.
func (r *ring) since(after int64) []Frame {
	var out []Frame
	n := len(r.frames)
	start := 0
	count := r.next
	if r.full {
		start = r.next
		count = n
	}
	for i := 0; i < count; i++ {
		f := r.frames[(start+i)%n]
		if f.Seq > after {
			out = append(out, f)
		}
	}
	return out
}

// Hub manages subscriptions and replay buffers.
type Hub struct {
	mu          sync.RWMutex
	clients     map[string]map[*Client]struct{} // taskID -> clients
	rings       map[string]*ring                // targetID -> ring
	seqs        map[string]int64                // targetID -> last seq
	taskTargets map[string]map[string]struct{}  // taskID -> targetIDs
	maxReplay   int
	maxConns    int
	conns       int
}

func NewHub(maxReplay, maxConns int) *Hub {
	return &Hub{
		clients:     map[string]map[*Client]struct{}{},
		rings:       map[string]*ring{},
		seqs:        map[string]int64{},
		taskTargets: map[string]map[string]struct{}{},
		maxReplay:   maxReplay,
		maxConns:    maxConns,
	}
}

// Register adds a client. It returns false when the connection limit is hit.
func (h *Hub) Register(taskID string) (*Client, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.maxConns > 0 && h.conns >= h.maxConns {
		return nil, false
	}
	c := &Client{Hub: h, TaskID: taskID, Send: make(chan Frame, 512), done: make(chan struct{})}
	if h.clients[taskID] == nil {
		h.clients[taskID] = map[*Client]struct{}{}
	}
	h.clients[taskID][c] = struct{}{}
	h.conns++
	return c, true
}

func (h *Hub) Unregister(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if set, ok := h.clients[c.TaskID]; ok {
		if _, exists := set[c]; exists {
			delete(set, c)
			h.conns--
		}
		if len(set) == 0 {
			delete(h.clients, c.TaskID)
		}
	}
	c.Close()
}

// Publish assigns the next sequence number for the target, stores it in the
// replay ring and fans it out to subscribers of the task. It returns the
// assigned sequence number (0 for task-level frames without a target).
func (h *Hub) Publish(f Frame) int64 {
	if f.TargetID != "" {
		h.mu.Lock()
		h.seqs[f.TargetID]++
		f.Seq = h.seqs[f.TargetID]
		if h.rings[f.TargetID] == nil {
			h.rings[f.TargetID] = newRing(h.maxReplay)
		}
		h.rings[f.TargetID].add(f)
		if f.TaskID != "" {
			if h.taskTargets[f.TaskID] == nil {
				h.taskTargets[f.TaskID] = map[string]struct{}{}
			}
			h.taskTargets[f.TaskID][f.TargetID] = struct{}{}
		}
		h.mu.Unlock()
	}
	h.mu.RLock()
	set := h.clients[f.TaskID]
	var slow []*Client
	for c := range set {
		select {
		case c.Send <- f:
		default:
			slow = append(slow, c)
		}
	}
	h.mu.RUnlock()
	for _, c := range slow {
		// Slow consumer: close it; it will reconnect and replay.
		h.Unregister(c)
	}
	return f.Seq
}

// Replay returns buffered frames with Seq > resume[targetID] for all targets
// known to belong to taskID.
func (h *Hub) Replay(taskID string, resume map[string]int64) []Frame {
	h.mu.RLock()
	defer h.mu.RUnlock()
	targets := h.taskTargets[taskID]
	var out []Frame
	for tid := range targets {
		r := h.rings[tid]
		if r == nil {
			continue
		}
		after := int64(0)
		if resume != nil {
			after = resume[tid]
		}
		out = append(out, r.since(after)...)
	}
	return out
}

// EnsureSeq raises the sequence counter for targetID to at least seq. It is
// used after a backend restart so new frames continue above the persisted
// sequence, ensuring clients that kept their last seq do not drop them.
func (h *Hub) EnsureSeq(targetID string, seq int64) {
	if targetID == "" || seq <= 0 {
		return
	}
	h.mu.Lock()
	if h.seqs[targetID] < seq {
		h.seqs[targetID] = seq
	}
	h.mu.Unlock()
}

// LastSeq returns the last sequence number for a target.
func (h *Hub) LastSeq(targetID string) int64 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.seqs[targetID]
}

// ConnCount returns the current WebSocket connection count.
func (h *Hub) ConnCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.conns
}
