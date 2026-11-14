// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd_test

import (
	"context"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"

	api "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/require"
)

func TestRemoteRunQuotedParams(t *testing.T) {
	const fileName = "remote-input"
	const dagName = "remote-declared"
	server := test.SetupServer(t)
	server.CreateDAGFile(t, server.Config.Paths.DAGsDir, fileName, []byte(fmt.Sprintf(`name: %s
params: default
steps:
  - name: print
    run: echo ok
`, dagName)))
	baseURL := fmt.Sprintf("http://%s:%d/api/v1", server.Config.Server.Host, server.Config.Server.Port)
	test.RunBuiltCLI(t, server.Helper, nil, "context", "add", "review",
		"--server="+baseURL, "--api-key=dagu_reviewtest123456789")

	for _, command := range []string{"start", "enqueue"} {
		t.Run(command, func(t *testing.T) {
			for _, tt := range []struct {
				name       string
				flags      []string
				dash       []string
				stdin      string
				wantParams string
				wantErr    string
			}{
				{name: "Stdin", flags: []string{"--params-stdin"}, stdin: `"hello world"`, wantParams: "1=hello world"},
				{name: "EmptyValue", flags: []string{"--params-stdin"}, stdin: `""`, wantParams: "1="},
				{name: "SpacedValue", flags: []string{"--params-stdin"}, stdin: `" hello world "`, wantParams: "1= hello world "},
				{name: "ExcessValues", flags: []string{"--params-stdin"}, stdin: `"hello world" "second value"`, wantErr: "too many positional params: expected at most 1, got 2"},
				{name: "Flag", flags: []string{`--params="\"hello world\""`}, wantParams: "1=hello world"},
				{name: "Dash", dash: []string{"--", "hello world"}, wantParams: "1=hello world"},
			} {
				t.Run(tt.name, func(t *testing.T) {
					runID := command + "-" + strings.ToLower(tt.name)
					args := []string{command, "--context=review", "--run-id=" + runID}
					args = append(args, tt.flags...)
					args = append(args, fileName)
					args = append(args, tt.dash...)
					ctx, cancel := context.WithTimeout(server.Context, 30*time.Second)
					defer cancel()
					cli := exec.CommandContext(ctx, server.Config.Paths.Executable, test.WithConfigFlag(args, server.Config)...) //nolint:gosec // Executes the binary built by the test harness.
					cli.Env = server.ChildEnv
					cli.Stdin = strings.NewReader(tt.stdin)
					output, err := cli.CombinedOutput()
					if tt.wantErr != "" {
						require.Error(t, err)
						require.Contains(t, string(output), tt.wantErr)
						return
					}
					require.NoError(t, err, "output: %s", output)
					require.Contains(t, string(output), runID)

					wantStatus := ir.Queued
					if command == "start" {
						wantStatus = ir.Succeeded
					}
					var status *ir.DAGRunStatus
					require.Eventually(t, func() bool {
						status, err = server.DAGRunMgr.GetSavedStatus(server.Context, ir.NewDAGRunRef(dagName, runID))
						return err == nil && status.Status == wantStatus
					}, 10*time.Second, 50*time.Millisecond)
					require.Equal(t, tt.wantParams, status.Params)
				})
			}
		})
	}

	for _, endpoint := range []string{"start", "enqueue"} {
		t.Run("API/"+endpoint, func(t *testing.T) {
			params := `"hello world"`
			runID := "api-" + endpoint
			path := "/api/v1/dags/" + fileName + "/" + endpoint
			if endpoint == "start" {
				server.Client().Post(path, api.ExecuteDAGJSONRequestBody{
					Params: &params, DagRunId: &runID,
				}).ExpectStatus(http.StatusOK).Send(t)
			} else {
				server.Client().Post(path, api.EnqueueDAGDAGRunJSONRequestBody{
					Params: &params, DagRunId: &runID,
				}).ExpectStatus(http.StatusOK).Send(t)
			}
			status, err := server.DAGRunMgr.GetSavedStatus(server.Context, ir.NewDAGRunRef(dagName, runID))
			require.NoError(t, err)
			require.Equal(t, "1=hello world", status.Params)
		})
	}
}
