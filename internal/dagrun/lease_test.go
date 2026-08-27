// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun_test

import (
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
)

func TestIsLeaseActiveAllowsCoordinatorRestart(t *testing.T) {
	status := &ir.DAGRunStatus{LeaseAt: time.Now().Add(-time.Minute).UnixMilli()}

	assert.True(t, dagrun.IsLeaseActive(status, 0))
}
