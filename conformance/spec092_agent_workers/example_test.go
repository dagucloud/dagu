// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec092_agent_workers_test

import (
	"os"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 092, "Examples > Complete DAG": dagu validate accepts the example DAG.
func TestCompleteExampleValidates(t *testing.T) {
	t.Parallel()

	harness.NewRunner(t).Run("validate", "dev-with-agent.yaml").ExpectExitCode(0)
}

// The text of the complete DAG in the spec is the text of the fixture.
func TestCompleteExampleMatchesSpec(t *testing.T) {
	t.Parallel()

	doc, err := os.ReadFile("../../specs/092-agent-workers.md")
	require.NoError(t, err)
	_, afterHeading, found := strings.Cut(string(doc), "### Complete DAG\n")
	require.True(t, found, "the spec has no section Complete DAG")
	_, afterFence, found := strings.Cut(afterHeading, "```yaml\n")
	require.True(t, found, "the section has no YAML block")
	block, _, found := strings.Cut(afterFence, "\n```\n")
	require.True(t, found, "the YAML block has no end")

	fixture, err := os.ReadFile("testdata/dev-with-agent.yaml")
	require.NoError(t, err)
	assert.Equal(t, strings.TrimRight(string(fixture), "\n"), block)
}
