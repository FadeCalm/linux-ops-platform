// Package agentclient is the HTTP client used by the management backend to
// talk to agents. The backend actively connects to each agent (pull model).
package agentclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// HTTPError is returned when an agent responds with a non-2xx status.
type HTTPError struct {
	Status int
	Op     string
	Body   string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("agent %s: http %d: %s", e.Op, e.Status, e.Body)
}

// IsNotFound reports whether err is a 404 from the agent (unknown task id).
func IsNotFound(err error) bool {
	var he *HTTPError
	return errors.As(err, &he) && he.Status == http.StatusNotFound
}

type Client struct {
	HTTP    *http.Client
	Timeout time.Duration

	// stream is used for file transfers: no overall timeout, but the
	// response header must arrive promptly.
	stream *http.Client
}

func New(timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 60 * time.Second
	return &Client{
		HTTP:    &http.Client{Timeout: timeout},
		Timeout: timeout,
		stream:  &http.Client{Transport: transport},
	}
}

type HealthResponse struct {
	Status string `json:"status"`
	Uptime int64  `json:"uptime_sec"`
}

type InfoResponse struct {
	Hostname    string `json:"hostname"`
	OS          string `json:"os"`
	Arch        string `json:"arch"`
	Version     string `json:"version"`
	UptimeSec   int64  `json:"uptime_sec"`
	RunningTask int    `json:"running_tasks"`
	MaxConcur   int    `json:"max_concurrent"`
}

type CreateTaskRequest struct {
	TaskID    string `json:"task_id"`
	Command   string `json:"command"`
	WorkDir   string `json:"work_dir,omitempty"`
	TimeoutMS int64  `json:"timeout_ms,omitempty"`
}

type TaskStatus struct {
	TaskID     string `json:"task_id"`
	Status     string `json:"status"`
	ExitCode   *int   `json:"exit_code"`
	DurationMS int64  `json:"duration_ms"`
	Error      string `json:"error"`
	Finished   bool   `json:"finished"`
}

type LogsResponse struct {
	TaskID       string `json:"task_id"`
	Status       string `json:"status"`
	ExitCode     *int   `json:"exit_code"`
	DurationMS   int64  `json:"duration_ms"`
	StdoutOffset int64  `json:"stdout_offset"`
	StderrOffset int64  `json:"stderr_offset"`
	Stdout       string `json:"stdout"` // base64
	Stderr       string `json:"stderr"` // base64
	Truncated    bool   `json:"truncated"`
	Finished     bool   `json:"finished"`
	Error        string `json:"error"`
}

func baseURL(addr string) string {
	if len(addr) > 7 && (addr[:7] == "http://" || addr[:8] == "https://") {
		return addr
	}
	return "http://" + addr
}

func (c *Client) do(ctx context.Context, method, addr, path string, body interface{}, out interface{}) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, baseURL(addr)+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &HTTPError{Status: resp.StatusCode, Op: method + " " + path, Body: string(data)}
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("decode agent response: %w", err)
		}
	}
	return nil
}

func (c *Client) Health(ctx context.Context, addr string) (*HealthResponse, error) {
	var r HealthResponse
	if err := c.do(ctx, http.MethodGet, addr, "/api/v1/health", nil, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func (c *Client) Info(ctx context.Context, addr string) (*InfoResponse, error) {
	var r InfoResponse
	if err := c.do(ctx, http.MethodGet, addr, "/api/v1/info", nil, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func (c *Client) CreateTask(ctx context.Context, addr string, req CreateTaskRequest) (*TaskStatus, error) {
	var r TaskStatus
	if err := c.do(ctx, http.MethodPost, addr, "/api/v1/tasks", req, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func (c *Client) GetTask(ctx context.Context, addr, id string) (*TaskStatus, error) {
	var r TaskStatus
	if err := c.do(ctx, http.MethodGet, addr, "/api/v1/tasks/"+id, nil, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func (c *Client) GetLogs(ctx context.Context, addr, id string, stdoutOffset, stderrOffset int64) (*LogsResponse, error) {
	path := fmt.Sprintf("/api/v1/tasks/%s/logs?stdout_offset=%d&stderr_offset=%d", id, stdoutOffset, stderrOffset)
	var r LogsResponse
	if err := c.do(ctx, http.MethodGet, addr, path, nil, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func (c *Client) CancelTask(ctx context.Context, addr, id string) error {
	return c.do(ctx, http.MethodPost, addr, "/api/v1/tasks/"+id+"/cancel", nil, nil)
}

// ---- file system ----

type FSEntry struct {
	Name       string    `json:"name"`
	Path       string    `json:"path"`
	Type       string    `json:"type"`
	Size       int64     `json:"size"`
	Mode       string    `json:"mode"`
	ModTime    time.Time `json:"mtime"`
	UID        uint32    `json:"uid"`
	GID        uint32    `json:"gid"`
	LinkTarget string    `json:"link_target,omitempty"`
	Editable   bool      `json:"editable"`
}

type FSListResponse struct {
	Path    string    `json:"path"`
	Parent  string    `json:"parent"`
	Entries []FSEntry `json:"entries"`
	Total   int       `json:"total"`
	Offset  int       `json:"offset"`
	Limit   int       `json:"limit"`
}

type FSReadResponse struct {
	Path    string    `json:"path"`
	Size    int64     `json:"size"`
	Offset  int64     `json:"offset"`
	ModTime time.Time `json:"mod_time"`
	Binary  bool      `json:"binary"`
	EOF     bool      `json:"eof"`
	Data    string    `json:"data"` // base64
}

type FSStatResponse struct {
	Entry  FSEntry `json:"entry"`
	Binary bool    `json:"binary"`
}

type FSWriteRequest struct {
	Path      string `json:"path"`
	Data      string `json:"data"`
	BaseMtime string `json:"base_mtime,omitempty"`
	Mode      string `json:"mode,omitempty"`
}

type FSOpRequest struct {
	Op      string `json:"op"`
	Path    string `json:"path"`
	NewPath string `json:"new_path,omitempty"`
	Mode    string `json:"mode,omitempty"`
}

type FSUploadResponse struct {
	Path     string `json:"path"`
	Received int64  `json:"received"`
	Total    int64  `json:"total"`
	Done     bool   `json:"done"`
}

func (c *Client) FSList(ctx context.Context, addr, path string, offset, limit int) (*FSListResponse, error) {
	q := url.Values{}
	q.Set("path", path)
	q.Set("offset", fmt.Sprint(offset))
	q.Set("limit", fmt.Sprint(limit))
	var r FSListResponse
	if err := c.do(ctx, http.MethodGet, addr, "/api/v1/fs/list?"+q.Encode(), nil, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func (c *Client) FSStat(ctx context.Context, addr, path string) (*FSStatResponse, error) {
	q := url.Values{}
	q.Set("path", path)
	var r FSStatResponse
	if err := c.do(ctx, http.MethodGet, addr, "/api/v1/fs/stat?"+q.Encode(), nil, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func (c *Client) FSRead(ctx context.Context, addr, path string, offset, limit int64) (*FSReadResponse, error) {
	q := url.Values{}
	q.Set("path", path)
	q.Set("offset", fmt.Sprint(offset))
	q.Set("limit", fmt.Sprint(limit))
	var r FSReadResponse
	if err := c.do(ctx, http.MethodGet, addr, "/api/v1/fs/read?"+q.Encode(), nil, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func (c *Client) FSWrite(ctx context.Context, addr string, req FSWriteRequest) error {
	return c.do(ctx, http.MethodPut, addr, "/api/v1/fs/write", req, nil)
}

func (c *Client) FSOp(ctx context.Context, addr string, req FSOpRequest) error {
	return c.do(ctx, http.MethodPost, addr, "/api/v1/fs/op", req, nil)
}

// FSOpenDownload starts a streaming download from the agent. rangeHeader is
// forwarded verbatim so resume (HTTP Range) works. The caller must close the
// returned body. Non-2xx/206/416 responses are converted to *HTTPError.
func (c *Client) FSOpenDownload(ctx context.Context, addr, path, rangeHeader string) (*http.Response, error) {
	q := url.Values{}
	q.Set("path", path)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		baseURL(addr)+"/api/v1/fs/download?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	if rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
	}
	resp, err := c.stream.Do(req)
	if err != nil {
		return nil, err
	}
	switch resp.StatusCode {
	case http.StatusOK, http.StatusPartialContent, http.StatusRequestedRangeNotSatisfiable:
		return resp, nil
	}
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	return nil, &HTTPError{
		Status: resp.StatusCode, Op: "GET /api/v1/fs/download", Body: string(data),
	}
}

// ---- self-update ----

type UpdateCheckResponse struct {
	Current   string `json:"current"`
	Latest    string `json:"latest"`
	Available bool   `json:"available"`
	Notes     string `json:"notes"`
	File      string `json:"file"`
	SHA256    string `json:"sha256"`
}

type UpdateApplyResponse struct {
	Updated    bool   `json:"updated"`
	From       string `json:"from"`
	To         string `json:"to"`
	Restarting bool   `json:"restarting"`
	Reason     string `json:"reason"`
}

func (c *Client) CheckUpdate(ctx context.Context, addr string) (*UpdateCheckResponse, error) {
	var r UpdateCheckResponse
	if err := c.do(ctx, http.MethodGet, addr, "/api/v1/update/check", nil, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// ApplyUpdate asks the agent to pull and install the newest package. The agent
// re-executes itself shortly after replying, so this may block for a while.
func (c *Client) ApplyUpdate(ctx context.Context, addr string) (*UpdateApplyResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL(addr)+"/api/v1/update", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.stream.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &HTTPError{Status: resp.StatusCode, Op: "POST /api/v1/update", Body: string(data)}
	}
	var out UpdateApplyResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("decode agent response: %w", err)
	}
	return &out, nil
}

// FSUpload forwards one chunk of an upload to the agent.
func (c *Client) FSUpload(ctx context.Context, addr string, q url.Values, body io.Reader, contentLength int64) (*FSUploadResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut,
		baseURL(addr)+"/api/v1/fs/upload?"+q.Encode(), body)
	if err != nil {
		return nil, err
	}
	req.ContentLength = contentLength
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := c.stream.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &HTTPError{Status: resp.StatusCode, Op: "PUT /api/v1/fs/upload", Body: string(data)}
	}
	var out FSUploadResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("decode agent response: %w", err)
	}
	return &out, nil
}
