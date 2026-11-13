// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package schedulerstate

import "context"

// RestartBackoff counts the runs of a service DAG in a row that failed soon
// after they started. Each one lengthens the wait before the next run.
type RestartBackoff struct {
	// RunID is the latest run counted, so each run is counted once.
	RunID    string `json:"runId"`
	Failures int    `json:"failures"`
}

// RestartBackoffStore shares the restart backoff of the active scheduler with
// other processes. The backoff is kept by DAG name, and a DAG whose runs do not
// fail has no entry.
type RestartBackoffStore interface {
	// Load returns the backoff that the scheduler last saved.
	Load(ctx context.Context) (map[string]RestartBackoff, error)
	// Save replaces the backoff.
	Save(ctx context.Context, backoff map[string]RestartBackoff) error
}
