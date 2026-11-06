package scheduler

import (
	"testing"

	"linux-ops-platform/backend/internal/models"
)

func TestAggregateStatuses(t *testing.T) {
	cases := []struct {
		name       string
		in         []string
		wantStatus string
		wantFinish bool
		wantSucc   int
		wantFail   int
		wantCancel int
		wantRun    int
	}{
		{"all success", []string{models.StatusSuccess, models.StatusSuccess}, models.StatusSuccess, true, 2, 0, 0, 0},
		{"one running", []string{models.StatusSuccess, models.StatusRunning}, models.StatusRunning, false, 1, 0, 0, 1},
		{"pending counts as running", []string{models.StatusPending}, models.StatusRunning, false, 0, 0, 0, 1},
		{"timeout is failure", []string{models.StatusTimeout, models.StatusSuccess}, models.StatusFailed, true, 1, 1, 0, 0},
		{"unknown is failure", []string{models.StatusUnknown}, models.StatusFailed, true, 0, 1, 0, 0},
		{"all cancelled", []string{models.StatusCancelled, models.StatusCancelled}, models.StatusCancelled, true, 0, 0, 2, 0},
		{"mixed cancel/success is failed", []string{models.StatusCancelled, models.StatusSuccess}, models.StatusFailed, true, 1, 0, 1, 0},
		{"empty batch succeeds", []string{}, models.StatusSuccess, true, 0, 0, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := AggregateStatuses(tc.in)
			if got.Status != tc.wantStatus {
				t.Errorf("status = %q, want %q", got.Status, tc.wantStatus)
			}
			if got.Finished != tc.wantFinish {
				t.Errorf("finished = %v, want %v", got.Finished, tc.wantFinish)
			}
			if got.Succeeded != tc.wantSucc || got.Failed != tc.wantFail ||
				got.Cancelled != tc.wantCancel || got.Running != tc.wantRun {
				t.Errorf("counts = %+v", got)
			}
		})
	}
}
