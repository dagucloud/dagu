// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/nodeconn"
)

// A test cancelled before its nodes finish fails, naming them, and prints
// no totals.
func TestConnectionTestCancelled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out bytes.Buffer
	nodes := []inventory.ResolvedNode{{Name: "web01"}, {Name: "web02"}}
	session := connectionTest{ctx: &Context{Context: ctx}, tester: localTester{service: nodeconn.New(nil, nil), nodes: nodes}, nodes: nodes, out: &out}

	err := session.run(nodes)

	require.ErrorIs(t, err, context.Canceled)
	assert.ErrorContains(t, err, "web01, web02")
	assert.NotContains(t, out.String(), "passed")
}
