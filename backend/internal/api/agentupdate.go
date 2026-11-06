package api

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"linux-ops-platform/backend/internal/models"

	"github.com/gin-gonic/gin"
)

type updateItem struct {
	ServerID  int64  `json:"server_id"`
	Name      string `json:"name"`
	OK        bool   `json:"ok"`
	Current   string `json:"current"`
	Latest    string `json:"latest"`
	Available bool   `json:"available"`
	Updated   bool   `json:"updated"`
	Source    string `json:"source,omitempty"`
	Verified  bool   `json:"verified"`
	Error     string `json:"error,omitempty"`
}

func (h *Handlers) AgentUpdateCheck(c *gin.Context) {
	srv, addr, okID := h.targetServer(c)
	if !okID {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	res, err := h.agent.CheckUpdate(ctx, addr)
	if err != nil {
		agentError(c, err)
		return
	}
	ok(c, gin.H{
		"server_id": srv.ID, "name": srv.Name,
		"current": res.Current, "latest": res.Latest,
		"available": res.Available, "notes": res.Notes,
		"source": res.Source, "verified": res.Verified,
	})
}

func (h *Handlers) AgentUpdate(c *gin.Context) {
	srv, addr, okID := h.targetServer(c)
	if !okID {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Minute)
	defer cancel()
	res, err := h.agent.ApplyUpdate(ctx, addr)
	if err != nil {
		agentError(c, err)
		return
	}
	if res.Updated {
		h.audit("agent.update", "server", strconv.FormatInt(srv.ID, 10),
			res.From+" -> "+res.To)
	}
	ok(c, res)
}

type batchUpdateRequest struct {
	ServerIDs []int64 `json:"server_ids"`
}

func (h *Handlers) batchServers(c *gin.Context) ([]models.Server, bool) {
	var req batchUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid body: "+err.Error())
		return nil, false
	}
	if len(req.ServerIDs) == 0 {
		fail(c, http.StatusBadRequest, "server_ids is required")
		return nil, false
	}
	if len(req.ServerIDs) > 200 {
		fail(c, http.StatusBadRequest, "too many servers (max 200)")
		return nil, false
	}
	var servers []models.Server
	if err := h.db.Where("id IN ?", req.ServerIDs).Find(&servers).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return nil, false
	}
	if len(servers) == 0 {
		fail(c, http.StatusNotFound, "no matching servers")
		return nil, false
	}
	return servers, true
}

// runBatch executes fn for every server with bounded concurrency and returns
// results in the same order as servers.
func runBatch(servers []models.Server, fn func(models.Server) updateItem) []updateItem {
	results := make([]updateItem, len(servers))
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for i, srv := range servers {
		wg.Add(1)
		go func(i int, srv models.Server) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i] = fn(srv)
		}(i, srv)
	}
	wg.Wait()
	return results
}

func (h *Handlers) BatchAgentUpdateCheck(c *gin.Context) {
	servers, okIDs := h.batchServers(c)
	if !okIDs {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 90*time.Second)
	defer cancel()
	results := runBatch(servers, func(srv models.Server) updateItem {
		item := updateItem{ServerID: srv.ID, Name: srv.Name}
		addr := fmt.Sprintf("%s:%d", srv.IP, srv.AgentPort)
		res, err := h.agent.CheckUpdate(ctx, addr)
		if err != nil {
			item.Error = err.Error()
			return item
		}
		item.OK = true
		item.Current = res.Current
		item.Latest = res.Latest
		item.Available = res.Available
		item.Source = res.Source
		item.Verified = res.Verified
		return item
	})
	ok(c, gin.H{"items": results})
}

func (h *Handlers) BatchAgentUpdate(c *gin.Context) {
	servers, okIDs := h.batchServers(c)
	if !okIDs {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Minute)
	defer cancel()
	results := runBatch(servers, func(srv models.Server) updateItem {
		item := updateItem{ServerID: srv.ID, Name: srv.Name}
		addr := fmt.Sprintf("%s:%d", srv.IP, srv.AgentPort)
		res, err := h.agent.ApplyUpdate(ctx, addr)
		if err != nil {
			item.Error = err.Error()
			return item
		}
		item.OK = true
		item.Current = res.From
		item.Latest = res.To
		item.Updated = res.Updated
		item.Source = res.Source
		item.Verified = res.Verified
		if res.Updated {
			h.audit("agent.update", "server", strconv.FormatInt(srv.ID, 10),
				res.From+" -> "+res.To)
		}
		return item
	})
	ok(c, gin.H{"items": results})
}
