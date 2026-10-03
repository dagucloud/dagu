// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package foreach

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
)

func TestForeachStatusDetailsIdentifyItems(t *testing.T) {
	t.Parallel()

	items := []expandedItem{
		{index: 0, key: "0", value: "customer-a"},
		{index: 1, key: "1", value: map[string]any{"customer": "customer-b"}},
	}
	results := []itemResult{
		{Index: 0, Key: "0", Status: ir.NodeFailed.String()},
		{Index: 1, Key: "1", Status: ir.NodeSucceeded.String()},
	}

	assert.Equal(t, []ir.NodeStatusDetail{
		{Label: "customer-a", Status: ir.NodeFailed},
		{Label: `{"customer":"customer-b"}`, Status: ir.NodeSucceeded},
	}, foreachStatusDetails(items, results, false))
}

func TestForeachStatusDetailsUseConfiguredKeys(t *testing.T) {
	t.Parallel()

	items := []expandedItem{{index: 0, key: "customer-a", value: map[string]any{"id": 42}}}
	results := []itemResult{{Index: 0, Key: "customer-a", Status: ir.NodeFailed.String()}}

	assert.Equal(t, []ir.NodeStatusDetail{
		{Label: "customer-a", Status: ir.NodeFailed},
	}, foreachStatusDetails(items, results, true))
}

func TestSummarizeLeavesUnstartedItemsOutOfTheFailedCount(t *testing.T) {
	t.Parallel()

	results := []itemResult{
		{Index: 0, Status: ir.NodeSucceeded.String()},
		{Index: 1, Status: ir.NodeFailed.String(), Error: "exit status 1"},
		{Index: 2, Status: ir.NodeNotStarted.String()},
	}
	outcome := summarize(results, nil)
	assert.Equal(t, 3, outcome.total)
	assert.Equal(t, 1, outcome.failed)
	assert.NoError(t, outcome.err)

	allFailed := summarize(results[1:2], nil)
	assert.EqualError(t, allFailed.err, "all 1 item bodies failed; first error: exit status 1")
}
