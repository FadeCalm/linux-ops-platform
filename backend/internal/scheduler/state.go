package scheduler

import "linux-ops-platform/backend/internal/models"

// Aggregate summarizes the statuses of a parent task's targets.
type Aggregate struct {
	Total     int
	Succeeded int
	Failed    int
	Cancelled int
	Running   int
	Status    string
	Finished  bool
}

// isTerminalStatus reports whether a target status will not change again.
func isTerminalStatus(status string) bool {
	switch status {
	case models.StatusSuccess, models.StatusFailed, models.StatusTimeout,
		models.StatusCancelled, models.StatusUnknown:
		return true
	}
	return false
}

// AggregateStatuses is the pure state-machine transition for a batch task.
// A batch is SUCCESS only when every target succeeded, CANCELLED only when
// every target was cancelled, otherwise FAILED once nothing is still running.
func AggregateStatuses(list []string) Aggregate {
	a := Aggregate{Total: len(list)}
	for _, st := range list {
		switch st {
		case models.StatusSuccess:
			a.Succeeded++
		case models.StatusFailed, models.StatusTimeout, models.StatusUnknown:
			a.Failed++
		case models.StatusCancelled:
			a.Cancelled++
		default:
			a.Running++
		}
	}
	switch {
	case a.Running > 0:
		a.Status = models.StatusRunning
		a.Finished = false
	case a.Succeeded == a.Total:
		a.Status = models.StatusSuccess
		a.Finished = true
	case a.Cancelled == a.Total:
		a.Status = models.StatusCancelled
		a.Finished = true
	default:
		a.Status = models.StatusFailed
		a.Finished = true
	}
	return a
}
