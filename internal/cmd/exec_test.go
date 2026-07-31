// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmd"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/require"
)

func TestExecCommand(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		expectErr   string
		expectedOut []string
		env         map[string]string
	}{
		{
			name:        "ExecBasicCommand",
			args:        []string{"exec", "--", "sh", "-c", "echo exec-basic"},
			expectedOut: []string{"Executing inline dag-run"},
		},
		{
			name:      "ExecMissingCommand",
			args:      []string{"exec"},
			expectErr: "command is required",
		},
		{
			name:      "ExecMissingBase",
			args:      []string{"exec", "--base", "missing-base.yaml", "--", "sh", "-c", "echo hi"},
			expectErr: "base DAG file",
		},
		{
			name:      "ExecMissingDotenv",
			args:      []string{"exec", "--dotenv", "missing.env", "--", "sh", "-c", "echo hi"},
			expectErr: "dotenv file",
		},
		{
			name: "ExecWorkerLabelWithoutQueue",
			args: []string{"exec", "--worker-label", "role=batch", "--", "sh", "-c", "echo hi"},
			env: map[string]string{
				"DAGU_QUEUE_ENABLED": "false",
			},
			expectErr: "worker selector requires queues",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Not using t.Parallel because SetupCommand manipulates process-wide env vars.
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			th := test.SetupCommand(t)
			run := cmd.Exec()

			if tc.expectErr != "" {
				err := th.RunCommandWithError(t, run, test.CmdTest{
					Name: tc.name,
					Args: tc.args,
				})
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.expectErr)
				return
			}

			th.RunCommand(t, run, test.CmdTest{
				Name:        tc.name,
				Args:        tc.args,
				ExpectedOut: tc.expectedOut,
			})
		})
	}
}
