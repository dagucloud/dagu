// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package distr_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	persisfile "github.com/dagucloud/dagu/v2/internal/persis/file"
	persiststore "github.com/dagucloud/dagu/v2/internal/persis/store"
	profilepkg "github.com/dagucloud/dagu/v2/internal/profile"
	secretpkg "github.com/dagucloud/dagu/v2/internal/secret"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
	"github.com/dagucloud/dagu/v2/internal/test"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
)

func directStartStatusTimeout() time.Duration {
	switch {
	case runtime.GOOS == "windows" && raceEnabled():
		return 45 * time.Second
	case runtime.GOOS == "windows":
		return 30 * time.Second
	default:
		return 20 * time.Second
	}
}

func executionStatusTimeout() time.Duration {
	switch {
	case runtime.GOOS == "windows" && raceEnabled():
		return 45 * time.Second
	case runtime.GOOS == "windows":
		return 30 * time.Second
	default:
		return 20 * time.Second
	}
}

func artifactExecutionStatusTimeout() time.Duration {
	switch {
	case runtime.GOOS == "windows" && raceEnabled():
		return 60 * time.Second
	case runtime.GOOS == "windows":
		// Distributed artifact persistence has to archive worker output and
		// stream it back to the coordinator, which is materially slower on the
		// GitHub-hosted Windows runners than ordinary distributed execution.
		return 45 * time.Second
	default:
		return 20 * time.Second
	}
}

func artifactStepShellYAML() string {
	if runtime.GOOS == "windows" {
		return "    shell: powershell\n"
	}
	return "    shell: /bin/sh\n"
}

func logStepShellYAML() string {
	if runtime.GOOS == "windows" {
		return "    shell: cmd\n"
	}
	return "    shell: /bin/sh\n"
}

func logOutputCommands(stdout, stderr string) string {
	if runtime.GOOS == "windows" {
		return test.JoinLines(
			"echo "+stdout,
			"echo "+stderr+" 1>&2",
		)
	}
	return test.JoinLines(
		test.Output(stdout),
		test.Stderr(stderr),
	)
}

func indentYAMLBlock(s string, spaces int) string {
	prefix := strings.Repeat(" ", spaces)
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}

func artifactWriteCommand(content string, fail bool) string {
	var commands []string
	if runtime.GOOS == "windows" {
		commands = append(commands,
			"if (-not $env:DAG_RUN_ARTIFACTS_DIR) { throw 'DAG_RUN_ARTIFACTS_DIR not set' }",
			"$reportsDir = Join-Path $env:DAG_RUN_ARTIFACTS_DIR 'reports'",
			"New-Item -ItemType Directory -Path $reportsDir -Force | Out-Null",
			fmt.Sprintf("[System.IO.File]::WriteAllText((Join-Path $reportsDir 'summary.md'), %s)", test.PowerShellQuote(content)),
		)
	} else {
		commands = append(commands,
			`test -n "${DAG_RUN_ARTIFACTS_DIR}"`,
			`mkdir -p "${DAG_RUN_ARTIFACTS_DIR}/reports"`,
			fmt.Sprintf(`printf '%%s\n' %s > "$DAG_RUN_ARTIFACTS_DIR/reports/summary.md"`, test.PosixQuote(content)),
		)
	}
	if fail {
		commands = append(commands, "exit 1")
	} else if runtime.GOOS == "windows" {
		commands = append(commands, "exit 0")
	}
	return test.JoinLines(commands...)
}

func largeArtifactWriteCommand(size int) string {
	if runtime.GOOS == "windows" {
		return test.JoinLines(
			"if (-not $env:DAG_RUN_ARTIFACTS_DIR) { throw 'DAG_RUN_ARTIFACTS_DIR not set' }",
			"$reportsDir = Join-Path $env:DAG_RUN_ARTIFACTS_DIR 'reports'",
			"New-Item -ItemType Directory -Path $reportsDir -Force | Out-Null",
			"$artifactFile = Join-Path $reportsDir 'large.txt'",
			"$stream = [System.IO.File]::Create($artifactFile)",
			fmt.Sprintf("try { $stream.SetLength(%d) } finally { $stream.Dispose() }", size),
		)
	}
	return test.JoinLines(
		`test -n "${DAG_RUN_ARTIFACTS_DIR}"`,
		`mkdir -p "${DAG_RUN_ARTIFACTS_DIR}/reports"`,
		fmt.Sprintf(`head -c %d /dev/zero > "${DAG_RUN_ARTIFACTS_DIR}/reports/large.txt"`, size),
	)
}

type artifactUploadGate struct {
	coordinator.Client
	firstChunk     chan struct{}
	release        chan struct{}
	firstChunkOnce sync.Once
	releaseOnce    sync.Once
	streamCount    atomic.Int32
}

func newArtifactUploadGate(client coordinator.Client) *artifactUploadGate {
	return &artifactUploadGate{
		Client:     client,
		firstChunk: make(chan struct{}),
		release:    make(chan struct{}),
	}
}

func (g *artifactUploadGate) StreamArtifacts(ctx context.Context) (coordinatorv1.CoordinatorService_StreamArtifactsClient, error) {
	stream, err := g.Client.StreamArtifacts(ctx)
	return g.wrap(stream, err)
}

func (g *artifactUploadGate) StreamArtifactsTo(ctx context.Context, owner serviceregistry.HostInfo) (coordinatorv1.CoordinatorService_StreamArtifactsClient, error) {
	stream, err := g.Client.StreamArtifactsTo(ctx, owner)
	return g.wrap(stream, err)
}

func (g *artifactUploadGate) wrap(
	stream coordinatorv1.CoordinatorService_StreamArtifactsClient,
	err error,
) (coordinatorv1.CoordinatorService_StreamArtifactsClient, error) {
	if err != nil {
		return nil, err
	}
	g.streamCount.Add(1)
	return &gatedArtifactStream{CoordinatorService_StreamArtifactsClient: stream, gate: g}, nil
}

func (g *artifactUploadGate) releaseUpload() {
	g.releaseOnce.Do(func() { close(g.release) })
}

type gatedArtifactStream struct {
	coordinatorv1.CoordinatorService_StreamArtifactsClient
	gate *artifactUploadGate
}

func (s *gatedArtifactStream) Send(chunk *coordinatorv1.ArtifactChunk) error {
	if err := s.CoordinatorService_StreamArtifactsClient.Send(chunk); err != nil {
		return err
	}
	if len(chunk.Data) > 0 {
		// The callback holds all artifact sends at the first chunk until the coordinator has restarted.
		s.gate.firstChunkOnce.Do(func() {
			close(s.gate.firstChunk)
			<-s.gate.release
		})
	}
	return nil
}

func gatedLogCommand(stdoutMarker, stderrMarker, releasePath string) string {
	if runtime.GOOS == "windows" {
		return test.JoinLines(
			fmt.Sprintf("[Console]::Out.WriteLine(%s)", test.PowerShellQuote(stdoutMarker)),
			"[Console]::Out.Flush()",
			fmt.Sprintf("[Console]::Error.WriteLine(%s)", test.PowerShellQuote(stderrMarker)),
			"[Console]::Error.Flush()",
			fmt.Sprintf("while (-not (Test-Path -LiteralPath %s)) { Start-Sleep -Milliseconds 100 }", test.PowerShellQuote(releasePath)),
		)
	}
	return test.JoinLines(
		fmt.Sprintf("printf '%%s\\n' %s", test.PosixQuote(stdoutMarker)),
		fmt.Sprintf("printf '%%s\\n' %s >&2", test.PosixQuote(stderrMarker)),
		fmt.Sprintf("while [ ! -f %s ]; do sleep 0.1; done", test.PosixQuote(releasePath)),
	)
}

func artifactNoWriteCommand() string {
	if runtime.GOOS == "windows" {
		return test.JoinLines(
			"if (-not $env:DAG_RUN_ARTIFACTS_DIR) { throw 'DAG_RUN_ARTIFACTS_DIR not set' }",
			"Write-Output 'no artifacts written'",
		)
	}
	return test.JoinLines(
		`test -n "${DAG_RUN_ARTIFACTS_DIR}"`,
		`echo "no artifacts written"`,
	)
}

func TestExecution_StatusPushing(t *testing.T) {
	t.Run("statusUpdatesPersistedToCoordinatorStore", func(t *testing.T) {
		f := newTestFixture(t, `
type: graph
name: status-push-test
worker_selector:
  test: "true"
steps:
  - name: step1
    run: echo "step1"
  - name: step2
    run: echo "step2"
    depends: [step1]
`)
		defer f.cleanup()

		require.NoError(t, f.enqueue())
		f.waitForQueued()
		f.startScheduler(30 * time.Second)

		status := f.waitForStatus(ir.Succeeded, executionStatusTimeout())

		require.Equal(t, ir.Succeeded, status.Status)
		require.Len(t, status.Nodes, 2)
		f.assertWorkerID(status, "worker-1")
		f.assertAllNodesSucceeded(status)
	})
}

func TestExecution_RemoteProfile(t *testing.T) {
	f := newTestFixture(t, fmt.Sprintf(`
name: shared-nothing-profile
labels:
  - workspace=ops
worker_selector:
  test: "true"
secrets:
  - name: DIRECT_SECRET
    ref: prod/direct
steps:
  - name: verify
    run: |
%s
`, indentYAMLBlock(sharedNothingProfileCommand(), 6)), withIsolatedWorker())
	defer f.cleanup()

	profileStore, err := persiststore.NewProfileStore(f.coord.Backend.Collection(persis.CollectionProfiles))
	require.NoError(t, err)
	secretStore := persisfile.NewSecretStore(f.coord.Context, f.coord.Config, f.coord.Backend.Collection(persis.CollectionSecrets))
	require.NotNil(t, secretStore)
	manager := profilepkg.NewManager(profileStore, secretStore)
	now := time.Now().UTC()

	global, err := profilepkg.NewInherited(profilepkg.GlobalInheritedRef(), profilepkg.InheritedCreateInput{}, now)
	require.NoError(t, err)
	require.NoError(t, profileStore.Create(f.coord.Context, global))
	_, err = manager.SetVariable(f.coord.Context, global, "GLOBAL_ONLY", "global", "test")
	require.NoError(t, err)
	_, err = manager.SetSecret(f.coord.Context, global, "GLOBAL_SECRET", "global-secret", "test")
	require.NoError(t, err)

	workspaceRef, err := profilepkg.WorkspaceInheritedRef("ops")
	require.NoError(t, err)
	workspaceDefaults, err := profilepkg.NewInherited(workspaceRef, profilepkg.InheritedCreateInput{}, now)
	require.NoError(t, err)
	require.NoError(t, profileStore.Create(f.coord.Context, workspaceDefaults))
	_, err = manager.SetVariable(f.coord.Context, workspaceDefaults, "WORKSPACE_ONLY", "workspace", "test")
	require.NoError(t, err)
	_, err = manager.SetVariable(f.coord.Context, workspaceDefaults, "SHARED", "workspace", "test")
	require.NoError(t, err)

	prof, err := profilepkg.New(profilepkg.CreateInput{Name: "prod"}, now)
	require.NoError(t, err)
	require.NoError(t, profileStore.Create(f.coord.Context, prof))
	_, err = manager.SetVariable(f.coord.Context, prof, "SELECTED_ONLY", "selected", "test")
	require.NoError(t, err)
	_, err = manager.SetVariable(f.coord.Context, prof, "SHARED", "selected", "test")
	require.NoError(t, err)
	_, err = manager.SetSecret(f.coord.Context, prof, "SELECTED_SECRET", "selected-secret", "test")
	require.NoError(t, err)

	direct, err := secretpkg.New(secretpkg.CreateInput{
		Workspace: "ops", Ref: "prod/direct", ProviderType: secretpkg.ProviderDaguManaged,
	}, now)
	require.NoError(t, err)
	require.NoError(t, secretStore.Create(f.coord.Context, direct, &secretpkg.WriteValueInput{
		Value: "direct-secret", CreatedBy: "test", CreatedAt: now,
	}))

	require.NoError(t, f.enqueueWithProfile("prod"))
	f.waitForQueued()
	f.startScheduler(30 * time.Second)

	status := f.waitForStatus(ir.Succeeded, executionStatusTimeout())
	require.Equal(t, "prod", status.ProfileName)
	require.ElementsMatch(t, []ir.RuntimeProfileEntry{
		{Key: "GLOBAL_ONLY", Kind: "variable"},
		{Key: "GLOBAL_SECRET", Kind: "secret"},
		{Key: "WORKSPACE_ONLY", Kind: "variable"},
		{Key: "SHARED", Kind: "variable"},
		{Key: "SELECTED_ONLY", Kind: "variable"},
		{Key: "SELECTED_SECRET", Kind: "secret"},
	}, status.ProfileEntries)

	statusJSON, err := json.Marshal(status)
	require.NoError(t, err)
	require.NotContains(t, string(statusJSON), "global-secret")
	require.NotContains(t, string(statusJSON), "selected-secret")
	require.NotContains(t, string(statusJSON), "direct-secret")
}

func sharedNothingProfileCommand() string {
	if runtime.GOOS == "windows" {
		return test.JoinLines(
			"if ($env:GLOBAL_ONLY -ne 'global') { throw 'bad global default' }",
			"if ($env:GLOBAL_SECRET -ne 'global-secret') { throw 'bad global secret' }",
			"if ($env:WORKSPACE_ONLY -ne 'workspace') { throw 'bad workspace default' }",
			"if ($env:SELECTED_ONLY -ne 'selected') { throw 'bad selected profile' }",
			"if ($env:SELECTED_SECRET -ne 'selected-secret') { throw 'bad selected secret' }",
			"if ($env:SHARED -ne 'selected') { throw 'bad profile precedence' }",
			"if ($env:DIRECT_SECRET -ne 'direct-secret') { throw 'bad direct secret' }",
		)
	}
	return test.JoinLines(
		`test "$GLOBAL_ONLY" = "global"`,
		`test "$GLOBAL_SECRET" = "global-secret"`,
		`test "$WORKSPACE_ONLY" = "workspace"`,
		`test "$SELECTED_ONLY" = "selected"`,
		`test "$SELECTED_SECRET" = "selected-secret"`,
		`test "$SHARED" = "selected"`,
		`test "$DIRECT_SECRET" = "direct-secret"`,
	)
}

func TestExecution_LocalChild(t *testing.T) {
	f := newTestFixture(t, `
name: shared-nothing-parent
labels:
  - workspace=ops
worker_selector:
  test: "true"
steps:
  - name: child
    action: dag.run
    with:
      dag: shared-nothing-external
`, withIsolatedWorker(), withConfigMutator(func(c *config.Config) {
		c.DefaultExecMode = config.ExecutionModeDistributed
	}))
	defer f.cleanup()

	f.coord.CreateDAGFile(t, f.coord.Config.Paths.DAGsDir, "shared-nothing-external", []byte(fmt.Sprintf(`
name: shared-nothing-external
labels:
  - workspace=child-ops
worker_selector: local
steps:
  - name: child
    action: dag.run
    with:
      dag: shared-nothing-child

---
name: shared-nothing-child
labels:
  - workspace=child-ops
worker_selector: local
secrets:
  - name: CHILD_SECRET
    ref: prod/child
steps:
  - name: verify
    run: |
%s
`, indentYAMLBlock(sharedNothingChildCommand(), 6))))

	profileStore, err := persiststore.NewProfileStore(f.coord.Backend.Collection(persis.CollectionProfiles))
	require.NoError(t, err)
	prof, err := profilepkg.New(profilepkg.CreateInput{Name: "prod"}, time.Now().UTC())
	require.NoError(t, err)
	require.NoError(t, prof.SetVariable("CHILD_PROFILE", "from-coordinator", "test", time.Now().UTC()))
	require.NoError(t, profileStore.Create(f.coord.Context, prof))

	secretStore := persisfile.NewSecretStore(f.coord.Context, f.coord.Config, f.coord.Backend.Collection(persis.CollectionSecrets))
	require.NotNil(t, secretStore)
	now := time.Now().UTC()
	childSecret, err := secretpkg.New(secretpkg.CreateInput{
		Workspace: "child-ops", Ref: "prod/child", ProviderType: secretpkg.ProviderDaguManaged,
	}, now)
	require.NoError(t, err)
	require.NoError(t, secretStore.Create(f.coord.Context, childSecret, &secretpkg.WriteValueInput{
		Value: "child-secret", CreatedBy: "test", CreatedAt: now,
	}))

	require.NoError(t, f.enqueueWithProfile("prod"))
	f.waitForQueued()
	f.startScheduler(30 * time.Second)

	status := f.waitForStatus(ir.Succeeded, executionStatusTimeout())
	require.Len(t, status.Nodes, 1)
	require.Len(t, status.Nodes[0].SubRuns, 1)
	require.Equal(t, ir.NodeSucceeded, status.Nodes[0].Status)
}

func sharedNothingChildCommand() string {
	if runtime.GOOS == "windows" {
		return test.JoinLines(
			"if ($env:CHILD_PROFILE -ne 'from-coordinator') { throw 'bad child profile' }",
			"if ($env:CHILD_SECRET -ne 'child-secret') { throw 'bad child secret' }",
		)
	}
	return test.JoinLines(
		`test "$CHILD_PROFILE" = "from-coordinator"`,
		`test "$CHILD_SECRET" = "child-secret"`,
	)
}

func TestExecution_LogStreaming(t *testing.T) {
	t.Run("logsStreamedToCoordinatorFilesystem", func(t *testing.T) {
		expectedOutput := "EXPECTED_OUTPUT_12345"
		f := newTestFixture(t, `
name: log-stream-test
worker_selector:
  test: "true"
steps:
  - name: echo-step
    run: echo "`+expectedOutput+`"
`, withLogPersistence())
		defer f.cleanup()

		require.NoError(t, f.enqueue())
		f.waitForQueued()
		f.startScheduler(30 * time.Second)

		status := f.waitForStatus(ir.Succeeded, executionStatusTimeout())

		require.Equal(t, ir.Succeeded, status.Status)
		assertLogContains(t, f.logDir(), f.dagWrapper.Name, status.DAGRunID, "echo-step", expectedOutput)
	})
}

func TestExecution_DistributedWorkerLogsVisibleFromCoordinator(t *testing.T) {
	firstStdout := "distributed-first-stdout"
	firstStderr := "distributed-first-stderr"
	secondStdout := "distributed-second-stdout"
	secondStderr := "distributed-second-stderr"

	f := newTestFixture(t, `
name: distributed-worker-visible-logs-test
worker_selector:
  test: "true"
steps:
  - name: first
`+logStepShellYAML()+`    command: |
`+indentYAMLBlock(logOutputCommands(firstStdout, firstStderr), 6)+`
  - name: second
`+logStepShellYAML()+`    command: |
`+indentYAMLBlock(logOutputCommands(secondStdout, secondStderr), 6)+`
    depends: [first]
`, withLogPersistence())
	defer f.cleanup()

	require.NoError(t, f.enqueue())
	f.waitForQueued()
	f.startScheduler(30 * time.Second)

	status := f.waitForStatus(ir.Succeeded, executionStatusTimeout())

	require.Equal(t, ir.Succeeded, status.Status)
	f.assertAllNodesSucceeded(status)

	expectedStepLogs := []struct {
		stepName string
		suffix   string
		lines    []string
	}{
		{stepName: "first", suffix: "stdout", lines: []string{firstStdout}},
		{stepName: "first", suffix: "stderr", lines: []string{firstStderr}},
		{stepName: "second", suffix: "stdout", lines: []string{secondStdout}},
		{stepName: "second", suffix: "stderr", lines: []string{secondStderr}},
	}
	for _, expected := range expectedStepLogs {
		matches := findLogFiles(t, f.logDir(), f.dagWrapper.Name, status.DAGRunID, expected.stepName, expected.suffix)
		require.NotEmpty(t, matches, "no %s log file found for step %s", expected.suffix, expected.stepName)
		content := getLogContent(t, matches[0])
		for _, line := range expected.lines {
			assert.Contains(t, content, line, "%s log file for step %s should contain expected content", expected.suffix, expected.stepName)
		}
	}

	require.NotEmpty(t, status.Log, "run-level scheduler log path should be persisted")
	require.FileExists(t, status.Log, "run-level scheduler log should exist on the coordinator")
	runLog := getLogContent(t, status.Log)
	for _, line := range []string{firstStdout, firstStderr, secondStdout, secondStderr} {
		assert.Contains(t, runLog, line, "run-level scheduler log should contain distributed step output evidence")
	}
}

func TestExecution_SmallDistributedWorkerLogsVisibleBeforeStepCompletes(t *testing.T) {
	stdoutMarker := "small-running-stdout-marker"
	stderrMarker := "small-running-stderr-marker"
	releasePath := filepath.Join(t.TempDir(), "release")
	releaseStep := func() error {
		return os.WriteFile(releasePath, []byte("release"), 0600)
	}

	f := newTestFixture(t, `
name: small-running-distributed-logs-test
worker_selector:
  test: "true"
steps:
  - name: gated-step
`+artifactStepShellYAML()+`    command: |
`+indentYAMLBlock(gatedLogCommand(stdoutMarker, stderrMarker, releasePath), 6)+`
`, withLogPersistence())
	defer f.cleanup()
	defer func() { _ = releaseStep() }()

	require.NoError(t, f.enqueue())
	f.waitForQueued()
	f.startScheduler(30 * time.Second)

	running := f.waitForStatus(ir.Running, executionStatusTimeout())
	require.Equal(t, ir.Running, running.Status)
	require.NotEmpty(t, running.Log)

	f.requireEventuallyNoSchedulerError(
		"small step logs should be visible on the coordinator while the step is running",
		executionStatusTimeout(),
		100*time.Millisecond,
		func() bool {
			current, err := f.latestStoredStatus()
			if err != nil || current.Status != ir.Running {
				return false
			}

			stdoutFiles := findLogFiles(t, f.logDir(), f.dagWrapper.Name, current.DAGRunID, "gated-step", "stdout")
			stderrFiles := findLogFiles(t, f.logDir(), f.dagWrapper.Name, current.DAGRunID, "gated-step", "stderr")
			if len(stdoutFiles) == 0 || len(stderrFiles) == 0 {
				return false
			}

			stdout, err := os.ReadFile(stdoutFiles[0])
			if err != nil || !strings.Contains(string(stdout), stdoutMarker) {
				return false
			}
			stderr, err := os.ReadFile(stderrFiles[0])
			if err != nil || !strings.Contains(string(stderr), stderrMarker) {
				return false
			}
			combined, err := os.ReadFile(current.Log)
			return err == nil &&
				strings.Contains(string(combined), stdoutMarker) &&
				strings.Contains(string(combined), stderrMarker)
		},
	)

	require.NoError(t, releaseStep())
	status := f.waitForStatus(ir.Succeeded, executionStatusTimeout())
	f.assertAllNodesSucceeded(status)

	stdoutFiles := findLogFiles(t, f.logDir(), f.dagWrapper.Name, status.DAGRunID, "gated-step", "stdout")
	stderrFiles := findLogFiles(t, f.logDir(), f.dagWrapper.Name, status.DAGRunID, "gated-step", "stderr")
	require.NotEmpty(t, stdoutFiles)
	require.NotEmpty(t, stderrFiles)
	require.Equal(t, 1, strings.Count(getLogContent(t, stdoutFiles[0]), stdoutMarker))
	require.Equal(t, 1, strings.Count(getLogContent(t, stderrFiles[0]), stderrMarker))

	combined := getLogContent(t, status.Log)
	require.Equal(t, 1, strings.Count(combined, stdoutMarker))
	require.Equal(t, 1, strings.Count(combined, stderrMarker))
}

func TestExecution_LargeOutput(t *testing.T) {
	t.Run("largeOutputStreamedCorrectly", func(t *testing.T) {
		command := `      for i in $(seq 1 2000); do
        echo "Line $i: This is a test line to generate large output that exceeds the 64KB buffer size used in log streaming"
      done`
		if runtime.GOOS == "windows" {
			command = `      1..2000 | ForEach-Object {
        Write-Output ("Line {0}: This is a test line to generate large output that exceeds the 64KB buffer size used in log streaming" -f $_)
      }`
		}

		f := newTestFixture(t, `
name: large-output-test
worker_selector:
  test: "true"
steps:
  - name: big-output
    run: |
`+command+`
`, withLogPersistence())
		defer f.cleanup()

		require.NoError(t, f.enqueue())
		f.waitForQueued()
		f.startScheduler(60 * time.Second)

		status := f.waitForStatus(ir.Succeeded, 45*time.Second)

		require.Equal(t, ir.Succeeded, status.Status)

		logPath := assertLogExists(t, f.logDir(), f.dagWrapper.Name, status.DAGRunID, "big-output")

		fileInfo, err := os.Stat(logPath)
		require.NoError(t, err)
		assert.Greater(t, fileInfo.Size(), int64(64*1024), "log file should exceed 64KB")

		content := getLogContent(t, logPath)
		assert.Contains(t, content, "Line 1:")
		assert.Contains(t, content, "Line 2000:")

		lineCount := strings.Count(content, "\n")
		assert.GreaterOrEqual(t, lineCount, 2000, "should have at least 2000 lines")
	})
}

func TestExecution_Artifacts(t *testing.T) {
	t.Run("workerUploadsArtifactsToCoordinatorFilesystem", func(t *testing.T) {
		f := newTestFixture(t, `
name: worker-artifact-test
worker_selector:
  test: "true"
artifacts:
  enabled: true
steps:
  - name: write-artifacts
`+artifactStepShellYAML()+`    command: |
`+indentYAMLBlock(artifactWriteCommand("artifact from worker", false), 6)+`
`, withArtifactPersistence())
		defer f.cleanup()

		require.NoError(t, f.enqueue())
		f.waitForQueued()
		f.startScheduler(30 * time.Second)

		status := f.waitForStatus(ir.Succeeded, artifactExecutionStatusTimeout())

		require.Equal(t, ir.Succeeded, status.Status)
		require.NotEmpty(t, status.ArchiveDir)
		require.DirExists(t, status.ArchiveDir)
		assert.True(t, strings.HasPrefix(status.ArchiveDir, filepath.Join(f.artifactDir(), f.dagWrapper.Name)+string(os.PathSeparator)))
		assertArtifactContains(t, status.ArchiveDir, "reports/summary.md", "artifact from worker")
	})

	t.Run("workerFailedRunsStillUploadArtifactsToCoordinatorFilesystem", func(t *testing.T) {
		f := newTestFixture(t, `
name: worker-failed-artifact-test
worker_selector:
  test: "true"
artifacts:
  enabled: true
steps:
  - name: write-artifacts-and-fail
`+artifactStepShellYAML()+`    command: |
`+indentYAMLBlock(artifactWriteCommand("artifact from failed worker", true), 6)+`
`, withArtifactPersistence())
		defer f.cleanup()

		require.NoError(t, f.enqueue())
		f.waitForQueued()
		f.startScheduler(30 * time.Second)

		status := f.waitForStatus(ir.Failed, artifactExecutionStatusTimeout())

		require.Equal(t, ir.Failed, status.Status)
		require.NotEmpty(t, status.ArchiveDir)
		require.DirExists(t, status.ArchiveDir)
		assert.True(t, strings.HasPrefix(status.ArchiveDir, filepath.Join(f.artifactDir(), f.dagWrapper.Name)+string(os.PathSeparator)))
		assertArtifactContains(t, status.ArchiveDir, "reports/summary.md", "artifact from failed worker")
	})

	t.Run("workerCreatesEmptyArtifactDirectoryWhenNoFilesAreWritten", func(t *testing.T) {
		f := newTestFixture(t, `
name: worker-empty-artifact-test
worker_selector:
  test: "true"
artifacts:
  enabled: true
steps:
  - name: no-artifacts-written
`+artifactStepShellYAML()+`    command: |
`+indentYAMLBlock(artifactNoWriteCommand(), 6)+`
`, withArtifactPersistence())
		defer f.cleanup()

		require.NoError(t, f.enqueue())
		f.waitForQueued()
		f.startScheduler(30 * time.Second)

		status := f.waitForStatus(ir.Succeeded, artifactExecutionStatusTimeout())

		require.Equal(t, ir.Succeeded, status.Status)
		require.NotEmpty(t, status.ArchiveDir)
		require.DirExists(t, status.ArchiveDir)
		assert.True(t, strings.HasPrefix(status.ArchiveDir, filepath.Join(f.artifactDir(), f.dagWrapper.Name)+string(os.PathSeparator)))

		entries, err := os.ReadDir(status.ArchiveDir)
		require.NoError(t, err)
		assert.Empty(t, entries)
	})

	t.Run("workerResumesArtifactUploadAfterCoordinatorReplacement", func(t *testing.T) {
		const artifactSize = 4*1024*1024 + 1
		f := newTestFixture(t, `
name: coordinator-restart-artifact-test
worker_selector:
  test: "true"
artifacts:
  enabled: true
steps:
  - name: write-large-artifact
`+artifactStepShellYAML()+`    command: |
`+indentYAMLBlock(largeArtifactWriteCommand(artifactSize), 6)+`
`, withArtifactPersistence(), withWorkerCount(0))
		defer f.cleanup()

		gate := newArtifactUploadGate(f.coordinatorClient)
		defer gate.releaseUpload()
		f.coordinatorClient = gate
		f.workers = append(f.workers, f.setupWorker("worker-1", map[string]string{"test": "true"}, ""))

		require.NoError(t, f.enqueue())
		f.waitForQueued()
		f.startScheduler(30 * time.Second)

		select {
		case <-gate.firstChunk:
		case <-time.After(distrTestTimeout(artifactExecutionStatusTimeout())):
			t.Fatal("artifact upload did not send its first chunk")
		}
		peer := f.coord.StartPeer(t)
		require.NotEqual(t, f.coord.Address(), peer.Address())
		require.NoError(t, f.coord.Stop())
		gate.releaseUpload()

		status := f.waitForStatus(ir.Succeeded, artifactExecutionStatusTimeout())
		require.GreaterOrEqual(t, gate.streamCount.Load(), int32(2))
		artifactPath := filepath.Join(status.ArchiveDir, "reports", "large.txt")
		info, err := os.Stat(artifactPath)
		require.NoError(t, err)
		assert.Equal(t, int64(artifactSize), info.Size())
	})

	t.Run("coordinatorRejectsStaleAttemptArtifactChunks", func(t *testing.T) {
		f := newTestFixture(t, `
name: stale-attempt-artifact-test
worker_selector:
  test: "true"
artifacts:
  enabled: true
steps:
  - name: write-artifacts
`+artifactStepShellYAML()+`    command: |
`+indentYAMLBlock(artifactWriteCommand("artifact from latest attempt", false), 6)+`
`, withArtifactPersistence())
		defer f.cleanup()

		require.NoError(t, f.enqueue())
		f.waitForQueued()
		f.startScheduler(30 * time.Second)

		status := f.waitForStatus(ir.Succeeded, artifactExecutionStatusTimeout())
		require.Equal(t, ir.Succeeded, status.Status)

		stream, err := f.coordinatorClient.StreamArtifacts(f.coord.Context)
		require.NoError(t, err)
		require.NoError(t, stream.Send(&coordinatorv1.ArtifactChunk{
			WorkerId:           "stale-worker",
			DagRunId:           status.DAGRunID,
			DagName:            f.dagWrapper.Name,
			AttemptId:          "stale-attempt",
			OwnerCoordinatorId: "test-coordinator",
			RelativePath:       "reports/stale.txt",
			Data:               []byte("stale artifact"),
		}))

		_, err = stream.CloseAndRecv()
		require.Error(t, err)
		assert.Equal(t, codes.FailedPrecondition, grpcstatus.Code(err))

		_, statErr := os.Stat(filepath.Join(status.ArchiveDir, "reports", "stale.txt"))
		require.Error(t, statErr)
		assert.True(t, os.IsNotExist(statErr))
	})
}

func TestExecution_StartCommand(t *testing.T) {
	t.Run("directStartCommandExecution", func(t *testing.T) {
		f := newTestFixture(t, `
type: graph
name: direct-start-test
worker_selector:
  test: "true"
steps:
  - name: step1
    run: echo "step1 output"
  - name: step2
    run: echo "step2 output"
    depends: [step1]
`)
		defer f.cleanup()

		f.startScheduler(30 * time.Second)

		require.NoError(t, f.start())

		status := f.waitForStatus(ir.Succeeded, directStartStatusTimeout())

		require.Equal(t, ir.Succeeded, status.Status)
		require.Len(t, status.Nodes, 2)
		f.assertAllNodesSucceeded(status)
	})

	t.Run("directStartCommandExecution_NoNameField", func(t *testing.T) {
		f := newTestFixture(t, `
type: graph
worker_selector:
  test: "true"
steps:
  - name: step1
    run: echo "no name field"
  - name: step2
    run: echo "step2 output"
    depends: [step1]
`)
		defer f.cleanup()

		f.startScheduler(30 * time.Second)

		require.NoError(t, f.start())

		status := f.waitForStatus(ir.Succeeded, directStartStatusTimeout())

		require.Equal(t, ir.Succeeded, status.Status)
		require.Len(t, status.Nodes, 2)
		f.assertAllNodesSucceeded(status)
	})
}

func TestExecution_LabelsPropagation(t *testing.T) {
	t.Run("labelsPreservedThroughCoordinator", func(t *testing.T) {
		f := newTestFixture(t, `
type: graph
name: labels-propagation-test
worker_selector:
  test: "true"
steps:
  - name: step1
    run: echo "tagged run"
`)
		defer f.cleanup()

		f.startScheduler(30 * time.Second)

		require.NoError(t, f.startWithLabels("env=prod,team=backend"))

		status := f.waitForStatus(ir.Succeeded, 20*time.Second)

		require.Equal(t, ir.Succeeded, status.Status)
		require.Contains(t, status.Labels, "env=prod")
		require.Contains(t, status.Labels, "team=backend")
	})

}

func TestExecution_WorkDir(t *testing.T) {
	t.Run("workerWorkDir", func(t *testing.T) {
		f := newTestFixture(t, `
type: graph
name: workdir-worker-test
worker_selector:
  test: "true"
steps:
  - name: write-to-workdir
    run: echo "hello" > "${DAG_RUN_WORK_DIR}/test.txt"
  - name: read-from-workdir
    run: cat "${DAG_RUN_WORK_DIR}/test.txt"
    depends: [write-to-workdir]
`, withLogPersistence())
		defer f.cleanup()

		require.NoError(t, f.enqueue())
		f.waitForQueued()
		f.startScheduler(30 * time.Second)

		status := f.waitForStatus(ir.Succeeded, 20*time.Second)

		require.Equal(t, ir.Succeeded, status.Status)
		f.assertAllNodesSucceeded(status)
		assertLogContains(t, f.logDir(), f.dagWrapper.Name, status.DAGRunID, "read-from-workdir", "hello")
	})

}

func TestExecution_FileDependencies(t *testing.T) {
	const (
		dependencyPath    = "fixtures/message.txt"
		dependencyContent = "dependency from coordinator"
	)
	defaultRoot := t.TempDir()
	dependencyRoot := t.TempDir()

	f := newTestFixture(t, fmt.Sprintf(`
type: graph
name: file-dependency-worker-test
params:
  DEPENDENCY_ROOT: %q
working_dir: ${params.DEPENDENCY_ROOT}
worker_selector:
  test: "true"
steps:
  - name: read-dependency
    dependencies:
      - `+dependencyPath+`
    action: file.read
    with:
      path: `+dependencyPath+`
    output: CONTENT
`, defaultRoot), withWorkerCount(0))
	defer f.cleanup()

	sourceDependency := filepath.Join(dependencyRoot, filepath.FromSlash(dependencyPath))
	require.NoError(t, os.MkdirAll(filepath.Dir(sourceDependency), 0o750))
	require.NoError(t, os.WriteFile(sourceDependency, []byte(dependencyContent), 0o600))

	taskClaimed := make(chan struct{})
	releaseTask := make(chan struct{})
	var claimOnce sync.Once
	afterTaskAck := func(ctx context.Context, _ *coordinatorv1.Task) bool {
		claimOnce.Do(func() { close(taskClaimed) })
		select {
		case <-releaseTask:
			return false
		case <-ctx.Done():
			return true
		}
	}
	require.NoError(t, f.enqueueWithParams("DEPENDENCY_ROOT="+dependencyRoot))
	f.waitForQueued()
	f.startScheduler(30 * time.Second)
	f.requireEventuallyNoSchedulerError(
		"DAG should remain queued while no worker is available",
		executionStatusTimeout(),
		100*time.Millisecond,
		func() bool {
			status, err := f.latestStoredStatus()
			if err != nil || status.Status != ir.Queued {
				return false
			}
			for _, condition := range status.Conditions {
				if condition.Type == "WorkerReady" && condition.Status == "False" && condition.Reason == "NoAvailableWorker" {
					return true
				}
			}
			return false
		},
	)

	f.workers = append(f.workers, f.setupWorkerWithAfterAckHook(
		"worker-1",
		map[string]string{"test": "true"},
		"",
		afterTaskAck,
	))

	select {
	case <-taskClaimed:
	case <-time.After(distrTestTimeout(executionStatusTimeout())):
		t.Fatal("worker did not claim the dependency task")
	}
	require.NoError(t, os.Remove(sourceDependency))
	close(releaseTask)

	status := f.waitForStatus(ir.Succeeded, executionStatusTimeout())
	f.assertWorkerID(status, "worker-1")
	f.assertAllNodesSucceeded(status)
	require.Len(t, status.Nodes, 1)
	assert.Equal(t, dependencyContent, nodeOutputValue(t, status.Nodes[0], "CONTENT"))
}

func TestExecution_QueueLifecycle(t *testing.T) {
	t.Run("queueItemRemovedAfterSuccess", func(t *testing.T) {
		f := newTestFixture(t, `
name: queue-cleanup-test
worker_selector:
  test: "true"
steps:
  - name: task1
    run: echo "done"
`)
		defer f.cleanup()

		require.NoError(t, f.enqueue())
		f.waitForQueued()
		f.startScheduler(30 * time.Second)

		require.Eventually(t, func() bool {
			status, err := f.latestStatus()
			if err != nil || status.Status != ir.Succeeded {
				return false
			}

			items, err := f.coord.QueueStore.ListByDAGName(f.coord.Context, f.dagWrapper.ProcGroup(), f.dagWrapper.Name)
			return err == nil && len(items) == 0
		}, distrTestTimeout(25*time.Second), 200*time.Millisecond, "Queue should be empty after success")
	})

	t.Run("queuedStatusBeforeSchedulerStarts", func(t *testing.T) {
		f := newTestFixture(t, `
type: graph
name: scheduler-process-test
worker_selector:
  env: prod
steps:
  - name: step1
    run: echo "step1"
  - name: step2
    run: echo "step2"
    depends: [step1]
`, withLabels(map[string]string{"env": "prod"}))
		defer f.cleanup()

		require.NoError(t, f.enqueue())
		f.waitForQueued()

		latest, err := f.latestStatus()
		require.NoError(t, err)
		require.Equal(t, ir.Queued, latest.Status, "DAG should be in queued state before scheduler starts")

		f.startScheduler(30 * time.Second)

		status := f.waitForStatus(ir.Succeeded, 20*time.Second)

		require.Equal(t, ir.Succeeded, status.Status)
		require.Len(t, status.Nodes, 2)
		f.assertAllNodesSucceeded(status)
	})
}

func TestExecution_QueuedCatchupHappyPath(t *testing.T) {
	t.Run("distributedWorkerPreservesCatchupMetadata", func(t *testing.T) {
		scheduleTime := time.Date(2026, 3, 13, 10, 0, 0, 0, time.UTC)
		expectedOutput := "distributed-catchup-remote"

		f := newTestFixture(t, `
name: distributed-catchup-remote-test
worker_selector:
  test: "true"
steps:
  - name: echo-step
    run: echo "`+expectedOutput+`"
`, withLogPersistence())
		defer f.cleanup()

		runID, err := f.enqueueCatchup(scheduleTime)
		require.NoError(t, err)

		f.waitForQueued()
		f.startScheduler(30 * time.Second)

		status := f.waitForStatus(ir.Succeeded, 20*time.Second)

		require.Equal(t, runID, status.DAGRunID)
		require.Equal(t, ir.TriggerTypeCatchUp, status.TriggerType)
		require.Equal(t, stringutil.FormatTime(scheduleTime), status.ScheduleTime)
		require.NotEmpty(t, status.Log)
		f.assertWorkerID(status, "worker-1")
		f.assertAllNodesSucceeded(status)
		assertLogContains(t, f.logDir(), f.dagWrapper.Name, status.DAGRunID, "echo-step", expectedOutput)
	})

}
