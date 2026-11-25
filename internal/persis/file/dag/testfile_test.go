// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dag

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Only a file named like a test file that reads as a DAG is reported, once,
// including a DAG whose first document is followed by a sub-DAG.
func TestWarnTestNamedDAGs(t *testing.T) {
	t.Parallel()

	const hint = "reads as a DAG, with steps and no cases, but Dagu treats it as a test file and does not run it as a DAG; rename it, for example to "
	tests := []struct {
		name string
		file string
		yaml string
		// rename is the name the warning suggests, or empty for no warning.
		rename string
	}{
		{
			name:   "DAG",
			file:   "smoke.test.yaml",
			yaml:   "steps:\n  - run: echo smoke\n",
			rename: "smoke-test.yaml",
		},
		{
			name:   "DAGWithSubDAG",
			file:   "nightly.test.yml",
			yaml:   "steps:\n  - call: report\n---\nname: report\nsteps:\n  - run: echo report\n",
			rename: "nightly-test.yml",
		},
		{
			name:   "MergedSteps",
			file:   "merged.test.yaml",
			yaml:   "name: merged\n<<:\n  steps:\n    - run: echo merged\n",
			rename: "merged-test.yaml",
		},
		{
			name: "TestFile",
			file: "release.test.yaml",
			yaml: "cases:\n  - name: passes\n    expect:\n      status: succeeded\n",
		},
		{
			name: "NotYAML",
			file: "broken.test.yaml",
			yaml: "steps: [echo\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			path := filepath.Join(dir, tt.file)
			require.NoError(t, os.WriteFile(path, []byte(tt.yaml), 0o600))

			var want []string
			if tt.rename != "" {
				want = []string{path + " " + hint + tt.rename}
			}
			assert.Equal(t, want, warnings(t, func(ctx context.Context) {
				WarnTestNamedDAGs(ctx, dir, DiscoveryOptions{})
			}))
		})
	}
}

// warnings returns the message of each warning that run logs.
func warnings(t *testing.T, run func(ctx context.Context)) []string {
	t.Helper()

	var logs bytes.Buffer
	run(logger.WithLogger(context.Background(), logger.NewLogger(logger.WithWriter(&logs), logger.WithQuiet())))
	var messages []string
	for line := range strings.Lines(logs.String()) {
		var record struct {
			Level string `json:"level"`
			Msg   string `json:"msg"`
		}
		require.NoError(t, json.Unmarshal([]byte(line), &record))
		if record.Level == "WARN" {
			messages = append(messages, record.Msg)
		}
	}
	return messages
}
