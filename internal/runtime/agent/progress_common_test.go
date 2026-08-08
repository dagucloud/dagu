// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"bytes"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
)

func TestProgressWriter_PrintHeader(t *testing.T) {
	var buf bytes.Buffer
	writer := progressWriter{out: &buf}

	writer.printHeader(&ir.DAG{Name: "etl"}, "run-1", "REGION=eu")
	assert.Equal(t, "▶ etl (run-1) [REGION=eu]\n", buf.String())

	buf.Reset()
	writer.printHeader(&ir.DAG{Name: "etl"}, "", "")
	assert.Equal(t, "▶ etl (...)\n", buf.String())

	buf.Reset()
	writer.printHeader(nil, "run-2", "")
	assert.Equal(t, "▶ unknown (run-2)\n", buf.String())
}

func TestStatusIcon(t *testing.T) {
	assert.Equal(t, "✓", statusIcon(ir.Succeeded))
	assert.Equal(t, "✓", statusIcon(ir.PartiallySucceeded))
	assert.Equal(t, "✗", statusIcon(ir.Failed))
	assert.Equal(t, "✗", statusIcon(ir.Aborted))
	assert.Equal(t, "⏸", statusIcon(ir.Waiting))
	assert.Equal(t, "●", statusIcon(ir.Rejected))
	assert.Equal(t, "●", statusIcon(ir.Queued))
	assert.Equal(t, "●", statusIcon(ir.Running))
}

func TestProgressWriter_Gray(t *testing.T) {
	writer := progressWriter{}
	assert.Equal(t, "plain", writer.gray("plain"))

	writer.tty = true
	assert.Equal(t, "\033[38;5;245mcolored\033[0m", writer.gray("colored"))
}
