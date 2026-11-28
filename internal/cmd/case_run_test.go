// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/dagtest"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/proc"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/agent"
	"github.com/dagucloud/dagu/v2/internal/runtime/stepmock"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The DAG writes state, a redirect file, an artifact, and logs to a DAG-level
// log directory, and the run emits events. None of it reaches the configured
// home.
func TestCaseLeavesHomeUnchanged(t *testing.T) {
	t.Parallel()

	ctx, home := newCaseTestContext(t)
	dag := writeCaseDAG(t, ctx, "isolated", fmt.Sprintf(`
log_dir: %q
artifacts:
  enabled: true
steps:
  - name: save
    action: state.set
    with:
      key: cursor
      value: 1
  - name: report
    run: echo report
    stdout: report.txt
  - name: archive
    run: echo archive
    stdout:
      artifact: archive.txt
`, filepath.Join(home, "dag-logs")))
	before := dirSnapshot(t, home)

	tc, err := openCase(ctx, caseOptions{tempDir: t.TempDir()})
	require.NoError(t, err)
	status, err := tc.run(dag)
	require.NoError(t, err)
	require.NoError(t, tc.close())

	assert.Equal(t, ir.Succeeded, status.Status, status.Error)
	assert.Equal(t, before, dirSnapshot(t, home))
}

// A run that fails while its agent is set up records no terminal status, so
// the case reports the failure. A file in place of the case's log directory
// makes the log file fail to open.
func TestCaseSetupFailure(t *testing.T) {
	t.Parallel()

	ctx, _ := newCaseTestContext(t)
	dag := writeCaseDAG(t, ctx, "setup", "steps:\n  - run: echo ok\n")
	tc := openTestCase(t, ctx, caseOptions{tempDir: t.TempDir()})
	require.NoError(t, os.WriteFile(tc.ctx.Config.Paths.LogDir, nil, 0o600))

	status, err := tc.run(dag)
	require.ErrorContains(t, err, "failed to initialize log file")
	assert.Nil(t, status)
}

func TestCaseClose(t *testing.T) {
	t.Parallel()

	for _, keep := range []bool{false, true} {
		t.Run(fmt.Sprintf("keep=%t", keep), func(t *testing.T) {
			t.Parallel()

			ctx, _ := newCaseTestContext(t)
			dag := writeCaseDAG(t, ctx, "close", "steps:\n  - run: echo ok\n")
			tempDir := t.TempDir()

			tc, err := openCase(ctx, caseOptions{tempDir: tempDir, keep: keep})
			require.NoError(t, err)
			_, err = tc.run(dag)
			require.NoError(t, err)
			require.NoError(t, tc.close())

			entries, err := os.ReadDir(tempDir)
			require.NoError(t, err)
			if keep {
				require.Len(t, entries, 1)
				assert.Equal(t, tc.dir, filepath.Join(tempDir, entries[0].Name()))
			} else {
				assert.Empty(t, entries)
			}
		})
	}
}

// A step outside real writes its redirect under the case directory, and a
// step in real writes it at its configured path.
func TestCaseRedirects(t *testing.T) {
	t.Parallel()

	ctx, _ := newCaseTestContext(t)
	workDir := t.TempDir()
	dag := writeCaseDAG(t, ctx, "redirects", fmt.Sprintf(`
working_dir: %q
steps:
  - name: moved
    run: echo moved
    stdout: out/result.txt
  - name: kept
    run: echo kept
    stdout: kept.txt
`, workDir))

	tc := openTestCase(t, ctx, caseOptions{tempDir: t.TempDir(), real: []string{"kept"}})
	status, err := tc.run(dag)
	require.NoError(t, err)
	require.Equal(t, ir.Succeeded, status.Status, status.Error)

	entries, err := os.ReadDir(workDir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "kept.txt", entries[0].Name())

	moved := findFile(t, tc.dir, "result.txt")
	require.NotEmpty(t, moved, "redirect file is not in the case directory")
	assert.Equal(t, "moved", readOutput(t, moved))
}

// The env provider would fail on the unset variables, so a passing run
// resolved its secrets from the test values and placeholders. The step runs
// only when its preconditions find the values the case expects.
func TestCaseSecrets(t *testing.T) {
	t.Parallel()

	const yaml = `
secrets:
  - name: API_TOKEN
    provider: env
    key: DAGU_CASE_TEST_UNSET_TOKEN
  - name: DB_PASSWORD
    provider: env
    key: DAGU_CASE_TEST_UNSET_PASSWORD
steps:
  - name: use
    preconditions:
      - condition: "${API_TOKEN}"
        expected: %q
      - condition: "${DB_PASSWORD}"
        expected: %q
    run: echo token=$API_TOKEN
`

	for _, tt := range []struct {
		name         string
		secrets      map[string]string
		real         []string
		wantToken    string
		wantPassword string
	}{
		{name: "Placeholders", wantToken: "<secret:API_TOKEN>", wantPassword: "<secret:DB_PASSWORD>"},
		{
			name:         "ValueOverPlaceholder",
			secrets:      map[string]string{"API_TOKEN": "case-token"},
			wantToken:    "case-token",
			wantPassword: "<secret:DB_PASSWORD>",
		},
		{
			name:         "RealStep",
			secrets:      map[string]string{"API_TOKEN": "case-token", "DB_PASSWORD": "case-password"},
			real:         []string{"use"},
			wantToken:    "case-token",
			wantPassword: "case-password",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx, _ := newCaseTestContext(t)
			dag := writeCaseDAG(t, ctx, "secrets", fmt.Sprintf(yaml, tt.wantToken, tt.wantPassword))
			tc := openTestCase(t, ctx, caseOptions{tempDir: t.TempDir(), secrets: tt.secrets, real: tt.real})

			status, err := tc.run(dag)
			require.NoError(t, err)
			require.Equal(t, ir.Succeeded, status.Status, status.Error)
			require.Equal(t, ir.NodeSucceeded, status.Nodes[0].Status, "a secret does not hold the expected value")
			assert.Equal(t, "token=*******", readOutput(t, status.Nodes[0].Stdout))
		})
	}
}

// A case with a real step needs a test value for every DAG secret and SSH
// credential ref, and starts no run without one.
func TestCaseSecretsMissing(t *testing.T) {
	t.Parallel()

	const yaml = `
secrets:
  - name: API_TOKEN
    provider: env
    key: DAGU_CASE_TEST_UNSET_TOKEN
steps:
  - name: remote
    action: ssh.run
    with:
      host: app.invalid
      command: "true"
      password: {secret: ssh/password}
`

	for _, tt := range []struct {
		name    string
		secrets map[string]string
		missing string
	}{
		{name: "Secret", secrets: map[string]string{"ssh/password": "case-password"}, missing: "API_TOKEN"},
		{name: "CredentialRef", secrets: map[string]string{"API_TOKEN": "case-token"}, missing: "ssh/password"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx, _ := newCaseTestContext(t)
			dag := writeCaseDAG(t, ctx, "secrets", yaml)
			tc := openTestCase(t, ctx, caseOptions{tempDir: t.TempDir(), secrets: tt.secrets, real: []string{"remote"}})

			_, err := tc.run(dag)
			require.ErrorContains(t, err, tt.missing)
			runs, err := tc.ctx.Persistence.DAGRunRepository.RecentStatuses(tc.ctx, dag.Name, 1)
			require.NoError(t, err)
			assert.Empty(t, runs, "the case started a run")
		})
	}
}

// A case with no real step leaves an SSH credential ref unresolved, so it
// needs no test value for it.
func TestCaseSSHCredentialRef(t *testing.T) {
	t.Parallel()

	ctx, _ := newCaseTestContext(t)
	dag := writeCaseDAG(t, ctx, "ssh", `
ssh:
  user: deploy
  host: app.invalid
  key: {secret: ssh/deploy-key}
steps:
  - name: check
    action: noop
`)
	tc := openTestCase(t, ctx, caseOptions{tempDir: t.TempDir()})

	status, err := tc.run(dag)
	require.NoError(t, err)
	assert.Equal(t, ir.Succeeded, status.Status, status.Error)
}

// A case records the mail its run would have sent and reaches no SMTP
// server.
func TestCaseSendsNoMail(t *testing.T) {
	t.Parallel()

	host, port, connections := listenSMTP(t)
	ctx, _ := newCaseTestContext(t)
	dag := writeCaseDAG(t, ctx, "mail", fmt.Sprintf(`
smtp:
  host: %s
  port: "%s"
mail_on:
  success: true
info_mail:
  from: dagu@example.com
  to: ops@example.com
steps:
  - run: echo ok
`, host, port))
	tc := openTestCase(t, ctx, caseOptions{tempDir: t.TempDir()})

	status, err := tc.run(dag)
	require.NoError(t, err)
	require.Equal(t, ir.Succeeded, status.Status, status.Error)
	assert.Zero(t, connections.Load(), "mail sent")
	assert.Equal(t, []agent.Notification{
		{Kind: agent.NotificationMailOnSuccess, To: []string{"ops@example.com"}},
	}, tc.notifications)
}

// A case reports nothing to Dagu Console, even with a run key set. The
// config is set directly, so the fake Console need not be https.
func TestCaseSendsNoJobReport(t *testing.T) {
	t.Parallel()

	var reports atomic.Int32
	console := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reports.Add(1)
		_, _ = io.WriteString(w, `{"stored":1}`)
	}))
	t.Cleanup(console.Close)

	ctx, _ := newCaseTestContext(t)
	ctx.Config.Cloud.RunKey = testRunKey
	ctx.Config.License.CloudURL = console.URL
	dag := writeCaseDAG(t, ctx, "job", "steps:\n  - run: echo ok\n")
	tc := openTestCase(t, ctx, caseOptions{tempDir: t.TempDir()})

	status, err := tc.run(dag)
	require.NoError(t, err)
	require.Equal(t, ir.Succeeded, status.Status, status.Error)
	assert.Zero(t, reports.Load(), "job run reported")
}

// A real dag.run step starts its child in the same case. The child takes its
// secret from the test values, since the env provider would fail on the
// unset variable, and writes its redirect under the case directory.
func TestCaseChildRun(t *testing.T) {
	t.Parallel()

	ctx, _ := newCaseTestContext(t)
	workDir := t.TempDir()
	dag := writeCaseDAG(t, ctx, "parent", fmt.Sprintf(`
steps:
  - name: call
    action: dag.run
    with:
      dag: child
---
name: child
working_dir: %q
secrets:
  - name: CHILD_TOKEN
    provider: env
    key: DAGU_CASE_TEST_UNSET_CHILD_TOKEN
steps:
  - name: write
    run: echo child=$CHILD_TOKEN
    stdout: child.txt
`, workDir))
	tc := openTestCase(t, ctx, caseOptions{
		tempDir: t.TempDir(),
		secrets: map[string]string{"CHILD_TOKEN": "child-token"},
		real:    []string{"call"},
	})

	status, err := tc.run(dag)
	require.NoError(t, err)
	require.Equal(t, ir.Succeeded, status.Status, status.Error)

	entries, err := os.ReadDir(workDir)
	require.NoError(t, err)
	assert.Empty(t, entries)
	moved := findFile(t, tc.dir, "child.txt")
	require.NotEmpty(t, moved, "the child's redirect file is not in the case directory")
	assert.Equal(t, "child=*******", readOutput(t, moved))
}

// The child of a real dag.run step runs under the case's side effects: it
// sends no mail, keeps its artifacts in the case directory although its DAG
// sets artifacts.dir, and does not wait for its repeat interval.
func TestCaseChildSideEffects(t *testing.T) {
	t.Parallel()

	host, port, connections := listenSMTP(t)
	ctx, home := newCaseTestContext(t)
	artifacts := filepath.Join(home, "child-artifacts")
	dag := writeCaseDAG(t, ctx, "parent", fmt.Sprintf(`
steps:
  - name: call
    action: dag.run
    with:
      dag: child
---
name: child
smtp:
  host: %s
  port: "%s"
mail_on:
  success: true
info_mail:
  from: dagu@example.com
  to: ops@example.com
artifacts:
  enabled: true
  dir: %q
steps:
  - name: report
    run: echo report
    stdout:
      artifact: report.txt
    repeat_policy:
      repeat: while
      limit: 2
      interval_sec: 60
`, host, port, artifacts))
	tc := openTestCase(t, ctx, caseOptions{tempDir: t.TempDir(), real: []string{"call"}})

	started := time.Now()
	status, err := tc.run(dag)
	require.NoError(t, err)
	require.Equal(t, ir.Succeeded, status.Status, status.Error)
	assert.Less(t, time.Since(started), 30*time.Second, "the child waited for its repeat interval")
	assert.Zero(t, connections.Load(), "the child sent mail")
	assert.NoDirExists(t, artifacts)
	assert.NotEmpty(t, findFile(t, tc.dir, "report.txt"), "the child's artifact is not in the case directory")
}

// A step in real waits for its retry interval, and a mocked step with a
// longer interval does not. Windows runs the real step in PowerShell, so it
// takes its own command to fail once.
func TestCaseWaits(t *testing.T) {
	t.Parallel()

	ctx, _ := newCaseTestContext(t)
	failOnce := test.ForOS(
		"test -f failed-once || { touch failed-once; exit 1; }",
		"if (Test-Path failed-once) { exit 0 }; New-Item -ItemType File failed-once | Out-Null; exit 1",
	)
	dag := writeCaseDAG(t, ctx, "waits", fmt.Sprintf(`
working_dir: %q
steps:
  - name: mocked
    run: ./flaky.sh
    retry_policy:
      limit: 1
      interval_sec: 60
  - name: real
    depends: mocked
    run: '%s'
    retry_policy:
      limit: 1
      interval_sec: 1
`, t.TempDir(), failOnce))
	tc := openTestCase(t, ctx, caseOptions{
		tempDir: t.TempDir(),
		real:    []string{"real"},
		mocks:   map[string]stepmock.Step{"mocked": {Results: []stepmock.Result{{ExitCode: 1}, {}}}},
	})

	started := time.Now()
	status, err := tc.run(dag)
	elapsed := time.Since(started)
	require.NoError(t, err)
	require.Equal(t, ir.Succeeded, status.Status, status.Error)
	require.Equal(t, 1, caseNode(t, status, "real").RetryCount, "the real step did not fail once")
	assert.GreaterOrEqual(t, elapsed, time.Second, "the real step skipped its retry interval")
	assert.Less(t, elapsed, 30*time.Second, "the mocked step waited for its retry interval")
}

func TestCaseTrigger(t *testing.T) {
	t.Parallel()

	const yaml = "steps:\n  - name: trigger\n    run: echo ${context.trigger.type}\n"

	for _, tt := range []struct {
		name    string
		trigger ir.TriggerType
		want    string
	}{
		{name: "Default", want: "manual"},
		{name: "Restart", trigger: ir.TriggerTypeRestart, want: "restart"},
		{name: "Retry", trigger: ir.TriggerTypeRetry, want: "retry"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx, _ := newCaseTestContext(t)
			dag := writeCaseDAG(t, ctx, "trigger", yaml)
			tc := openTestCase(t, ctx, caseOptions{tempDir: t.TempDir(), trigger: tt.trigger})

			status, err := tc.run(dag)
			require.NoError(t, err)
			require.Equal(t, ir.Succeeded, status.Status, status.Error)
			assert.Equal(t, tt.want, readOutput(t, status.Nodes[0].Stdout))
		})
	}

	t.Run("SubDAG", func(t *testing.T) {
		t.Parallel()

		ctx, _ := newCaseTestContext(t)
		dag := writeCaseDAG(t, ctx, "trigger", yaml)
		tc := openTestCase(t, ctx, caseOptions{tempDir: t.TempDir(), trigger: ir.TriggerTypeSubDAG})

		_, err := tc.run(dag)
		require.ErrorContains(t, err, "subdag")
	})
}

// A service DAG has one run at a time. The live run is recorded in the
// configured data directory, which the case's checks do not read, and the
// case's run is recorded only in the case directory.
func TestCaseServiceBesideLiveRun(t *testing.T) {
	t.Parallel()

	ctx, _ := newCaseTestContext(t)
	dag := writeCaseDAG(t, ctx, "service", `
restart_policy: always
restart_wait_sec: 1
steps:
  - run: echo ok
`)

	const liveRunID = "live"
	live := ir.NewDAGRunRef(dag.Name, liveRunID)
	attempt, err := ctx.Persistence.DAGRunRepository.CreateAttempt(ctx, dag, time.Now(), liveRunID, persis.DAGRunCreateAttemptOptions{})
	require.NoError(t, err)
	require.NoError(t, attempt.Open(ctx))
	require.NoError(t, attempt.Write(ctx, ir.NewStatusBuilder(dag).Create(liveRunID, ir.Running, os.Getpid(), time.Now(),
		ir.WithAttemptID(attempt.ID()),
		ir.WithWorkerID("local"),
	)))
	require.NoError(t, attempt.Close(ctx))
	handle, err := ctx.Persistence.ProcRepository.Acquire(ctx, dag.ProcGroup(), proc.ProcMeta{
		StartedAt:    time.Now().Unix(),
		Name:         dag.Name,
		DAGRunID:     liveRunID,
		AttemptID:    attempt.ID(),
		RootName:     dag.Name,
		RootDAGRunID: liveRunID,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = handle.Stop(context.Background()) })
	require.ErrorIs(t, ctx.DAGRunMgr.CheckServiceRun(ctx, dag, "manual"), runtime.ErrServiceRunActive)

	tc := openTestCase(t, ctx, caseOptions{tempDir: t.TempDir()})
	status, err := tc.run(dag)
	require.NoError(t, err)
	assert.Equal(t, ir.Succeeded, status.Status, status.Error)

	alive, err := ctx.Persistence.ProcRepository.ListAlive(ctx, dag.ProcGroup())
	require.NoError(t, err)
	assert.Equal(t, []ir.DAGRunRef{live}, alive)
	runs, err := ctx.Persistence.DAGRunRepository.RecentStatuses(ctx, dag.Name, 10)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, liveRunID, runs[0].DAGRunID)
	assert.ErrorIs(t, ctx.DAGRunMgr.CheckServiceRun(ctx, dag, "manual"), runtime.ErrServiceRunActive)
}

// Every field of config.PathsConfig is moved into the case directory or is
// kept on purpose, so a path added later cannot escape the case unnoticed.
func TestCasePaths(t *testing.T) {
	t.Parallel()

	// The inputs of a run, and the tools directory, where declared tools
	// install as in a real run. Explicit only says which paths were set; the
	// paths it names are moved.
	kept := []string{
		"DAGsDir", "ProjectsDir", "AltDAGsDir", "BaseConfig", "CalendarsDir", "WikiDir", "WikiDirLegacy",
		"Executable", "ToolsDir", "ConfigFileUsed", "ConfigFilesUsed", "DefaultConfigFile", "Explicit",
	}

	configured := filepath.Join(t.TempDir(), "configured")
	dir := filepath.Join(t.TempDir(), "case")
	var paths config.PathsConfig
	fields := reflect.ValueOf(&paths).Elem()
	for i := range fields.NumField() {
		field, name := fields.Field(i), fields.Type().Field(i).Name
		switch ptr := field.Addr().Interface().(type) {
		case *string:
			*ptr = filepath.Join(configured, name)
		case *bool:
			*ptr = true
		case *[]string:
			*ptr = []string{filepath.Join(configured, name)}
		case *config.ExplicitPaths:
			*ptr = config.ExplicitPaths{LogDir: true, DAGRunsDir: true}
		default:
			t.Fatalf("PathsConfig.%s has unexpected kind %s", name, field.Kind())
		}
	}

	moved := reflect.ValueOf(casePaths(paths, dir))
	for i := range fields.NumField() {
		name := fields.Type().Field(i).Name
		got := moved.Field(i).Interface()
		if slices.Contains(kept, name) {
			assert.Equal(t, fields.Field(i).Interface(), got, "PathsConfig.%s is kept", name)
			continue
		}
		path, ok := got.(string)
		if !assert.True(t, ok, "PathsConfig.%s is neither moved nor kept", name) || path == "" {
			continue
		}
		rel, err := filepath.Rel(dir, path)
		assert.True(t, err == nil && !strings.HasPrefix(rel, ".."),
			"PathsConfig.%s is %s, neither moved into the case directory nor kept", name, path)
	}
}

// The run reaches its waiting checkpoint as a real run does: handler_on.wait
// runs while the gate waits, and the wait notification is recorded. The case
// then settles the gate, and the run resumes under the same run ID. The
// success notification comes from the resumed attempt, so the case records
// the notifications of every attempt.
func TestCaseWaitCheckpoint(t *testing.T) {
	t.Parallel()

	tc, status, err := runCase(t, `
mail_on:
  wait: true
  success: true
wait_mail:
  from: dagu@example.com
  to: approvers@example.com
info_mail:
  from: dagu@example.com
  to: ops@example.com
handler_on:
  wait:
    run: echo waiting=${DAG_WAITING_STEPS}
    stdout: wait.txt
steps:
  - name: gate
    run: echo built
    approval:
      prompt: Ship it?
  - name: after
    depends: gate
    run: echo after
`, caseOptions{})
	require.NoError(t, err)
	require.Equal(t, ir.Succeeded, status.Status, status.Error)

	assert.Equal(t, "waiting=gate", readOutput(t, findFile(t, tc.dir, "wait.txt")))
	assert.Equal(t, []agent.Notification{
		{Kind: agent.NotificationMailOnWait, To: []string{"approvers@example.com"}},
		{Kind: agent.NotificationMailOnSuccess, To: []string{"ops@example.com"}},
	}, tc.notifications)
	assert.Equal(t, ir.NodeSucceeded, caseNode(t, status, "after").Status)
	runs, err := tc.ctx.Persistence.DAGRunRepository.RecentStatuses(tc.ctx, status.Name, 10)
	require.NoError(t, err)
	assert.Len(t, runs, 1, "the resume started another run")
}

// A wait that needs no values and has no review is approved automatically
// with no values, and listed in the order it was settled. A required form
// property with a default needs no value, and the default becomes its output.
func TestCaseAutoApproval(t *testing.T) {
	t.Parallel()

	tc, status, err := runCase(t, `
steps:
  - id: ack
    action: human.task
    with:
      prompt: Acknowledge
  - id: defaults
    action: human.task
    with:
      prompt: Pick a size
      form:
        type: object
        properties:
          size:
            type: string
            default: small
        required: [size]
  - name: gate
    run: echo built
    approval:
      prompt: Ship it?
      input: [NOTE]
  - name: done
    depends: [ack, defaults, gate]
    run: echo size=${steps.defaults.outputs.size}
`, caseOptions{})
	require.NoError(t, err)
	require.Equal(t, ir.Succeeded, status.Status, status.Error)

	assert.Equal(t, []string{"ack", "defaults", "gate"}, tc.autoApproved)
	assert.Equal(t, "size=small", readOutput(t, caseNode(t, status, "done").Stdout))
}

// A wait that needs values fails without a review, through the same
// validation a person's completion takes.
func TestCaseWaitNeedsReview(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		yaml string
		want string
	}{
		{
			name: "HumanTask",
			yaml: `
steps:
  - id: pick
    action: human.task
    with:
      prompt: Pick a window
      form:
        type: object
        properties:
          window:
            type: string
        required: [window]
`,
			want: "step pick: approving its wait with no values failed; give it a review",
		},
		{
			name: "Approval",
			yaml: `
steps:
  - name: announce
    run: echo announce
    approval:
      prompt: Announce it?
      input: [CHANNEL]
      required: [CHANNEL]
`,
			want: "missing required inputs: [CHANNEL]",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tc, _, err := runCase(t, tt.yaml, caseOptions{})
			require.ErrorContains(t, err, tt.want)
			assert.Empty(t, tc.autoApproved)
		})
	}
}

// A real dag.run step whose child run waits is waiting too, even when the
// step has its own gate. The case does not approve that gate, which would
// leave the child unsettled, and fails with the step's name. Once the child
// finishes, the step's gate takes its review.
func TestCaseChildWait(t *testing.T) {
	t.Parallel()

	const yaml = `
steps:
  - name: call
    action: dag.run
    with:
      dag: child
    approval:
      prompt: Accept the child's result?
---
name: child
steps:
  - name: build
    run: echo built
%s`
	opts := caseOptions{
		mocks: map[string]stepmock.Step{"call": {Review: &stepmock.Review{}}},
		real:  []string{"call"},
	}

	t.Run("Waiting", func(t *testing.T) {
		t.Parallel()

		_, _, err := runCase(t, fmt.Sprintf(yaml, "    approval:\n      prompt: Ship it?\n"), opts)
		require.ErrorContains(t, err, "step call waits for its child run")
	})

	t.Run("Finished", func(t *testing.T) {
		t.Parallel()

		_, status, err := runCase(t, fmt.Sprintf(yaml, ""), opts)
		require.NoError(t, err)
		require.Equal(t, ir.Succeeded, status.Status, status.Error)
		assert.NotEmpty(t, caseNode(t, status, "call").ApprovedAt)
	})
}

// approve completes a human task with the review's values as its typed
// outputs, and approves a gate with them as its inputs.
func TestCaseReviewApprove(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name   string
		yaml   string
		values map[string]any
		want   string
	}{
		{
			name: "HumanTask",
			yaml: `
steps:
  - id: pick
    action: human.task
    with:
      prompt: Pick a window
      form:
        type: object
        properties:
          window:
            type: string
            enum: [morning, evening]
          replicas:
            type: integer
        required: [window, replicas]
  - name: after
    depends: pick
    run: echo ${steps.pick.outputs.window}_${steps.pick.outputs.replicas}
`,
			values: map[string]any{"window": "evening", "replicas": 3},
			want:   "evening_3",
		},
		{
			name: "Approval",
			yaml: `
steps:
  - name: pick
    run: echo built
    approval:
      prompt: Ship it?
      input: [CHANNEL, COUNT]
      required: [CHANNEL]
  - name: after
    depends: pick
    run: echo ${CHANNEL}_${COUNT}
`,
			values: map[string]any{"CHANNEL": "releases", "COUNT": 3},
			want:   "releases_3",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tc, status, err := runCase(t, tt.yaml, caseOptions{
				mocks: map[string]stepmock.Step{"pick": {Review: &stepmock.Review{Values: tt.values}}},
			})
			require.NoError(t, err)
			require.Equal(t, ir.Succeeded, status.Status, status.Error)
			assert.Equal(t, tt.want, readOutput(t, caseNode(t, status, "after").Stdout))
			assert.Empty(t, tc.autoApproved)
		})
	}

	// Values are typed as --inputs-json types them, so a string does not
	// satisfy an integer property.
	t.Run("HumanTaskTyped", func(t *testing.T) {
		t.Parallel()

		_, _, err := runCase(t, `
steps:
  - id: pick
    action: human.task
    with:
      prompt: How many?
      form:
        type: object
        properties:
          replicas:
            type: integer
`, caseOptions{mocks: map[string]stepmock.Step{
			"pick": {Review: &stepmock.Review{Values: map[string]any{"replicas": "3"}}},
		}})
		require.ErrorContains(t, err, `step pick: invalid input for human task step "pick"`)
	})
}

// reject rejects a gate as a person does: the step and the run end rejected,
// and its dependent never starts. A human task cannot be rejected.
func TestCaseReviewReject(t *testing.T) {
	t.Parallel()

	reject := &stepmock.Review{Reject: true}
	t.Run("Approval", func(t *testing.T) {
		t.Parallel()

		_, status, err := runCase(t, `
steps:
  - name: release
    run: echo built
    approval:
      prompt: Ship it?
  - name: announce
    depends: release
    run: echo announce
`, caseOptions{mocks: map[string]stepmock.Step{"release": {Review: reject}}})
		require.NoError(t, err)
		assert.Equal(t, ir.Rejected, status.Status)
		assert.Equal(t, ir.NodeRejected, caseNode(t, status, "release").Status)
		assert.Equal(t, ir.NodeNotStarted, caseNode(t, status, "announce").Status)
	})

	t.Run("HumanTask", func(t *testing.T) {
		t.Parallel()

		_, _, err := runCase(t, `
steps:
  - id: ack
    action: human.task
    with:
      prompt: Acknowledge
`, caseOptions{mocks: map[string]stepmock.Step{"ack": {Review: reject}}})
		require.ErrorContains(t, err, "step ack: a human task cannot be rejected")
	})
}

// A gate takes its review after its action, whether the action is real or
// mocked. A mocked action retries as its results script and reaches the gate
// once. A mocked dependent stays mocked in the resumed attempt.
func TestCaseReviewAfterAction(t *testing.T) {
	t.Parallel()

	const yaml = `
steps:
  - name: release
    run: echo released
    retry_policy:
      limit: 1
      interval_sec: 0
    approval:
      prompt: Ship it?
  - name: announce
    depends: release
    run: echo announced
`
	approve := &stepmock.Review{}
	announce := stepmock.Step{Results: []stepmock.Result{{Stdout: "mocked\n"}}}

	t.Run("Real", func(t *testing.T) {
		t.Parallel()

		tc, status, err := runCase(t, yaml, caseOptions{
			mocks: map[string]stepmock.Step{"release": {Review: approve}, "announce": announce},
			real:  []string{"release"},
		})
		require.NoError(t, err)
		require.Equal(t, ir.Succeeded, status.Status, status.Error)
		release := caseNode(t, status, "release")
		assert.Equal(t, "released", readOutput(t, release.Stdout))
		assert.NotEmpty(t, release.ApprovedAt)
		assert.Equal(t, "mocked", readOutput(t, caseNode(t, status, "announce").Stdout))
		assert.Empty(t, tc.autoApproved)
	})

	t.Run("Mocked", func(t *testing.T) {
		t.Parallel()

		_, status, err := runCase(t, yaml, caseOptions{mocks: map[string]stepmock.Step{
			"release": {
				Results: []stepmock.Result{{ExitCode: 1, Stdout: "first\n"}, {Stdout: "second\n"}},
				Review:  approve,
			},
			"announce": announce,
		}})
		require.NoError(t, err)
		require.Equal(t, ir.Succeeded, status.Status, status.Error)
		release := caseNode(t, status, "release")
		assert.Equal(t, "first\nsecond", readOutput(t, release.Stdout))
		assert.Equal(t, 1, release.RetryCount)
		assert.NotEmpty(t, release.ApprovedAt)
		assert.Equal(t, "mocked", readOutput(t, caseNode(t, status, "announce").Stdout))
	})
}

// A failed run with a DAG-level retry left is retried at once under the same
// run ID, as the scheduler retries it. The mocked step takes its next result
// in the retry, and the step the first attempt left not started sees the
// retry's trigger type. The failure handler waits for the last attempt, so
// it never runs.
func TestCaseRunRetry(t *testing.T) {
	t.Parallel()

	const interval = time.Hour
	start := time.Now()
	tc, status, err := runCase(t, fmt.Sprintf(`
retry_policy:
  limit: 2
  interval_sec: %d
handler_on:
  failure:
    run: echo failure
    stdout: failure.txt
steps:
  - name: sync
    run: ./sync.sh
  - name: report
    depends: sync
    run: echo ${context.trigger.type}
`, int(interval.Seconds())), caseOptions{
		trigger: ir.TriggerTypeScheduler,
		mocks: map[string]stepmock.Step{"sync": {Results: []stepmock.Result{
			{ExitCode: 1, Stdout: "first\n"},
			{Stdout: "second\n"},
		}}},
	})
	require.NoError(t, err)
	require.Equal(t, ir.Succeeded, status.Status, status.Error)

	assert.Less(t, time.Since(start), interval, "the retry waited for its interval")
	assert.Equal(t, 2, tc.attempts)
	assert.Equal(t, "second", readOutput(t, caseNode(t, status, "sync").Stdout))
	assert.Equal(t, "retry", readOutput(t, caseNode(t, status, "report").Stdout))
	assert.Empty(t, findFile(t, tc.dir, "failure.txt"), "the failure handler ran while a retry remained")
	runs, err := tc.ctx.Persistence.DAGRunRepository.RecentStatuses(tc.ctx, status.Name, 10)
	require.NoError(t, err)
	assert.Len(t, runs, 1, "the retry started another run")
	// The last attempt's status holds one execution of sync; the case counts
	// the executions of both attempts.
	assert.Empty(t, tc.check(dagtest.Expect{Attempts: new(2), Steps: []dagtest.StepExpect{
		{Key: "sync", Step: "sync", Attempts: new(2)},
		{Key: "report", Step: "report", Attempts: new(1)},
	}}, status))
}

// A stream assertion reads the step's last execution: the last retry of a
// mocked step and the last repeat of a real one, not their whole logs. The
// run retries once for gate and carries flaky and tick over, so their
// streams come from the first attempt's logs.
func TestCaseLastExecutionStream(t *testing.T) {
	t.Parallel()

	tc, status, err := runCase(t, `
retry_policy:
  limit: 1
  interval_sec: 60
steps:
  - name: flaky
    run: ./flaky.sh
    retry_policy:
      limit: 1
      interval_sec: 60
  - name: tick
    action: log.write
    with:
      message: tick ${context.step.repeat_index}
    repeat_policy:
      repeat: while
      limit: 3
  - name: gate
    depends: [flaky, tick]
    run: ./gate.sh
`, caseOptions{mocks: map[string]stepmock.Step{
		"flaky": {Results: []stepmock.Result{
			{ExitCode: 1, Stdout: "first\n", Stderr: "boom\n"},
			{Stdout: "second\n"},
		}},
		"gate": {Results: []stepmock.Result{{ExitCode: 1}, {}}},
	}})
	require.NoError(t, err)
	require.Equal(t, ir.Succeeded, status.Status, status.Error)
	require.Equal(t, 2, tc.attempts)

	failures := tc.check(dagtest.Expect{Steps: []dagtest.StepExpect{
		{Key: "flaky", Step: "flaky", Stdout: &dagtest.StreamExpect{
			Contains: new("first"),
			Matches:  regexp.MustCompile(`^second\n$`),
		}, Stderr: &dagtest.StreamExpect{Contains: new("boom")}},
		{Key: "tick", Step: "tick", Stdout: &dagtest.StreamExpect{
			Contains: new("tick 1"),
			Matches:  regexp.MustCompile(`^tick 2\s*$`),
		}},
	}}, status)
	assert.Equal(t, []string{
		`step flaky stdout: does not contain "first"`,
		`step flaky stderr: does not contain "boom"`,
		`step tick stdout: does not contain "tick 1"`,
	}, failureLines(failures))
}

// A run that fails every attempt ends failed after limit+1 attempts. As in a
// real run, the failure handler runs once, in the last attempt, and every
// failed attempt sends its failure mail, so the case records each one.
func TestCaseRunRetryExhausted(t *testing.T) {
	t.Parallel()

	tc, status, err := runCase(t, `
retry_policy:
  limit: 2
  interval_sec: 300
mail_on:
  failure: true
error_mail:
  from: dagu@example.com
  to: ops@example.com
handler_on:
  failure:
    run: echo failure
steps:
  - name: sync
    run: ./sync.sh
`, caseOptions{
		mocks: map[string]stepmock.Step{"sync": {Results: []stepmock.Result{{ExitCode: 1}}}},
		handlers: map[ir.HandlerType]stepmock.Step{
			ir.HandlerOnFailure: {Results: []stepmock.Result{{Stdout: "first\n"}, {Stdout: "second\n"}}},
		},
	})
	require.NoError(t, err)
	require.Equal(t, ir.Failed, status.Status)

	assert.Equal(t, 3, tc.attempts)
	require.NotNil(t, status.OnFailure)
	assert.Equal(t, ir.NodeSucceeded, status.OnFailure.Status)
	assert.Equal(t, "first", readOutput(t, status.OnFailure.Stdout))
	failure := agent.Notification{Kind: agent.NotificationMailOnFailure, To: []string{"ops@example.com"}}
	assert.Equal(t, []agent.Notification{failure, failure, failure}, tc.notifications)
}

// Retries and waits combine: the first attempt fails before the gate, the
// retry reaches the gate and is settled, the resumed attempt fails after it,
// and the second retry succeeds. A resume is part of the attempt it resumes,
// so the run has three attempts.
func TestCaseRunRetryWait(t *testing.T) {
	t.Parallel()

	failThenPass := stepmock.Step{Results: []stepmock.Result{
		{ExitCode: 1, Stdout: "first\n"},
		{Stdout: "second\n"},
	}}
	tc, status, err := runCase(t, `
retry_policy:
  limit: 2
  interval_sec: 300
steps:
  - name: sync
    run: ./sync.sh
  - name: gate
    depends: sync
    run: echo built
    approval:
      prompt: Ship it?
  - name: deploy
    depends: gate
    run: ./deploy.sh
`, caseOptions{mocks: map[string]stepmock.Step{"sync": failThenPass, "deploy": failThenPass}})
	require.NoError(t, err)
	require.Equal(t, ir.Succeeded, status.Status, status.Error)

	assert.Equal(t, 3, tc.attempts)
	assert.Equal(t, "second", readOutput(t, caseNode(t, status, "deploy").Stdout))
	assert.Equal(t, []string{"gate"}, tc.autoApproved)
}

// failureLines returns each failure as the text report writes it.
func failureLines(failures []caseFailure) []string {
	var lines []string
	for _, f := range failures {
		lines = append(lines, f.String())
	}
	return lines
}

// newCaseTestContext returns the context of dagu start with every configured
// path under the returned home directory.
func newCaseTestContext(t *testing.T) (*Context, string) {
	t.Helper()

	home := t.TempDir()
	configPath := filepath.Join(home, "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte("debug: false\n"), 0o600))

	command := &cobra.Command{Use: "start"}
	initFlags(command, startFlags...)
	command.SetContext(context.Background())
	require.NoError(t, command.Flags().Set("dagu-home", home))
	require.NoError(t, command.Flags().Set("config", configPath))

	ctx, err := NewContext(command, nil)
	require.NoError(t, err)
	return ctx, home
}

func writeCaseDAG(t *testing.T, ctx *Context, name, yaml string) *ir.DAG {
	t.Helper()

	path := filepath.Join(ctx.Config.Paths.DAGsDir, name+".yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(yaml), 0o600))
	dag, err := spec.Load(ctx, path, spec.WithBaseConfig(ctx.Config.Paths.BaseConfig))
	require.NoError(t, err)
	return dag
}

func openTestCase(t *testing.T, ctx *Context, opts caseOptions) *testCase {
	t.Helper()

	tc, err := openCase(ctx, opts)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, tc.close()) })
	return tc
}

// runCase runs the DAG of yaml in a new case with opts and returns the case
// and what its run returned.
func runCase(t *testing.T, yaml string, opts caseOptions) (*testCase, *ir.DAGRunStatus, error) {
	t.Helper()

	ctx, _ := newCaseTestContext(t)
	dag := writeCaseDAG(t, ctx, "case", yaml)
	opts.tempDir = t.TempDir()
	tc := openTestCase(t, ctx, opts)
	status, err := tc.run(dag)
	return tc, status, err
}

// listenSMTP returns the address of a listener that counts the connections
// it accepts. It closes each at once, so a mail client returns only after
// its connection was counted.
func listenSMTP(t *testing.T) (host, port string, connections *atomic.Int32) {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	connections = new(atomic.Int32)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			connections.Add(1)
			_ = conn.Close()
		}
	}()
	host, port, err = net.SplitHostPort(listener.Addr().String())
	require.NoError(t, err)
	return host, port, connections
}

// caseNode returns the node of the step named name.
func caseNode(t *testing.T, status *ir.DAGRunStatus, name string) *ir.Node {
	t.Helper()

	idx := slices.IndexFunc(status.Nodes, func(node *ir.Node) bool { return node.Step.Name == name })
	require.GreaterOrEqual(t, idx, 0, "no step %s", name)
	return status.Nodes[idx]
}

// dirSnapshot maps each path under root to the content of its file, or to
// "dir" for a directory.
func dirSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()

	snapshot := make(map[string]string)
	require.NoError(t, filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			snapshot[rel] = "dir"
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		snapshot[rel] = string(data)
		return nil
	}))
	return snapshot
}

// readOutput returns the text a step wrote to the file at path, without the
// line break echo ends it with, which is \r\n on Windows.
func readOutput(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return strings.TrimRight(string(data), "\r\n")
}

// findFile returns the path of the file named name under root, or "".
func findFile(t *testing.T, root, name string) string {
	t.Helper()

	var found string
	require.NoError(t, filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && entry.Name() == name {
			found = path
		}
		return nil
	}))
	return found
}
