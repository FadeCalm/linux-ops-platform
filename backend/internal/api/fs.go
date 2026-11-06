package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"linux-ops-platform/backend/internal/agentclient"
	"linux-ops-platform/backend/internal/models"

	"github.com/gin-gonic/gin"
)

// targetServer resolves the :id path parameter to a server and its agent address.
func (h *Handlers) targetServer(c *gin.Context) (models.Server, string, bool) {
	id, okID := parseID(c)
	if !okID {
		return models.Server{}, "", false
	}
	var srv models.Server
	if err := h.db.First(&srv, id).Error; err != nil {
		fail(c, http.StatusNotFound, "server not found")
		return models.Server{}, "", false
	}
	return srv, fmt.Sprintf("%s:%d", srv.IP, srv.AgentPort), true
}

// agentError forwards an agent error status/message to the browser.
func agentError(c *gin.Context, err error) {
	var he *agentclient.HTTPError
	if errors.As(err, &he) {
		msg := strings.TrimSpace(he.Body)
		var body struct {
			Error string `json:"error"`
		}
		if json.Unmarshal([]byte(he.Body), &body) == nil && body.Error != "" {
			msg = body.Error
		}
		fail(c, he.Status, msg)
		return
	}
	fail(c, http.StatusBadGateway, err.Error())
}

func (h *Handlers) FSList(c *gin.Context) {
	_, addr, okID := h.targetServer(c)
	if !okID {
		return
	}
	path := c.Query("path")
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "500"))
	res, err := h.agent.FSList(c.Request.Context(), addr, path, offset, limit)
	if err != nil {
		agentError(c, err)
		return
	}
	ok(c, res)
}

func (h *Handlers) FSStat(c *gin.Context) {
	_, addr, okID := h.targetServer(c)
	if !okID {
		return
	}
	res, err := h.agent.FSStat(c.Request.Context(), addr, c.Query("path"))
	if err != nil {
		agentError(c, err)
		return
	}
	ok(c, res)
}

func (h *Handlers) FSRead(c *gin.Context) {
	_, addr, okID := h.targetServer(c)
	if !okID {
		return
	}
	offset, _ := strconv.ParseInt(c.DefaultQuery("offset", "0"), 10, 64)
	limit, _ := strconv.ParseInt(c.DefaultQuery("limit", "0"), 10, 64)
	res, err := h.agent.FSRead(c.Request.Context(), addr, c.Query("path"), offset, limit)
	if err != nil {
		agentError(c, err)
		return
	}
	ok(c, res)
}

type fsWriteBody struct {
	Path      string `json:"path"`
	Data      string `json:"data"`
	BaseMtime string `json:"base_mtime"`
	Mode      string `json:"mode"`
}

func (h *Handlers) FSWrite(c *gin.Context) {
	srv, addr, okID := h.targetServer(c)
	if !okID {
		return
	}
	var body fsWriteBody
	if err := c.ShouldBindJSON(&body); err != nil {
		fail(c, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	if strings.TrimSpace(body.Path) == "" {
		fail(c, http.StatusBadRequest, "path is required")
		return
	}
	err := h.agent.FSWrite(c.Request.Context(), addr, agentclient.FSWriteRequest{
		Path: body.Path, Data: body.Data, BaseMtime: body.BaseMtime, Mode: body.Mode,
	})
	if err != nil {
		agentError(c, err)
		return
	}
	h.audit("fs.write", "server", strconv.FormatInt(srv.ID, 10), body.Path)
	ok(c, gin.H{"ok": true})
}

type fsOpBody struct {
	Op      string `json:"op"`
	Path    string `json:"path"`
	NewPath string `json:"new_path"`
	Mode    string `json:"mode"`
}

func (h *Handlers) FSOp(c *gin.Context) {
	srv, addr, okID := h.targetServer(c)
	if !okID {
		return
	}
	var body fsOpBody
	if err := c.ShouldBindJSON(&body); err != nil {
		fail(c, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	if strings.TrimSpace(body.Path) == "" {
		fail(c, http.StatusBadRequest, "path is required")
		return
	}
	err := h.agent.FSOp(c.Request.Context(), addr, agentclient.FSOpRequest{
		Op: body.Op, Path: body.Path, NewPath: body.NewPath, Mode: body.Mode,
	})
	if err != nil {
		agentError(c, err)
		return
	}
	detail := body.Path
	if body.NewPath != "" {
		detail += " -> " + body.NewPath
	}
	h.audit("fs."+strings.ToLower(body.Op), "server", strconv.FormatInt(srv.ID, 10), detail)
	ok(c, gin.H{"ok": true})
}

// FSDownload streams a file from the agent to the browser.
func (h *Handlers) FSDownload(c *gin.Context) {
	srv, addr, okID := h.targetServer(c)
	if !okID {
		return
	}
	path := c.Query("path")
	resp, err := h.agent.FSOpenDownload(c.Request.Context(), addr, path, c.GetHeader("Range"))
	if err != nil {
		agentError(c, err)
		return
	}
	defer resp.Body.Close()

	for _, name := range []string{
		"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges",
		"Last-Modified", "Content-Disposition", "ETag",
	} {
		if v := resp.Header.Get(name); v != "" {
			c.Header(name, v)
		}
	}
	c.Status(resp.StatusCode)
	if _, err := io.Copy(c.Writer, resp.Body); err != nil {
		return
	}
	h.audit("fs.download", "server", strconv.FormatInt(srv.ID, 10), path)
}

// FSUpload proxies one chunk of a chunked upload to the agent.
func (h *Handlers) FSUpload(c *gin.Context) {
	srv, addr, okID := h.targetServer(c)
	if !okID {
		return
	}
	q := url.Values{}
	for _, k := range []string{"path", "upload_id", "offset", "total", "mode"} {
		if v := c.Query(k); v != "" {
			q.Set(k, v)
		}
	}
	if q.Get("path") == "" || q.Get("upload_id") == "" {
		fail(c, http.StatusBadRequest, "path and upload_id are required")
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32<<20+1)
	res, err := h.agent.FSUpload(c.Request.Context(), addr, q, c.Request.Body, c.Request.ContentLength)
	if err != nil {
		agentError(c, err)
		return
	}
	if res.Done {
		h.audit("fs.upload", "server", strconv.FormatInt(srv.ID, 10),
			fmt.Sprintf("%s (%d bytes)", res.Path, res.Total))
	}
	ok(c, res)
}
