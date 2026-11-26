// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package inventory_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dagucloud/dagu/v2/internal/inventory"
)

// A worker is a node only when its ID names one; a worker without
// worker.id reports hostname@pid.
func TestIsNodeName(t *testing.T) {
	t.Parallel()

	assert.True(t, inventory.IsNodeName("tokyo-gw"))
	assert.True(t, inventory.IsNodeName("Worker_1.prod"))
	assert.False(t, inventory.IsNodeName("build-host@4242"))
	assert.False(t, inventory.IsNodeName(""))
	assert.False(t, inventory.IsNodeName("-gw"))
}

func TestSplitWorkers(t *testing.T) {
	t.Parallel()

	ssh := []inventory.ResolvedNode{{Name: "tokyo-web01", InventoryName: "Tokyo"}}
	workers := []inventory.WorkerNode{{Name: "osaka-gw"}, {Name: "Tokyo-Web01"}, {Name: "tokyo-gw"}}

	kept, conflicts := inventory.SplitWorkers(workers, ssh)

	assert.Equal(t, []inventory.WorkerNode{{Name: "osaka-gw"}, {Name: "tokyo-gw"}}, kept)
	assert.Equal(t, []inventory.WorkerConflict{{Worker: workers[1], Node: ssh[0]}}, conflicts)
}
