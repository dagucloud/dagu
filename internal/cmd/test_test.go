// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A DAG named like a test file is an invalid test file, and the report
// suggests a name under which it runs as a DAG.
func TestTestReadsAsDAG(t *testing.T) {
	t.Parallel()

	home := writeInitDAG(t, "smoke.test.yaml", "steps:\n  - run: ./smoke.sh\n")
	testPath := filepath.Join(home, "dags", "smoke.test.yaml")

	stdout, err := runTestCommand(t, home, testPath)
	var exit *exitStatusError
	require.ErrorAs(t, err, &exit)
	assert.Equal(t, testExitInvalid, exit.code)
	assert.Contains(t, stdout, "INVALID "+testReportPath(testPath)+"\n"+
		"  reads as a DAG, with steps and no cases, but Dagu treats it as a test file and does not run it as a DAG; rename it, for example to smoke-test.yaml\n")
}
