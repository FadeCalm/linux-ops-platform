package api

import (
	"log"
	"net/http"
	"time"

	"linux-ops-platform/agent/internal/update"
)

func (s *Server) updateCheck(w http.ResponseWriter, r *http.Request) {
	res, err := s.updater.Check(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) updateApply(w http.ResponseWriter, r *http.Request) {
	if s.engine.RunningCount() > 0 {
		writeError(w, http.StatusConflict,
			"refusing to update while tasks are running; retry after they finish")
		return
	}
	res, err := s.updater.Apply(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)

	if res.Updated && res.Restarting {
		exe := res.Exe
		go func() {
			// Give the HTTP response (and the backend's audit write) time to
			// complete before the process image is replaced.
			time.Sleep(700 * time.Millisecond)
			log.Printf("[agent] restarting after update to %s", res.To)
			if err := update.Restart(exe); err != nil {
				log.Printf("[agent] restart failed: %v", err)
			}
		}()
	}
}
