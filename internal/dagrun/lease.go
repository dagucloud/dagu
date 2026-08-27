// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun

import (
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

// DefaultStaleLeaseThreshold is the default interval after which an
// unobserved distributed run is considered stale.
const DefaultStaleLeaseThreshold = 90 * time.Second

// IsLeaseActive reports whether the coordinator has observed the run within
// the stale threshold. A run without a lease timestamp is stale.
func IsLeaseActive(status *ir.DAGRunStatus, staleThreshold time.Duration) bool {
	if status == nil || status.LeaseAt == 0 {
		return false
	}
	if staleThreshold <= 0 {
		staleThreshold = DefaultStaleLeaseThreshold
	}
	return time.Since(time.UnixMilli(status.LeaseAt)) < staleThreshold
}
