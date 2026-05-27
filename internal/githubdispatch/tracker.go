// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package githubdispatch

import "time"

// TrackedJob is the durable scheduler state for a cloud GitHub dispatch job.
type TrackedJob struct {
	JobID     string    `json:"job_id"`
	DAGName   string    `json:"dag_name"`
	DAGRunID  string    `json:"dag_run_id"`
	Phase     string    `json:"phase"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Tracker persists in-flight GitHub dispatch jobs until they are reported.
type Tracker interface {
	Upsert(TrackedJob) error
	Delete(string) error
	List() ([]TrackedJob, error)
}
