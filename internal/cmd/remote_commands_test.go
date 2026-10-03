// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	api "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToExecStatus_MapsRemoteFieldsExplicitly(t *testing.T) {
	t.Parallel()

	detail := &api.DAGRunDetails{
		Name:           "example",
		DagRunId:       "run-1",
		RootDAGRunName: "example",
		RootDAGRunId:   "run-1",
		Status:         api.Status(ir.Running),
		StartedAt:      "2026-04-02T00:00:00Z",
		FinishedAt:     "",
		Log:            "/tmp/example.log",
		Params:         new("P1=foo"),
		WorkerId:       new("worker-a"),
		Labels:         &[]string{"env=prod"},
		Nodes: []api.Node{
			{
				Step: api.Step{
					Name: "step-1",
					Commands: &[]api.CommandEntry{
						{Command: "echo", Args: &[]string{"hello"}},
					},
				},
				Status:    api.NodeStatus(ir.NodeRunning),
				StartedAt: "2026-04-02T00:00:01Z",
				Stdout:    "/tmp/stdout",
				Stderr:    "/tmp/stderr",
			},
		},
	}

	status, err := toExecStatus(detail)
	require.NoError(t, err)
	assert.Equal(t, "example", status.Name)
	assert.Equal(t, "run-1", status.DAGRunID)
	assert.Equal(t, ir.Running, status.Status)
	assert.Equal(t, "/tmp/example.log", status.Log)
	require.Len(t, status.Nodes, 1)
	assert.Equal(t, "step-1", status.Nodes[0].Step.Name)
	require.Len(t, status.Nodes[0].Step.Commands, 1)
	assert.Equal(t, "echo", status.Nodes[0].Step.Commands[0].Command)
	assert.Equal(t, []string{"hello"}, status.Nodes[0].Step.Commands[0].Args)
}

func TestIsLikelyLocalDAGArg(t *testing.T) {
	t.Parallel()

	// A forward slash always reads as a local path. A bare backslash can be a
	// valid character in a remote file ID, so only Windows drive-letter and
	// UNC prefixes read as local.
	tests := []struct {
		name string
		arg  string
		want bool
	}{
		{name: "yaml suffix", arg: "workflow.yaml", want: true},
		{name: "yml suffix", arg: "workflow.yml", want: true},
		{name: "posix separator", arg: "dir/workflow", want: true},
		{name: "windows drive path", arg: `C:\dags\workflow`, want: true},
		{name: "windows unc path", arg: `\\server\dags\workflow`, want: true},
		{name: "windows forward slashes", arg: "C:/dags/workflow", want: true},
		{name: "remote dag name", arg: "workflow", want: false},
		{name: "remote dag fileName", arg: "etl-dag", want: false},
		{name: "remote id with backslash", arg: `dir\workflow`, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isLikelyLocalDAGArg(tt.arg))
		})
	}
}

func TestRemoteStatusValueRejectsNone(t *testing.T) {
	t.Parallel()

	_, err := remoteStatusValue("none")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not supported")
}

func TestBuildRemoteHistoryQueryRejectsMalformedLimit(t *testing.T) {
	t.Parallel()

	command := &cobra.Command{Use: "history"}
	initFlags(command, historyFlags...)
	require.NoError(t, command.Flags().Set("limit", "10foo"))

	ctx := &Context{Command: command}
	_, _, err := buildRemoteHistoryQuery(ctx, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be an integer")

	require.NoError(t, command.Flags().Set("limit", "0"))
	_, _, err = buildRemoteHistoryQuery(ctx, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "greater than 0")
}

func TestBuildRemoteHistoryQueryParsesMultipleStatuses(t *testing.T) {
	t.Parallel()

	command := &cobra.Command{Use: "history"}
	initFlags(command, historyFlags...)
	require.NoError(t, command.Flags().Set("status", "running,queued"))

	ctx := &Context{Command: command}
	query, limit, err := buildRemoteHistoryQuery(ctx, nil)
	require.NoError(t, err)

	assert.Equal(t, 100, limit)
	assert.Equal(t, []int{int(ir.Running), int(ir.Queued)}, query.Statuses)
}

func TestRemoteClientListDAGRunsUsesRepeatedStatusParams(t *testing.T) {
	t.Parallel()

	statusValues := make(chan []string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		statusValues <- append([]string(nil), r.URL.Query()["status"]...)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"dagRuns":[]}`))
	}))
	defer server.Close()

	client := &remoteClient{
		baseURL: server.URL,
		client:  server.Client(),
	}

	_, err := client.listDAGRuns(context.Background(), remoteHistoryQuery{
		Statuses: []int{int(ir.Running), int(ir.Queued)},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"1", "5"}, <-statusValues)
}

func TestRemoteClientRetryDAGRunSendsChildTarget(t *testing.T) {
	t.Parallel()

	type request struct {
		path string
		body api.RetryDAGRunJSONBody
	}
	requests := make(chan request, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body api.RetryDAGRunJSONBody
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		requests <- request{path: r.URL.Path, body: body}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := &remoteClient{
		baseURL: server.URL,
		client:  server.Client(),
	}
	stepName := "target"
	childRunID := "child-run"
	require.NoError(t, client.retryDAGRun(
		context.Background(),
		"root",
		"root-run",
		api.RetryDAGRunJSONBody{
			DagRunId:    "root-run",
			StepName:    &stepName,
			SubDAGRunId: &childRunID,
		},
	))

	got := <-requests
	assert.Equal(t, "/dag-runs/root/root-run/retry", got.path)
	assert.Equal(t, "root-run", got.body.DagRunId)
	require.NotNil(t, got.body.StepName)
	require.NotNil(t, got.body.SubDAGRunId)
	assert.Equal(t, "target", *got.body.StepName)
	assert.Equal(t, "child-run", *got.body.SubDAGRunId)
}

func TestRemoteDAGLookup(t *testing.T) {
	t.Parallel()

	details := &api.DAGDetails{Name: "declared"}
	listed := api.DAGFile{FileName: "actual", Dag: api.DAG{Name: "declared"}}
	for _, tt := range []struct {
		name     string
		status   int
		details  *api.DAGDetails
		listed   []api.DAGFile
		fileName string
		wantErr  string
	}{
		{name: "Details", status: http.StatusOK, details: details, fileName: "lookup"},
		{name: "NameFallback", status: http.StatusNotFound, listed: []api.DAGFile{listed}, fileName: "actual"},
		{name: "MissingDetails", status: http.StatusOK, wantErr: "missing DAG identity"},
		{name: "EmptyName", status: http.StatusOK, details: &api.DAGDetails{}, wantErr: "missing DAG identity"},
		{name: "NotFound", status: http.StatusNotFound, wantErr: "was not found"},
		{name: "Ambiguous", status: http.StatusNotFound, listed: []api.DAGFile{listed, listed}, wantErr: "ambiguous"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/dags" {
					assert.Equal(t, "lookup", r.URL.Query().Get("name"))
					assert.NoError(t, json.NewEncoder(w).Encode(struct {
						Dags []api.DAGFile `json:"dags"`
					}{Dags: tt.listed}))
					return
				}
				assert.Equal(t, "/dags/lookup", r.URL.Path)
				w.WriteHeader(tt.status)
				assert.NoError(t, json.NewEncoder(w).Encode(api.GetDAGDetails200JSONResponse{Dag: tt.details}))
			}))
			defer server.Close()
			client := &remoteClient{baseURL: server.URL, client: server.Client()}
			dag, err := client.resolveDAG(context.Background(), "lookup")
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.fileName, dag.FileName)
			assert.Equal(t, "declared", dag.Dag.Name)
		})
	}
}

func TestRemoteStartSendsSteps(t *testing.T) {
	t.Parallel()

	bodies := make(chan api.ExecuteDAGJSONBody, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"dag":{"name":"etl"}}`))
			return
		}
		var body api.ExecuteDAGJSONBody
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		bodies <- body
		_, _ = w.Write([]byte(`{"dagRunId":"run-1"}`))
	}))
	defer server.Close()

	command := &cobra.Command{Use: "start"}
	initFlags(command, startFlags...)
	require.NoError(t, command.Flags().Set("only", "load"))
	require.NoError(t, command.Flags().Set("outputs-from", "source"))
	require.NoError(t, command.Flags().Set("output", "extract.rows=3"))
	ctx := &Context{
		Context: context.Background(),
		Command: command,
		Remote:  &remoteClient{baseURL: server.URL, client: server.Client()},
	}

	require.NoError(t, remoteRunStart(ctx, []string{"etl"}))

	body := <-bodies
	require.NotNil(t, body.Steps)
	assert.Equal(t, []string{"load"}, *body.Steps)
	require.NotNil(t, body.OutputsFromRunId)
	assert.Equal(t, "source", *body.OutputsFromRunId)
	require.NotNil(t, body.Outputs)
	assert.Equal(t, map[string]map[string]string{"extract": {"rows": "3"}}, *body.Outputs)
}

// These cases replace process stdin and must remain sequential.
func TestRemoteRunParams(t *testing.T) {
	commands := []struct {
		name  string
		flags []commandLineFlag
		run   func(*Context, []string) error
	}{
		{name: "start", flags: startFlags, run: remoteRunStart},
		{name: "enqueue", flags: enqueueFlags, run: remoteRunEnqueue},
	}
	oversizedInput := strings.Repeat("x", maxStdinParamsSize+1)
	tests := []struct {
		name       string
		args       []string
		stdin      string
		wantParams *string
		wantValues []string
		wantErr    string
		wantUnread bool
		startOnly  bool
		closed     bool
	}{
		{
			name:    "ClosedStdinRejected",
			args:    []string{"--params-stdin", "etl"},
			closed:  true,
			wantErr: "params from stdin",
		},
		{
			name:       "FlagSkipsClosedStdin",
			args:       []string{"--params-stdin", "--params=P1=flag", "etl"},
			closed:     true,
			wantParams: new("P1=flag"),
		},
		{
			name:   "EmptyFlagSkipsClosedStdin",
			args:   []string{"--params-stdin", "--params=", "etl"},
			closed: true,
		},
		{
			name:       "DashSkipsClosedStdin",
			args:       []string{"--params-stdin", "etl", "--", "P1=dash"},
			closed:     true,
			wantParams: new(`P1="dash"`),
		},
		{
			name:   "EmptyDashSkipsClosedStdin",
			args:   []string{"--params-stdin", "etl", "--"},
			closed: true,
		},
		{
			name:       "InheritedStdinIgnored",
			args:       []string{"etl"},
			stdin:      "P1=stdin",
			wantUnread: true,
		},
		{
			name:       "DisabledStdinIgnored",
			args:       []string{"--params-stdin=false", "etl"},
			stdin:      "P1=stdin",
			wantUnread: true,
		},
		{
			name:       "InheritedOversizedStdinIgnored",
			args:       []string{"etl"},
			stdin:      oversizedInput,
			wantUnread: true,
		},
		{
			name:       "NamedStdin",
			args:       []string{"--params-stdin", "etl"},
			stdin:      "P1=foo P2=bar",
			wantParams: new("P1=foo P2=bar"),
		},
		{
			name:      "FromRunIDRejectsStdin",
			args:      []string{"--params-stdin", "--from-run-id=source", "etl"},
			stdin:     "P1=stdin",
			wantErr:   "parameters cannot be provided when using --from-run-id",
			startOnly: true,
		},
		{
			name:       "FlagBeatsStdin",
			args:       []string{"--params-stdin", "--params=P1=flag", "etl"},
			stdin:      "P1=stdin",
			wantParams: new("P1=flag"),
		},
		{
			name:  "EmptyFlagBeatsStdin",
			args:  []string{"--params-stdin", "--params=", "etl"},
			stdin: "P1=stdin",
		},
		{
			name:       "DashBeatsFlagAndStdin",
			args:       []string{"--params-stdin", "--params=P1=flag", "etl", "--", "P1=dash"},
			stdin:      "P1=stdin",
			wantParams: new(`P1="dash"`),
		},
		{
			name:  "EmptyDashBeatsFlagAndStdin",
			args:  []string{"--params-stdin", "--params=P1=flag", "etl", "--"},
			stdin: "P1=stdin",
		},
		{
			name:  "EmptyFlagSkipsOversizedStdin",
			args:  []string{"--params-stdin", "--params=", "etl"},
			stdin: oversizedInput,
		},
		{
			name:       "FlagSkipsOversizedStdin",
			args:       []string{"--params-stdin", "--params=P1=flag", "etl"},
			stdin:      oversizedInput,
			wantParams: new("P1=flag"),
		},
		{
			name:       "DashSkipsOversizedStdin",
			args:       []string{"--params-stdin", "etl", "--", "P1=dash"},
			stdin:      oversizedInput,
			wantParams: new(`P1="dash"`),
		},
		{
			name:    "OversizedStdinRejected",
			args:    []string{"--params-stdin", "etl"},
			stdin:   oversizedInput,
			wantErr: "params from stdin exceed",
		},
		{
			name:  "WhitespaceStdin",
			args:  []string{"--params-stdin", "etl"},
			stdin: " \n\t\n",
		},
		{
			name:       "JSONStdin",
			args:       []string{"--params-stdin", "etl"},
			stdin:      `{"P1":"foo","P2":"bar"}`,
			wantParams: new(`{"P1":"foo","P2":"bar"}`),
		},
		{
			name:       "NamedQuotedStdin",
			args:       []string{"--params-stdin", "etl"},
			stdin:      "  P1=\"foo bar\" P2=\"\"\n",
			wantParams: new(`P1="foo bar" P2=""`),
			wantValues: []string{"P1=foo bar", "P2="},
		},
		{
			name:       "QuotedFlag",
			args:       []string{"--params-stdin", `--params="P1=foo P2=bar"`, "etl"},
			stdin:      "P1=stdin",
			wantParams: new("P1=foo P2=bar"),
			wantValues: []string{"P1=foo", "P2=bar"},
		},
		{
			name:       "DashSpacedValue",
			args:       []string{"etl", "--", "hello world"},
			wantParams: new(`"hello world"`),
			wantValues: []string{"P1=default1", "P2=default2", "1=hello world"},
		},
		{
			name:       "DashEmptyValue",
			args:       []string{"etl", "--", ""},
			wantParams: new(`""`),
			wantValues: []string{"P1=default1", "P2=default2", "1="},
		},
		{
			name:       "QuotedValueStdin",
			args:       []string{"--params-stdin", "etl"},
			stdin:      `"hello world"`,
			wantParams: new(`"hello world"`),
			wantValues: []string{"P1=default1", "P2=default2", "1=hello world"},
		},
		{
			name:       "EmptyValueStdin",
			args:       []string{"--params-stdin", "etl"},
			stdin:      `""`,
			wantParams: new(`""`),
			wantValues: []string{"P1=default1", "P2=default2", "1="},
		},
		{
			name:       "SpacedValueStdin",
			args:       []string{"--params-stdin", "etl"},
			stdin:      `" hello world "`,
			wantParams: new(`" hello world "`),
			wantValues: []string{"P1=default1", "P2=default2", "1= hello world "},
		},
	}
	for _, commandSpec := range commands {
		t.Run(commandSpec.name, func(t *testing.T) {
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					if tt.startOnly && commandSpec.name != "start" {
						t.Skip("enqueue does not support --from-run-id")
					}
					if tt.closed {
						stdin, err := os.Open(os.DevNull)
						require.NoError(t, err)
						require.NoError(t, stdin.Close())
						original := os.Stdin
						os.Stdin = stdin
						t.Cleanup(func() { os.Stdin = original })
					} else if len(tt.stdin) > maxStdinParamsSize {
						// Files avoid blocking on pipe capacity before the command reads.
						path := filepath.Join(t.TempDir(), "params.txt")
						require.NoError(t, os.WriteFile(path, []byte(tt.stdin), 0o600))
						file, err := os.Open(path)
						require.NoError(t, err)
						original := os.Stdin
						os.Stdin = file
						t.Cleanup(func() {
							os.Stdin = original
							require.NoError(t, file.Close())
						})
					} else {
						pipeStdin(t, tt.stdin)
					}

					requests := make(chan *string, 1)
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						if r.Method == http.MethodGet {
							_, _ = w.Write([]byte(`{"dag":{"name":"etl"}}`))
							return
						}
						var body struct {
							Params *string `json:"params"`
						}
						if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&body)) {
							w.WriteHeader(http.StatusBadRequest)
							return
						}
						requests <- body.Params
						_, _ = w.Write([]byte(`{"dagRunId":"run-1"}`))
					}))
					defer server.Close()

					command := &cobra.Command{Use: commandSpec.name}
					initFlags(command, commandSpec.flags...)
					require.NoError(t, command.Flags().Parse(tt.args))
					ctx := &Context{
						Context: context.Background(),
						Command: command,
						Remote:  &remoteClient{baseURL: server.URL, client: server.Client()},
					}

					err := commandSpec.run(ctx, command.Flags().Args())
					if tt.wantErr != "" {
						require.ErrorContains(t, err, tt.wantErr)
						select {
						case <-requests:
							t.Fatal("run submitted after invalid stdin")
						default:
						}
						return
					}
					require.NoError(t, err)
					if tt.wantUnread {
						remaining, err := io.ReadAll(os.Stdin)
						require.NoError(t, err)
						assert.Equal(t, tt.stdin, string(remaining))
					}
					select {
					case params := <-requests:
						if tt.wantParams == nil {
							require.Nil(t, params)
							return
						}
						require.NotNil(t, params)
						assert.Equal(t, *tt.wantParams, *params)
						if tt.wantValues != nil {
							source := []byte("params: P1=default1 P2=default2\nsteps:\n  - name: print\n    run: echo ok\n")
							dag, err := spec.LoadYAML(ctx, source, spec.WithParams(*params))
							require.NoError(t, err)
							assert.Equal(t, tt.wantValues, dag.Params)
						}
					default:
						t.Fatal("run was not submitted")
					}
				})
			}
		})
	}
}

func TestWaitForRemoteStopHonorsContextCancellation(t *testing.T) {
	t.Parallel()

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	ctx := &Context{
		Context: cancelled,
		Remote: &remoteClient{
			client: &http.Client{Timeout: time.Minute},
		},
	}

	err := waitForRemoteStop(ctx, "example", "run-1")
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.Canceled))
}

func TestEnrichRemoteHistoryStatusPopulatesErrorAndMetadata(t *testing.T) {
	t.Parallel()

	status := &ir.DAGRunStatus{Name: "example", DAGRunID: "run-1"}
	detail := &api.DAGRunDetails{
		Name:           "example",
		DagRunId:       "run-1",
		RootDAGRunName: "example",
		RootDAGRunId:   "run-1",
		Status:         api.Status(ir.Failed),
		WorkerId:       new("worker-a"),
		Labels:         &[]string{"env=prod"},
		Nodes: []api.Node{
			{
				Step:   api.Step{Name: "step-1"},
				Status: api.NodeStatus(ir.NodeFailed),
				Error:  new("boom"),
			},
		},
	}

	require.NoError(t, enrichRemoteHistoryStatus(status, detail))
	assert.Equal(t, []string{"env=prod"}, status.Labels)
	assert.Equal(t, "worker-a", status.WorkerID)
	assert.Contains(t, status.Error, "boom")
}
