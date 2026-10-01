// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package cmd_test

import (
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/require"
)

// Supervisors must remain alive until signaled runners persist terminal state.
func TestSignalPropagation(t *testing.T) {
	for _, commandName := range []string{"server", "scheduler", "start-all"} {
		for _, shutdownSignal := range []os.Signal{syscall.SIGTERM, syscall.SIGINT} {
			t.Run(commandName+"/"+shutdownSignal.String(), func(t *testing.T) {
				run := startSignalRun(t, commandName, 10)
				require.NoError(t, run.command.Process.Signal(shutdownSignal))
				run.waitForFile(t, run.stopped)
				run.assertAlive(t, 200*time.Millisecond)
				releaseHoldFile(t, run.release)
				require.NoError(t, run.wait(t))
				_, err := os.Stat(run.cleaned)
				require.NoError(t, err, "supervisor exited before step cleanup")
				data, err := os.ReadFile(run.stopped)
				require.NoError(t, err)
				want := "TERM"
				if shutdownSignal == syscall.SIGINT {
					want = "INT"
				}
				require.Equal(t, want, string(data))
				run.assertStatus(t, ir.Aborted)
			})
		}
	}
}

// A supervisor must finish shutdown while a runner's startup secret stays blocked.
func TestSignalDuringStartup(t *testing.T) {
	for _, shutdownSignal := range []os.Signal{syscall.SIGTERM, syscall.SIGINT} {
		t.Run(shutdownSignal.String(), func(t *testing.T) {
			th := test.SetupCommand(t, test.WithBuiltExecutable())
			dir := t.TempDir()
			fifo := filepath.Join(dir, "secret")
			require.NoError(t, syscall.Mkfifo(fifo, 0600))
			secretFile, err := os.OpenFile(fifo, os.O_RDWR, 0600)
			require.NoError(t, err)
			marker := filepath.Join(dir, "executed")
			dag := th.DAG(t, fmt.Sprintf(`
max_clean_up_time_sec: 1
secrets:
  - name: TOKEN
    provider: file
    key: %q
steps:
  - script: %q
`, fifo, "printf done > "+test.PosixQuote(marker)))
			logFile, err := os.Create(filepath.Join(dir, "supervisor.log"))
			require.NoError(t, err)
			t.Cleanup(func() { _ = logFile.Close() })
			command := exec.Command(th.Config.Paths.Executable, test.WithConfigFlag([]string{"scheduler"}, th.Config)...) //nolint:gosec // Test executes the repository binary.
			command.Env = append(th.ChildEnv, "DAGU_SIGNAL_PROPAGATION=true", "GOMAXPROCS=1")
			command.Stdout = logFile
			command.Stderr = logFile
			require.NoError(t, command.Start())
			done := make(chan error, 1)
			go func() { done <- command.Wait() }()
			exited := false
			t.Cleanup(func() {
				_, _ = secretFile.Write([]byte("fixture"))
				_ = secretFile.Close()
				if !exited {
					select {
					case <-done:
					case <-time.After(5 * time.Second):
						terminateTestCommand(command, done)
					}
				}
			})
			require.Eventually(t, func() bool {
				data, _ := os.ReadFile(logFile.Name())
				return strings.Contains(string(data), "Scheduler started")
			}, commandLogWaitTimeout(), 20*time.Millisecond)
			enqueue := exec.Command(th.Config.Paths.Executable, test.WithConfigFlag([]string{"enqueue", dag.Location}, th.Config)...) //nolint:gosec // Test executes the repository binary.
			enqueue.Env = command.Env
			require.NoError(t, enqueue.Run())
			require.Eventually(t, func() bool {
				found := false
				_ = filepath.WalkDir(th.Config.Paths.LogDir, func(path string, entry fs.DirEntry, err error) error {
					if err == nil && !entry.IsDir() && strings.HasSuffix(path, ".log") {
						data, _ := os.ReadFile(path)
						found = found || strings.Contains(string(data), "Resolving secrets")
					}
					return nil
				})
				return found
			}, commandLogWaitTimeout(), 20*time.Millisecond)
			require.NoError(t, command.Process.Signal(shutdownSignal))
			select {
			case err := <-done:
				exited = true
				require.NoError(t, err)
			case <-time.After(3 * time.Second):
				t.Fatal("supervisor waited for blocked startup beyond the cleanup deadline")
			}
			dag.AssertLatestStatus(t, ir.Aborted)
			_, err = os.Stat(marker)
			require.ErrorIs(t, err, os.ErrNotExist, "step started after shutdown")
		})
	}
}

// Runner cleanup can outlast start-all's separate service shutdown budget.
func TestSignalCleanupBeyondServiceTimeout(t *testing.T) {
	run := startSignalRun(t, "start-all", 50)
	require.NoError(t, run.command.Process.Signal(syscall.SIGTERM))
	run.waitForFile(t, run.stopped)
	run.assertAlive(t, 31*time.Second)
	releaseHoldFile(t, run.release)
	require.NoError(t, run.wait(t))
	_, err := os.Stat(run.cleaned)
	require.NoError(t, err)
	run.assertStatus(t, ir.Aborted)
}

func TestSignalCleanupTimeout(t *testing.T) {
	for _, commandName := range []string{"server", "scheduler", "start-all"} {
		for _, scenario := range []string{"Step", "Repeat"} {
			t.Run(commandName+"/"+scenario, func(t *testing.T) {
				run := startSignalRun(t, commandName, 1, func(yaml string) string {
					switch scenario {
					case "Repeat":
						yaml += "    repeat_policy:\n      repeat: while\n      condition: \"true\"\n      expected: \"true\"\n"
					}
					return yaml
				})
				require.NoError(t, run.command.Process.Signal(syscall.SIGTERM))
				// The run finish time includes handlers. Service shutdown has its
				// own budget and can finish later.
				require.Eventually(t, func() bool {
					status, err := run.th.DAGRunMgr.GetLatestStatus(run.th.Context, run.dag.DAG)
					return err == nil && status.Status == ir.Aborted && status.FinishedAt != ""
				}, 5*time.Second, 20*time.Millisecond, "runner exceeded the cleanup deadline: %s", run.output())
				require.NoError(t, run.wait(t))
				_, err := os.Stat(run.cleaned)
				require.ErrorIs(t, err, os.ErrNotExist)
				run.assertStatus(t, ir.Aborted)
			})
		}
	}
}

func TestSignalHandlerBudgets(t *testing.T) {
	for _, commandName := range []string{"server", "scheduler", "start-all"} {
		t.Run(commandName, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "exited")
			run := startSignalRun(t, commandName, 1, func(yaml string) string {
				return yaml + fmt.Sprintf("handler_on:\n  abort:\n    script: sleep 2\n  exit:\n    script: %q\n", "printf done > "+test.PosixQuote(marker))
			})
			releaseHoldFile(t, run.release)
			require.NoError(t, run.command.Process.Signal(syscall.SIGTERM))
			require.NoError(t, run.wait(t))
			data, err := os.ReadFile(marker)
			require.NoError(t, err, "supervisor exited before handlers: %s", run.output())
			require.Equal(t, "done", string(data))
			status, err := run.th.DAGRunMgr.GetLatestStatus(run.th.Context, run.dag.DAG)
			require.NoError(t, err)
			require.Equal(t, ir.Aborted, status.Status)
			require.Equal(t, ir.NodeSucceeded, status.OnAbort.Status)
			require.Equal(t, ir.NodeSucceeded, status.OnExit.Status)
		})
	}
}

// Lifecycle handlers retain their own execution budgets after a CLI stop.
func TestStopHandlers(t *testing.T) {
	th := test.SetupCommand(t, test.WithBuiltExecutable())
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	aborted := filepath.Join(dir, "aborted")
	exited := filepath.Join(dir, "exited")
	dag := th.DAG(t, fmt.Sprintf(`
steps:
  - script: |
      printf ready > %s
      sleep 60
handler_on:
  abort:
    script: |
      sleep 10
      printf done > %s
  exit:
    script: printf done > %s
`, test.PosixQuote(ready), test.PosixQuote(aborted), test.PosixQuote(exited)))
	command := exec.Command(th.Config.Paths.Executable, test.WithConfigFlag([]string{"start", dag.Location}, th.Config)...) //nolint:gosec // Test executes the repository binary.
	command.Env = append(th.ChildEnv, "DAGU_SIGNAL_PROPAGATION=false")
	logFile, err := os.CreateTemp(dir, "run-*.log")
	require.NoError(t, err)
	t.Cleanup(func() { _ = logFile.Close() })
	command.Stdout, command.Stderr = logFile, logFile
	require.NoError(t, command.Start())
	waitCh := make(chan error, 1)
	go func() { waitCh <- command.Wait() }()
	run := &signalRun{th: th, dag: dag, command: command, waitCh: waitCh, logFile: logFile}
	t.Cleanup(func() {
		if !run.exited {
			terminateTestCommand(command, waitCh)
		}
	})
	run.waitForFile(t, ready)
	stop := exec.Command(th.Config.Paths.Executable, test.WithConfigFlag([]string{"stop", dag.Location}, th.Config)...) //nolint:gosec // Test executes the repository binary.
	stop.Env = command.Env
	output, err := stop.CombinedOutput()
	require.NoError(t, err, "output: %s", output)
	select {
	case err := <-waitCh:
		run.exited = true
		require.NoError(t, err)
	case <-time.After(20 * time.Second):
		t.Fatalf("run did not finish its handlers: %s", run.output())
	}
	for _, marker := range []string{aborted, exited} {
		data, err := os.ReadFile(marker)
		require.NoError(t, err, "handler did not complete: %s", run.output())
		require.Equal(t, "done", string(data))
	}
	status, err := th.DAGRunMgr.GetLatestStatus(th.Context, dag.DAG)
	require.NoError(t, err)
	require.Equal(t, ir.Aborted, status.Status)
	require.Equal(t, ir.NodeSucceeded, status.OnAbort.Status)
	require.Equal(t, ir.NodeSucceeded, status.OnExit.Status)
}

// A cleanup command must survive automatic resends until the DAG deadline,
// including standalone runs without supervisor signal propagation.
func TestSignalCleanupWithoutPropagation(t *testing.T) {
	th := test.SetupCommand(t, test.WithBuiltExecutable())
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	cleaned := filepath.Join(dir, "cleaned")
	dag := th.DAG(t, fmt.Sprintf(`
max_clean_up_time_sec: 20
steps:
  - id: probe
    run: |
      cleanup() {
        trap - TERM INT
        sleep 8 && printf done > %s
        exit 0
      }
      trap cleanup TERM INT
      printf ready > %s
      while :; do sleep 0.05; done
    with:
      shell: /bin/sh
`, test.PosixQuote(cleaned), test.PosixQuote(ready)))
	command := exec.Command(th.Config.Paths.Executable, test.WithConfigFlag([]string{"start", dag.Location}, th.Config)...) //nolint:gosec // Test executes the repository binary.
	command.Env = append(th.ChildEnv, "DAGU_SIGNAL_PROPAGATION=false")
	logFile, err := os.CreateTemp(dir, "run-*.log")
	require.NoError(t, err)
	defer func() { _ = logFile.Close() }()
	command.Stdout = logFile
	command.Stderr = logFile
	require.NoError(t, command.Start())
	waitCh := make(chan error, 1)
	go func() { waitCh <- command.Wait() }()
	run := &signalRun{th: th, dag: dag, command: command, waitCh: waitCh, logFile: logFile}
	t.Cleanup(func() {
		_ = th.DAGRunMgr.Stop(th.Context, dag.DAG, "")
		if !run.exited {
			terminateTestCommand(command, waitCh)
		}
	})
	run.waitForFile(t, ready)
	require.NoError(t, command.Process.Signal(syscall.SIGTERM))
	require.NoError(t, run.wait(t))
	_, err = os.Stat(cleaned)
	require.NoError(t, err, "cleanup was interrupted before its deadline: %s", run.output())
	run.assertStatus(t, ir.Aborted)
}

func TestSecondSignalDuringRunCleanup(t *testing.T) {
	for _, commandName := range []string{"server", "scheduler", "start-all"} {
		t.Run(commandName, func(t *testing.T) {
			run := startSignalRun(t, commandName, 10)
			require.NoError(t, run.command.Process.Signal(syscall.SIGINT))
			run.waitForFile(t, run.stopped)
			run.assertAlive(t, 200*time.Millisecond)
			require.NoError(t, run.command.Process.Signal(syscall.SIGINT))
			var exitErr *exec.ExitError
			require.ErrorAs(t, run.wait(t), &exitErr)
			status, ok := exitErr.Sys().(syscall.WaitStatus)
			require.True(t, ok)
			require.Equal(t, syscall.SIGINT, status.Signal())
		})
	}
}

// Container runtimes and service managers can deliver SIGTERM more than once,
// for example to a whole process group and again through a relay such as
// sudo. Unlike a second SIGINT, a repeated SIGTERM must not cut runner
// cleanup short.
func TestRepeatedTerminateDuringRunCleanup(t *testing.T) {
	for _, commandName := range []string{"server", "scheduler", "start-all"} {
		t.Run(commandName, func(t *testing.T) {
			run := startSignalRun(t, commandName, 10)
			require.NoError(t, run.command.Process.Signal(syscall.SIGTERM))
			run.waitForFile(t, run.stopped)
			require.NoError(t, run.command.Process.Signal(syscall.SIGTERM))
			run.assertAlive(t, 200*time.Millisecond)
			releaseHoldFile(t, run.release)
			require.NoError(t, run.wait(t))
			_, err := os.Stat(run.cleaned)
			require.NoError(t, err, "supervisor exited before step cleanup")
			run.assertStatus(t, ir.Aborted)
		})
	}
}

func TestSchedulerUnsupportedSignal(t *testing.T) {
	for _, shutdownSignal := range []os.Signal{syscall.SIGHUP, syscall.SIGQUIT} {
		t.Run(shutdownSignal.String(), func(t *testing.T) {
			run := startSignalRun(t, "scheduler", 10)
			require.NoError(t, run.command.Process.Signal(shutdownSignal))
			require.NoError(t, run.wait(t))
			before, err := os.Stat(run.progress)
			require.NoError(t, err)
			require.Eventually(t, func() bool {
				after, err := os.Stat(run.progress)
				return err == nil && after.Size() > before.Size()
			}, time.Second, 20*time.Millisecond, "run stopped after unsupported signal")
			run.assertStatus(t, ir.Running)
		})
	}
}

type signalRun struct {
	th       test.Command
	dag      test.DAG
	command  *exec.Cmd
	waitCh   <-chan error
	exited   bool
	stopped  string
	cleaned  string
	release  string
	progress string
	logFile  *os.File
}

func startSignalRun(t *testing.T, commandName string, maxCleanup int, edits ...func(string) string) *signalRun {
	t.Helper()
	th := test.SetupCommand(t, test.WithBuiltExecutable())
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	run := &signalRun{
		th:       th,
		stopped:  filepath.Join(dir, "signal"),
		cleaned:  filepath.Join(dir, "cleaned"),
		release:  newHoldFile(t),
		progress: filepath.Join(dir, "progress"),
	}
	yaml := fmt.Sprintf(`
type: graph
max_clean_up_time_sec: %d
steps:
  - id: probe
    shell: /bin/sh
    script: |
      cleanup() {
        trap '' TERM INT
        printf '%%s' "$1" > %s
        while [ ! -f %s ]; do sleep 0.05; done
        printf done > %s
        exit 0
      }
      trap 'cleanup TERM' TERM
      trap 'cleanup INT' INT
      printf ready > %s
      while :; do printf x >> %s; sleep 0.05; done
`, maxCleanup, test.PosixQuote(run.stopped), test.PosixQuote(run.release), test.PosixQuote(run.cleaned), test.PosixQuote(ready), test.PosixQuote(run.progress))
	for _, edit := range edits {
		yaml = edit(yaml)
	}
	run.dag = th.DAG(t, yaml)
	args := []string{commandName}
	port := ""
	startupLog := "Scheduler started"
	if commandName != "scheduler" {
		port = findPort(t)
		args = append(args, "--host=127.0.0.1", "--port="+port)
		startupLog = "Server is starting"
	}
	run.command = exec.Command(th.Config.Paths.Executable, test.WithConfigFlag(args, th.Config)...) //nolint:gosec // Test executes the repository binary.
	run.command.Env = append(th.ChildEnv, "DAGU_SIGNAL_PROPAGATION=true", "GOMAXPROCS=1")
	logFile, err := os.CreateTemp(dir, "supervisor-*.log")
	require.NoError(t, err)
	t.Cleanup(func() { _ = logFile.Close() })
	run.logFile = logFile
	// A pipe makes exec.Cmd.Wait wait for inherited runner output as well as
	// the supervisor, hiding an early supervisor exit.
	run.command.Stdout = logFile
	run.command.Stderr = logFile
	require.NoError(t, run.command.Start())
	waitCh := make(chan error, 1)
	run.waitCh = waitCh
	go func() { waitCh <- run.command.Wait() }()
	t.Cleanup(func() {
		releaseHoldFile(t, run.release)
		_ = th.DAGRunMgr.Stop(th.Context, run.dag.DAG, "")
		if status, err := th.DAGRunMgr.GetLatestStatus(th.Context, run.dag.DAG); err == nil && status.Status.IsActive() {
			run.dag.AssertLatestStatus(t, ir.Aborted)
		}
		if !run.exited {
			terminateTestCommand(run.command, waitCh)
		}
	})
	require.Eventually(t, func() bool {
		return strings.Contains(run.output(), startupLog)
	}, commandLogWaitTimeout(), 20*time.Millisecond, "output: %s", run.output())
	if commandName == "server" {
		client := &http.Client{Timeout: time.Second}
		baseURL := "http://127.0.0.1:" + port + "/api/v1"
		require.Eventually(t, func() bool {
			resp, err := client.Get(baseURL + "/health")
			if err != nil {
				return false
			}
			_ = resp.Body.Close()
			return resp.StatusCode == http.StatusOK
		}, commandLogWaitTimeout(), 20*time.Millisecond)
		fileName := strings.TrimSuffix(filepath.Base(run.dag.Location), ".yaml")
		resp, err := client.Post(baseURL+"/dags/"+url.PathEscape(fileName)+"/start", "application/json", strings.NewReader("{}"))
		require.NoError(t, err)
		_ = resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)
	} else {
		enqueue := exec.Command(th.Config.Paths.Executable, test.WithConfigFlag([]string{"enqueue", run.dag.Location}, th.Config)...) //nolint:gosec // Test executes the repository binary.
		enqueue.Env = run.command.Env
		output, err := enqueue.CombinedOutput()
		require.NoError(t, err, "output: %s", output)
	}
	run.waitForFile(t, ready)
	return run
}

func (r *signalRun) waitForFile(t *testing.T, path string) {
	t.Helper()
	require.Eventually(t, func() bool {
		_, err := os.Stat(path)
		return err == nil
	}, commandLogWaitTimeout(), 20*time.Millisecond, "output: %s", r.output())
}

func (r *signalRun) assertAlive(t *testing.T, duration time.Duration) {
	t.Helper()
	select {
	case err := <-r.waitCh:
		r.exited = true
		t.Fatalf("supervisor exited during runner cleanup: %v; output: %s", err, r.output())
	case <-time.After(duration):
	}
}

func (r *signalRun) wait(t *testing.T) error {
	t.Helper()
	select {
	case err := <-r.waitCh:
		r.exited = true
		return err
	case <-time.After(commandLogWaitTimeout()):
		t.Fatalf("supervisor did not shut down; output: %s", r.output())
		return nil
	}
}

func (r *signalRun) output() string {
	data, _ := os.ReadFile(r.logFile.Name())
	return string(data)
}

func (r *signalRun) assertStatus(t *testing.T, want ir.Status) {
	t.Helper()
	status, err := r.th.DAGRunMgr.GetLatestStatus(r.th.Context, r.dag.DAG)
	require.NoError(t, err)
	require.Equal(t, want, status.Status)
}
