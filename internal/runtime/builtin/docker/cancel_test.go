// SPDX-License-Identifier: GPL-3.0-or-later

package docker

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func dockerAPIResponse(statusCode int, body string) *http.Response {
	header := make(http.Header)
	if body != "" {
		header.Set("Content-Type", "application/json")
	}
	return &http.Response{
		StatusCode: statusCode,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     header,
	}
}

func TestWaitUntilContainerStopped_StopsAndReturns_WhenContextCanceled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	var stopped atomic.Bool
	inspect := func(context.Context) (running bool, notFound bool, err error) {
		return !stopped.Load(), false, nil
	}
	stop := func(context.Context) error {
		stopped.Store(true)
		return nil
	}

	cancel()

	done := make(chan error, 1)
	go func() {
		done <- waitUntilContainerStopped(ctx, inspect, stop, 10*time.Millisecond, defaultCancelStopWait)
	}()

	select {
	case err := <-done:
		require.NoError(t, err)
		assert.True(t, stopped.Load(), "canceled wait must stop the still-running container")
	case <-time.After(200 * time.Millisecond):
		t.Fatal("waitUntilContainerStopped hung after ctx cancel; it must stop the container and return")
	}
}

func TestWaitUntilContainerStopped_ReturnsImmediately_WhenAlreadyStopped(t *testing.T) {
	t.Parallel()

	var stopCalls atomic.Int32
	err := waitUntilContainerStopped(context.Background(),
		func(context.Context) (bool, bool, error) { return false, false, nil },
		func(context.Context) error {
			stopCalls.Add(1)
			return nil
		},
		10*time.Millisecond,
		defaultCancelStopWait,
	)

	require.NoError(t, err)
	assert.Equal(t, int32(0), stopCalls.Load(), "must not stop a container that is already not running")
}

func TestWaitUntilContainerStopped_TreatsNotFoundAsStopped(t *testing.T) {
	t.Parallel()

	err := waitUntilContainerStopped(context.Background(),
		func(context.Context) (bool, bool, error) { return false, true, nil },
		func(context.Context) error { t.Fatal("stop must not run when inspect reports not found"); return nil },
		10*time.Millisecond,
		defaultCancelStopWait,
	)

	require.NoError(t, err)
}

func TestWaitUntilContainerStopped_Returns_WhenStopIsNoOpAndContainerKeepsRunning(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan error, 1)
	go func() {
		done <- waitUntilContainerStopped(ctx,
			func(context.Context) (bool, bool, error) { return true, false, nil },
			func(context.Context) error { return nil },
			10*time.Millisecond,
			40*time.Millisecond,
		)
	}()

	select {
	case err := <-done:
		require.Error(t, err, "no-op stop with a still-running container must not poll forever")
	case <-time.After(200 * time.Millisecond):
		t.Fatal("waitUntilContainerStopped hung after a no-op stop; cancel must bound the wait")
	}
}

func TestWaitUntilContainerStopped_ReturnsStopError_WhenCancelAndStopFails(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	want := errors.New("stop failed")

	err := waitUntilContainerStopped(ctx,
		func(context.Context) (bool, bool, error) { return true, false, nil },
		func(context.Context) error { return want },
		10*time.Millisecond,
		time.Second,
	)

	require.ErrorIs(t, err, want)
	require.ErrorIs(t, err, context.Canceled)
}

func TestWaitUntilContainerStopped_PreservesCancelWhenCleanupInspectFails(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	want := errors.New("inspect failed")

	err := waitUntilContainerStopped(ctx,
		func(context.Context) (bool, bool, error) { return false, false, want },
		nil,
		10*time.Millisecond,
		time.Second,
	)

	require.ErrorIs(t, err, want)
	require.ErrorIs(t, err, context.Canceled)
}

func TestWaitUntilContainerStopped_PreservesInspectErrorWhenCleanupDeadlineExpires(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	want := errors.New("inspect failed")

	err := waitUntilContainerStopped(ctx,
		func(inspectCtx context.Context) (bool, bool, error) {
			<-inspectCtx.Done()
			return false, false, errors.Join(inspectCtx.Err(), want)
		},
		nil,
		time.Millisecond,
		20*time.Millisecond,
	)

	require.ErrorIs(t, err, context.Canceled)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.ErrorIs(t, err, want)
}

func TestWaitUntilContainerStopped_ReturnsInspectError(t *testing.T) {
	t.Parallel()

	want := errors.New("inspect failed")
	err := waitUntilContainerStopped(context.Background(),
		func(context.Context) (bool, bool, error) { return false, false, want },
		func(context.Context) error { return nil },
		10*time.Millisecond,
		defaultCancelStopWait,
	)

	require.ErrorIs(t, err, want)
}

func TestWaitUntilContainerStopped_CancelsStopAndInspect_WhenTheyBlock(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan error, 1)
	go func() {
		done <- waitUntilContainerStopped(ctx,
			func(inspectCtx context.Context) (bool, bool, error) {
				<-inspectCtx.Done()
				return true, false, inspectCtx.Err()
			},
			func(stopCtx context.Context) error {
				<-stopCtx.Done()
				return stopCtx.Err()
			},
			10*time.Millisecond,
			40*time.Millisecond,
		)
	}()

	select {
	case err := <-done:
		require.Error(t, err, "stalled docker stop/inspect must be bound by the cleanup deadline")
	case <-time.After(200 * time.Millisecond):
		t.Fatal("waitUntilContainerStopped hung because stop/inspect used context.Background()")
	}
}

func TestWaitUntilContainerStopped_CancelsInitialInspectAndStops(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	inspectStarted := make(chan struct{})
	var stopped atomic.Bool
	var inspectCalls atomic.Int32

	done := make(chan error, 1)
	go func() {
		done <- waitUntilContainerStopped(ctx,
			func(inspectCtx context.Context) (bool, bool, error) {
				if inspectCalls.Add(1) == 1 {
					close(inspectStarted)
					<-inspectCtx.Done()
					return true, false, inspectCtx.Err()
				}
				return !stopped.Load(), false, nil
			},
			func(context.Context) error {
				stopped.Store(true)
				return nil
			},
			10*time.Millisecond,
			40*time.Millisecond,
		)
	}()

	<-inspectStarted
	cancel()

	select {
	case err := <-done:
		require.NoError(t, err)
		assert.True(t, stopped.Load(), "cancellation during inspect must still stop the container")
	case <-time.After(200 * time.Millisecond):
		t.Fatal("waitUntilContainerStopped did not interrupt the initial inspect on cancellation")
	}
}

func TestClientRun_StopsContainer_WhenContextCanceled(t *testing.T) {
	dockerSDK := newDockerSDKOrSkip(t)
	t.Cleanup(func() { _ = dockerSDK.Close() })

	pullCtx, pullCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer pullCancel()
	pullImageOrSkip(t, dockerSDK, pullCtx, "alpine:latest")

	name := fmt.Sprintf("dagu-timeout-test-%d", time.Now().UnixNano())
	cfg, err := LoadConfigFromMap(map[string]any{
		"image":          "alpine:latest",
		"container_name": name,
		"auto_remove":    true,
		"pull":           "never",
	}, nil)
	require.NoError(t, err)
	cfg.ShouldStart = true

	cli, err := InitializeClient(context.Background(), cfg)
	require.NoError(t, err)
	t.Cleanup(func() { cli.Close(context.Background()) })

	runCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	start := time.Now()
	_, runErr := cli.Run(runCtx, []string{"sleep", "60"}, io.Discard, io.Discard)
	elapsed := time.Since(start)

	require.Less(t, elapsed, 15*time.Second,
		"Client.Run must stop the container on timeout instead of waiting for sleep 60 (took %s, err=%v)",
		elapsed, runErr)
	require.Error(t, runErr)

	inspectCtx, inspectCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer inspectCancel()
	_, inspectErr := dockerSDK.ContainerInspect(inspectCtx, name, client.ContainerInspectOptions{})
	require.Error(t, inspectErr, "auto-removed container %s must not remain after cancel", name)
}

func TestClientRun_LeavesStoppedContainer_WhenKeepContainerAndCanceled(t *testing.T) {
	dockerSDK := newDockerSDKOrSkip(t)
	t.Cleanup(func() { _ = dockerSDK.Close() })

	pullCtx, pullCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer pullCancel()
	pullImageOrSkip(t, dockerSDK, pullCtx, "alpine:latest")

	name := fmt.Sprintf("dagu-keep-timeout-test-%d", time.Now().UnixNano())
	cfg, err := LoadConfigFromMap(map[string]any{
		"image":          "alpine:latest",
		"container_name": name,
		"auto_remove":    false,
		"pull":           "never",
	}, nil)
	require.NoError(t, err)
	cfg.ShouldStart = true

	cli, err := InitializeClient(context.Background(), cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		cli.Close(context.Background())
		_, _ = dockerSDK.ContainerRemove(context.Background(), name, client.ContainerRemoveOptions{Force: true})
	})

	runCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	start := time.Now()
	_, runErr := cli.Run(runCtx, []string{"sleep", "60"}, io.Discard, io.Discard)
	elapsed := time.Since(start)

	require.Less(t, elapsed, 15*time.Second, "keep_container timeout must still stop sleep 60 (took %s, err=%v)", elapsed, runErr)
	require.Error(t, runErr)

	inspectCtx, inspectCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer inspectCancel()
	info, inspectErr := dockerSDK.ContainerInspect(inspectCtx, name, client.ContainerInspectOptions{})
	require.NoError(t, inspectErr, "keep_container must leave the container after timeout")
	require.NotNil(t, info.Container.State)
	assert.False(t, info.Container.State.Running, "keep_container timeout must stop the container, not leave it running")
}

func TestClientExec_StopsStepProcessAndDescendants_WhenContextCanceled(t *testing.T) {
	dockerSDK := newDockerSDKOrSkip(t)
	t.Cleanup(func() { _ = dockerSDK.Close() })

	pullCtx, pullCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer pullCancel()
	pullImageOrSkip(t, dockerSDK, pullCtx, "alpine:latest")

	name := fmt.Sprintf("dagu-exec-timeout-test-%d", time.Now().UnixNano())
	cfg, err := LoadConfigFromMap(map[string]any{
		"image":          "alpine:latest",
		"container_name": name,
		"auto_remove":    true,
		"pull":           "never",
	}, nil)
	require.NoError(t, err)
	cfg.ShouldStart = true
	cfg.Startup = "keepalive"

	cli, err := InitializeClient(context.Background(), cfg)
	require.NoError(t, err)
	t.Cleanup(func() { cli.Close(context.Background()) })

	require.NoError(t, cli.StartBackground(context.Background()))

	runCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	start := time.Now()
	_, runErr := cli.Exec(runCtx, []string{
		"sh",
		"-c",
		`trap 'exit 0' TERM; (trap '' TERM; exec sleep 60) & while true; do sleep 1; done`,
	}, io.Discard, io.Discard, nativeExecOptions())
	elapsed := time.Since(start)

	require.Less(t, elapsed, 15*time.Second, "shared-container exec must return on timeout (took %s, err=%v)", elapsed, runErr)
	require.Error(t, runErr)

	inspectCtx, inspectCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer inspectCancel()
	info, inspectErr := dockerSDK.ContainerInspect(inspectCtx, name, client.ContainerInspectOptions{})
	require.NoError(t, inspectErr, "keepalive container must still exist after exec cancel")
	require.NotNil(t, info.Container.State)
	assert.True(t, info.Container.State.Running, "exec cancel must not stop the shared keepalive container")
	assertNoProcessInContainer(t, dockerSDK, name, "sleep 60")
}

func TestClientExec_StopsNonRootStepWithoutContainerShell(t *testing.T) {
	dockerSDK := newDockerSDKOrSkip(t)
	t.Cleanup(func() { _ = dockerSDK.Close() })

	pullCtx, pullCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer pullCancel()
	pullImageOrSkip(t, dockerSDK, pullCtx, "alpine:latest")

	name := fmt.Sprintf("dagu-exec-shellless-timeout-%d", time.Now().UnixNano())
	cfg, err := LoadConfigFromMap(map[string]any{
		"image":          "alpine:latest",
		"container_name": name,
		"auto_remove":    true,
		"pull":           "never",
	}, nil)
	require.NoError(t, err)
	cfg.ShouldStart = true
	cfg.Startup = "keepalive"
	cfg.ExecOptions = &client.ExecCreateOptions{User: "65534"}

	cli, err := InitializeClient(context.Background(), cfg)
	require.NoError(t, err)
	t.Cleanup(func() { cli.Close(context.Background()) })
	require.NoError(t, cli.StartBackground(context.Background()))
	assertCanceledNonRootExecWithoutShell(t, dockerSDK, cli, name)
}

func TestClientExec_StopsNonRootStepWithCopiedHelper(t *testing.T) {
	dockerSDK := newDockerSDKOrSkip(t)
	t.Cleanup(func() { _ = dockerSDK.Close() })

	pullCtx, pullCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer pullCancel()
	pullImageOrSkip(t, dockerSDK, pullCtx, "alpine:latest")

	name := fmt.Sprintf("dagu-exec-copied-helper-%d", time.Now().UnixNano())
	cfg, err := LoadConfigFromMap(map[string]any{
		"image":          "alpine:latest",
		"container_name": name,
		"auto_remove":    true,
		"pull":           "never",
	}, nil)
	require.NoError(t, err)
	cfg.ShouldStart = true
	cfg.Startup = "keepalive"
	cfg.ExecOptions = &client.ExecCreateOptions{User: "65534"}

	cli, err := InitializeClient(context.Background(), cfg)
	require.NoError(t, err)
	t.Cleanup(func() { cli.Close(context.Background()) })
	_, err = cli.startNewContainer(
		context.Background(),
		name,
		cli.cli,
		[]string{keepAliveTargetPath},
		true,
		func(ctx context.Context, containerID string) error {
			return copyKeepaliveToContainer(ctx, cli.cli, containerID, cli.platform)
		},
	)
	require.NoError(t, err)
	cli.cancelHelper = cancelHelperCopied

	assertCanceledNonRootExecWithoutShell(t, dockerSDK, cli, name)
}

func TestStartNewContainer_RemovesReadOnlyContainerWhenHelperCopyFails(t *testing.T) {
	dockerSDK := newDockerSDKOrSkip(t)
	t.Cleanup(func() { _ = dockerSDK.Close() })

	pullCtx, pullCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer pullCancel()
	pullImageOrSkip(t, dockerSDK, pullCtx, "alpine:latest")

	name := fmt.Sprintf("dagu-readonly-helper-%d", time.Now().UnixNano())
	cfg, err := LoadConfigFromMap(map[string]any{
		"image":          "alpine:latest",
		"container_name": name,
		"auto_remove":    false,
		"pull":           "never",
	}, nil)
	require.NoError(t, err)
	cfg.ShouldStart = true
	cfg.Host.ReadonlyRootfs = true

	cli, err := InitializeClient(context.Background(), cfg)
	require.NoError(t, err)
	t.Cleanup(func() { cli.Close(context.Background()) })
	_, err = cli.startNewContainer(
		context.Background(),
		name,
		cli.cli,
		[]string{keepAliveTargetPath},
		true,
		func(ctx context.Context, containerID string) error {
			return copyKeepaliveToContainer(ctx, cli.cli, containerID, cli.platform)
		},
	)
	require.Error(t, err)

	inspectCtx, inspectCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer inspectCancel()
	_, inspectErr := dockerSDK.ContainerInspect(inspectCtx, name, client.ContainerInspectOptions{})
	require.Error(t, inspectErr, "failed initialization must not leave the stopped container behind")
}

func TestClientExec_StopsNonRootStepInExternalContainer(t *testing.T) {
	dockerSDK := newDockerSDKOrSkip(t)
	t.Cleanup(func() { _ = dockerSDK.Close() })

	pullCtx, pullCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer pullCancel()
	pullImageOrSkip(t, dockerSDK, pullCtx, "alpine:latest")

	name := fmt.Sprintf("dagu-external-timeout-%d", time.Now().UnixNano())
	createExternalContainer(t, dockerSDK, name)
	cli := newExternalContainerClient(t, name, "65534")

	runCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, runErr := cli.Exec(runCtx, []string{"/bin/sleep", "60"}, io.Discard, io.Discard, nativeExecOptions())
	require.ErrorIs(t, runErr, context.DeadlineExceeded)
	assertNoProcessInContainerTop(t, dockerSDK, name, "/bin/sleep 60")

	inspectCtx, inspectCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer inspectCancel()
	info, inspectErr := dockerSDK.ContainerInspect(inspectCtx, name, client.ContainerInspectOptions{})
	require.NoError(t, inspectErr)
	require.NotNil(t, info.Container.State)
	assert.True(t, info.Container.State.Running, "exec cancellation must not stop an external container")
}

func TestClientExec_ReturnsCleanupErrorForShelllessExternalContainer(t *testing.T) {
	dockerSDK := newDockerSDKOrSkip(t)
	t.Cleanup(func() { _ = dockerSDK.Close() })

	pullCtx, pullCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer pullCancel()
	pullImageOrSkip(t, dockerSDK, pullCtx, "alpine:latest")

	name := fmt.Sprintf("dagu-external-shellless-%d", time.Now().UnixNano())
	createExternalContainer(t, dockerSDK, name)
	runContainerCommand(t, dockerSDK, name, client.ExecCreateOptions{
		User: "0",
		Cmd:  []string{"/bin/rm", "-f", "/bin/sh"},
	})
	cli := newExternalContainerClient(t, name, "65534")

	runCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, runErr := cli.Exec(runCtx, []string{"/bin/sleep", "60"}, io.Discard, io.Discard, nativeExecOptions())
	require.ErrorIs(t, runErr, context.DeadlineExceeded)
	var joined interface{ Unwrap() []error }
	require.ErrorAs(t, runErr, &joined)
	require.Len(t, joined.Unwrap(), 2, "cancellation must retain the external-container cleanup failure")

	inspectCtx, inspectCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer inspectCancel()
	info, inspectErr := dockerSDK.ContainerInspect(inspectCtx, name, client.ContainerInspectOptions{})
	require.NoError(t, inspectErr)
	require.NotNil(t, info.Container.State)
	assert.True(t, info.Container.State.Running, "failed exec cleanup must not stop an external container")
}

func TestTerminateExecProcess_SweepsTokenProcessesAfterMainExecStops(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                 string
		initiallyRunning     bool
		stopAfterTerm        bool
		termExitCode         int
		waitInspectionErrors bool
		wantSignals          []string
		wantErr              bool
	}{
		{
			name:             "main exec stops after TERM",
			initiallyRunning: true,
			stopAfterTerm:    true,
			wantSignals:      []string{"TERM", "KILL"},
		},
		{
			name:        "main exec already stopped",
			wantSignals: []string{"KILL"},
		},
		{
			name:             "TERM signaling fails",
			initiallyRunning: true,
			termExitCode:     1,
			wantSignals:      []string{"TERM", "KILL"},
			wantErr:          true,
		},
		{
			name:                 "stop inspection fails after TERM",
			initiallyRunning:     true,
			waitInspectionErrors: true,
			wantSignals:          []string{"TERM", "KILL"},
			wantErr:              true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mainRunning := tt.initiallyRunning
			mainInspectCalls := 0
			var signals []string

			httpClient := &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				path := req.URL.Path
				switch {
				case req.Method == http.MethodGet && strings.HasSuffix(path, "/exec/main-exec/json"):
					mainInspectCalls++
					if tt.waitInspectionErrors && mainInspectCalls == 2 {
						return nil, errors.New("inspect failed")
					}
					return dockerAPIResponse(http.StatusOK, fmt.Sprintf(
						`{"ID":"main-exec","ContainerID":"ctr","Running":%t,"ExitCode":0}`,
						mainRunning,
					)), nil
				case req.Method == http.MethodPost && strings.HasSuffix(path, "/containers/ctr/exec"):
					var execConfig struct {
						Cmd []string `json:"Cmd"`
					}
					if err := json.NewDecoder(req.Body).Decode(&execConfig); err != nil {
						return nil, fmt.Errorf("decode signal exec request: %w", err)
					}
					if len(execConfig.Cmd) != 4 || execConfig.Cmd[0] != keepAliveTargetPath || execConfig.Cmd[1] != "signal-token" {
						return nil, fmt.Errorf("unexpected signal command: %v", execConfig.Cmd)
					}
					signalName := execConfig.Cmd[2]
					signals = append(signals, signalName)
					return dockerAPIResponse(
						http.StatusCreated,
						fmt.Sprintf(`{"Id":"signal-%s"}`, strings.ToLower(signalName)),
					), nil
				case req.Method == http.MethodPost && strings.HasSuffix(path, "/exec/signal-term/start"):
					if tt.stopAfterTerm {
						mainRunning = false
					}
					return dockerAPIResponse(http.StatusOK, `{}`), nil
				case req.Method == http.MethodPost && strings.HasSuffix(path, "/exec/signal-kill/start"):
					mainRunning = false
					return dockerAPIResponse(http.StatusOK, `{}`), nil
				case req.Method == http.MethodGet && strings.Contains(path, "/exec/signal-") && strings.HasSuffix(path, "/json"):
					exitCode := 0
					if strings.Contains(path, "/exec/signal-term/") {
						exitCode = tt.termExitCode
					}
					return dockerAPIResponse(http.StatusOK, fmt.Sprintf(
						`{"ContainerID":"ctr","Running":false,"ExitCode":%d}`,
						exitCode,
					)), nil
				default:
					return nil, fmt.Errorf("unexpected Docker API request: %s %s", req.Method, path)
				}
			})}

			dockerSDK, err := client.New(
				client.WithHTTPClient(httpClient),
				client.WithAPIVersion("1.52"),
			)
			require.NoError(t, err)
			t.Cleanup(func() { _ = dockerSDK.Close() })

			err = terminateExecProcess(
				context.Background(),
				dockerSDK,
				"main-exec",
				"token",
				execSignalOptions{helper: cancelHelperBound},
			)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			assert.Equal(t, tt.wantSignals, signals)
		})
	}
}

// The daemon writes the attach response header before it marks the exec running,
// so an inspect issued in that window reports Running:false with a zero exit code
// for a command that has not started yet.
func TestClientExec_WaitsForAttachStream_BeforeReadingExitCode(t *testing.T) {
	t.Parallel()

	var execRunning atomic.Bool
	var execExitCode atomic.Int32
	releaseAttach := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		path := req.URL.Path
		switch {
		case req.Method == http.MethodGet && strings.HasSuffix(path, "/containers/ctr/json"):
			writeDockerJSON(w, `{"Id":"ctr","State":{"Running":true},"Config":{"Env":[]}}`)
		case req.Method == http.MethodPost && strings.HasSuffix(path, "/containers/ctr/exec"):
			writeDockerJSON(w, `{"Id":"main-exec"}`)
		case req.Method == http.MethodPost && strings.HasSuffix(path, "/exec/main-exec/start"):
			hijacker, ok := w.(http.Hijacker)
			if !ok {
				t.Error("test Docker server does not support hijacking")
				return
			}
			conn, _, err := hijacker.Hijack()
			if err != nil {
				t.Errorf("hijack attach connection: %v", err)
				return
			}
			// Complete the attach handshake, then hold the stream open: the exec
			// is attached but the daemon has not marked it running yet.
			_, _ = conn.Write([]byte("HTTP/1.1 101 UPGRADED\r\n" +
				"Content-Type: application/vnd.docker.multiplexed-stream\r\n" +
				"Connection: Upgrade\r\nUpgrade: tcp\r\n\r\n"))
			<-releaseAttach
			execRunning.Store(false)
			execExitCode.Store(42)
			_ = conn.Close()
		case req.Method == http.MethodGet && strings.HasSuffix(path, "/exec/main-exec/json"):
			writeDockerJSON(w, fmt.Sprintf(
				`{"ID":"main-exec","ContainerID":"ctr","Running":%t,"ExitCode":%d}`,
				execRunning.Load(), execExitCode.Load()))
		default:
			http.Error(w, "unexpected Docker API request: "+req.Method+" "+path, http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	dockerSDK, err := client.New(
		client.WithHost("tcp://"+strings.TrimPrefix(server.URL, "http://")),
		client.WithScheme("http"),
		client.WithAPIVersion("1.52"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = dockerSDK.Close() })

	cli := &Client{
		cfg:         &Config{Container: &container.Config{}},
		containerID: "ctr",
		cli:         dockerSDK,
	}
	cli.started.Store(true)

	type execResult struct {
		exitCode int
		err      error
	}
	done := make(chan execResult, 1)
	go func() {
		exitCode, err := cli.Exec(context.Background(), []string{"sleep", "1"}, io.Discard, io.Discard, nativeExecOptions())
		done <- execResult{exitCode, err}
	}()

	select {
	case res := <-done:
		t.Fatalf("Exec returned %d (err=%v) before the exec started; it must not read the pre-start exit code", res.exitCode, res.err)
	case <-time.After(300 * time.Millisecond):
	}

	close(releaseAttach)

	select {
	case res := <-done:
		require.ErrorContains(t, res.err, "exit code: 42")
		assert.Equal(t, 42, res.exitCode, "Exec must report the exit code the daemon recorded once the exec finished")
	case <-time.After(10 * time.Second):
		t.Fatal("Exec did not return after the attach stream closed")
	}
}

func TestClientExec_CleansUpWhenCancellationRacesWithAttach(t *testing.T) {
	t.Parallel()

	attachStarted := make(chan struct{})
	releaseAttach := make(chan struct{})
	cleanupStarted := make(chan struct{})
	var createdExecs atomic.Int32
	var signalStarts atomic.Int32
	var execStopped atomic.Bool

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		path := req.URL.Path
		switch {
		case req.Method == http.MethodGet && strings.HasSuffix(path, "/containers/ctr/json"):
			writeDockerJSON(w, `{"Id":"ctr","State":{"Running":true},"Config":{"Env":[]}}`)
		case req.Method == http.MethodPost && strings.HasSuffix(path, "/containers/ctr/exec"):
			if createdExecs.Add(1) == 1 {
				writeDockerJSON(w, `{"Id":"main-exec"}`)
			} else {
				writeDockerJSON(w, `{"Id":"signal-exec"}`)
			}
		case req.Method == http.MethodPost && strings.HasSuffix(path, "/exec/main-exec/start"):
			hijacker, ok := w.(http.Hijacker)
			if !ok {
				t.Error("test Docker server does not support hijacking")
				return
			}
			conn, _, err := hijacker.Hijack()
			if err != nil {
				t.Errorf("hijack attach connection: %v", err)
				return
			}
			close(attachStarted)
			<-releaseAttach
			_ = conn.Close()
		case req.Method == http.MethodGet && strings.HasSuffix(path, "/exec/main-exec/json"):
			writeDockerJSON(w, fmt.Sprintf(`{"ID":"main-exec","ContainerID":"ctr","Running":%t,"ExitCode":0}`, !execStopped.Load()))
		case req.Method == http.MethodPost && strings.HasSuffix(path, "/exec/signal-exec/start"):
			if signalStarts.Add(1) == 1 {
				execStopped.Store(true)
				close(cleanupStarted)
			}
			writeDockerJSON(w, `{}`)
		case req.Method == http.MethodGet && strings.HasSuffix(path, "/exec/signal-exec/json"):
			writeDockerJSON(w, `{"ID":"signal-exec","ContainerID":"ctr","Running":false,"ExitCode":0}`)
		default:
			http.Error(w, "unexpected Docker API request: "+req.Method+" "+path, http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	dockerSDK, err := client.New(
		client.WithHost("tcp://"+strings.TrimPrefix(server.URL, "http://")),
		client.WithScheme("http"),
		client.WithAPIVersion("1.52"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = dockerSDK.Close() })
	cli := &Client{
		cfg:          &Config{Container: &container.Config{}},
		containerID:  "ctr",
		cli:          dockerSDK,
		cancelHelper: cancelHelperBound,
	}
	cli.started.Store(true)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := cli.Exec(ctx, []string{"sleep", "60"}, io.Discard, io.Discard, nativeExecOptions())
		done <- err
	}()
	<-attachStarted
	cancel()
	close(releaseAttach)

	select {
	case runErr := <-done:
		require.ErrorIs(t, runErr, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("exec did not return after cancellation raced with attach")
	}
	select {
	case <-cleanupStarted:
	case <-time.After(time.Second):
		t.Fatal("exec process cleanup was not attempted after the attach race")
	}
}

func writeDockerJSON(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, body)
}

func assertCanceledNonRootExecWithoutShell(t *testing.T, dockerSDK *client.Client, cli *Client, name string) {
	t.Helper()

	runContainerCommand(t, dockerSDK, name, client.ExecCreateOptions{
		User: "0",
		Cmd:  []string{"/bin/rm", "-f", "/bin/sh"},
	})

	runCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, runErr := cli.Exec(runCtx, []string{"/bin/sleep", "60"}, io.Discard, io.Discard, nativeExecOptions())
	require.ErrorIs(t, runErr, context.DeadlineExceeded)

	inspectCtx, inspectCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer inspectCancel()
	info, inspectErr := dockerSDK.ContainerInspect(inspectCtx, name, client.ContainerInspectOptions{})
	require.NoError(t, inspectErr)
	require.NotNil(t, info.Container.State)
	assert.True(t, info.Container.State.Running, "exec cancellation must not stop the shared container")
	assertNoProcessInContainerTop(t, dockerSDK, name, "/bin/sleep 60")
}

func createExternalContainer(t *testing.T, dockerSDK *client.Client, name string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	created, err := dockerSDK.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config: &container.Config{
			Image: "alpine:latest",
			Cmd:   []string{"/bin/sleep", "300"},
		},
		Name: name,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = dockerSDK.ContainerRemove(context.Background(), created.ID, client.ContainerRemoveOptions{Force: true})
	})
	_, err = dockerSDK.ContainerStart(ctx, created.ID, client.ContainerStartOptions{})
	require.NoError(t, err)
}

func newExternalContainerClient(t *testing.T, name string, user string) *Client {
	t.Helper()

	cfg, err := LoadConfigFromMap(map[string]any{"container_name": name}, nil)
	require.NoError(t, err)
	cfg.ExecOptions = &client.ExecCreateOptions{User: user}
	cli, err := InitializeClient(context.Background(), cfg)
	require.NoError(t, err)
	t.Cleanup(func() { cli.Close(context.Background()) })
	return cli
}
func assertNoProcessInContainer(t *testing.T, dockerSDK *client.Client, container string, command string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	createResp, err := dockerSDK.ExecCreate(ctx, container, client.ExecCreateOptions{
		Cmd:          []string{"pgrep", "-f", command},
		AttachStdout: true,
		AttachStderr: true,
	})
	require.NoError(t, err)

	attachResp, err := dockerSDK.ExecAttach(ctx, createResp.ID, client.ExecAttachOptions{})
	require.NoError(t, err)
	defer attachResp.Close()

	out, err := io.ReadAll(attachResp.Reader)
	require.NoError(t, err)

	inspectResp, err := dockerSDK.ExecInspect(ctx, createResp.ID, client.ExecInspectOptions{})
	require.NoError(t, err)
	require.False(t, inspectResp.Running)
	require.NotEqual(t, 0, inspectResp.ExitCode, "canceled exec left %q running in %s: %s", command, container, out)
}

func runContainerCommand(t *testing.T, dockerSDK *client.Client, container string, opts client.ExecCreateOptions) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	created, err := dockerSDK.ExecCreate(ctx, container, opts)
	require.NoError(t, err)
	_, err = dockerSDK.ExecStart(ctx, created.ID, client.ExecStartOptions{Detach: true})
	require.NoError(t, err)
	for {
		inspected, err := dockerSDK.ExecInspect(ctx, created.ID, client.ExecInspectOptions{})
		require.NoError(t, err)
		if !inspected.Running {
			require.Zero(t, inspected.ExitCode)
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("container command did not finish")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func assertNoProcessInContainerTop(t *testing.T, dockerSDK *client.Client, container string, command string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	top, err := dockerSDK.ContainerTop(ctx, container, client.ContainerTopOptions{Arguments: []string{"-eo", "pid,args"}})
	require.NoError(t, err)
	for _, process := range top.Processes {
		assert.NotContains(t, process, command, "canceled exec left %q running in %s", command, container)
	}
}

// dockerTestsEnv opts a run in to the tests that create real containers on the
// developer's or CI daemon. They are off by default so `go test ./...` stays a
// unit-test run with no daemon or registry dependency.
const dockerTestsEnv = "DAGU_TEST_DOCKER"

func newDockerSDKOrSkip(t *testing.T) *client.Client {
	t.Helper()

	if os.Getenv(dockerTestsEnv) == "" {
		t.Skipf("set %s=1 to run tests that drive a real docker daemon", dockerTestsEnv)
	}

	dockerSDK, err := client.New(client.FromEnv)
	if err != nil {
		t.Skipf("docker client unavailable: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := dockerSDK.Info(ctx, client.InfoOptions{}); err != nil {
		_ = dockerSDK.Close()
		t.Skipf("docker daemon unavailable: %v", err)
	}
	return dockerSDK
}

func pullImageOrSkip(t *testing.T, dockerSDK *client.Client, ctx context.Context, image string) {
	t.Helper()

	// Anonymous registry pulls are rate limited per IP, so a locally cached image
	// is used as-is rather than re-fetched for every test.
	if _, err := dockerSDK.ImageInspect(ctx, image); err == nil {
		return
	}

	reader, err := dockerSDK.ImagePull(ctx, image, client.ImagePullOptions{})
	if err != nil {
		t.Skipf("cannot pull %s: %v", image, err)
	}
	defer func() { _ = reader.Close() }()
	if err := checkImagePullStream(reader); err != nil {
		t.Skipf("cannot pull %s: %v", image, err)
	}
}

func TestWaitUntilContainerStopped_PollsUntilStopped_WhenContextActive(t *testing.T) {
	t.Parallel()

	var inspects atomic.Int32
	err := waitUntilContainerStopped(context.Background(),
		func(context.Context) (bool, bool, error) {
			if inspects.Add(1) < 3 {
				return true, false, nil
			}
			return false, false, nil
		},
		func(context.Context) error { t.Fatal("stop must not run while ctx is active"); return nil },
		time.Millisecond,
		defaultCancelStopWait,
	)

	require.NoError(t, err)
	assert.GreaterOrEqual(t, inspects.Load(), int32(3))
}

func TestWaitUntilContainerStopped_UsesDefaultPoll_WhenIntervalInvalid(t *testing.T) {
	t.Parallel()

	err := waitUntilContainerStopped(context.Background(),
		func(context.Context) (bool, bool, error) { return false, false, nil },
		nil,
		0,
		defaultCancelStopWait,
	)

	require.NoError(t, err)
}

func TestStopContainer_ReturnsUnavailable_WhenClientOrIDMissing(t *testing.T) {
	t.Parallel()

	require.ErrorIs(t, stopContainer(context.Background(), nil, "ctr", "", 0), errContainerStopUnavailable)
	cli := &client.Client{}
	require.ErrorIs(t, stopContainer(context.Background(), cli, "", "", 0), errContainerStopUnavailable)
}

func TestStopContainer_IgnoresMissingContainer(t *testing.T) {
	dockerSDK := newDockerSDKOrSkip(t)
	t.Cleanup(func() { _ = dockerSDK.Close() })

	err := stopContainer(context.Background(), dockerSDK, "dagu-no-such-container", "", 0)
	require.NoError(t, err)
}

func TestStopContainer_IgnoresAlreadyStoppedContainer(t *testing.T) {
	t.Parallel()

	httpClient := &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet || !strings.HasSuffix(req.URL.Path, "/json") {
			return nil, fmt.Errorf("stopped container must not be stopped again: %s %s", req.Method, req.URL.Path)
		}
		return dockerAPIResponse(http.StatusOK, `{"Id":"stopped-container","State":{"Running":false}}`), nil
	})}
	dockerSDK, err := client.New(
		client.WithHTTPClient(httpClient),
		client.WithAPIVersion("1.44"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = dockerSDK.Close() })

	require.NoError(t, stopContainer(context.Background(), dockerSDK, "stopped-container", "", 0))
}

func TestStopContainer_ForceKillsAfterBlockedStop(t *testing.T) {
	t.Parallel()

	var killed atomic.Bool
	httpClient := &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/stop"):
			<-req.Context().Done()
			return nil, req.Context().Err()
		case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/kill"):
			killed.Store(true)
			return dockerAPIResponse(http.StatusNoContent, ""), nil
		case req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/json"):
			body := fmt.Sprintf(`{"State":{"Running":%t}}`, !killed.Load())
			return dockerAPIResponse(http.StatusOK, body), nil
		default:
			return nil, fmt.Errorf("unexpected Docker request: %s %s", req.Method, req.URL.Path)
		}
	})}
	dockerSDK, err := client.New(
		client.WithHTTPClient(httpClient),
		client.WithAPIVersion("1.44"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = dockerSDK.Close() })

	start := time.Now()
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err = stopContainer(cleanupCtx, dockerSDK, "blocked-container", "", 20*time.Millisecond)

	require.NoError(t, err)
	assert.True(t, killed.Load(), "a blocked graceful stop must be followed by a bounded force kill")
	assert.Less(t, time.Since(start), 200*time.Millisecond)
}

func TestStopContainer_ReturnsWhenAlreadyStopped(t *testing.T) {
	t.Parallel()

	httpClient := &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet || !strings.HasSuffix(req.URL.Path, "/json") {
			return nil, fmt.Errorf("unexpected Docker request: %s %s", req.Method, req.URL.Path)
		}
		return dockerAPIResponse(http.StatusOK, `{"State":{"Running":false}}`), nil
	})}
	dockerSDK, err := client.New(
		client.WithHTTPClient(httpClient),
		client.WithAPIVersion("1.44"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = dockerSDK.Close() })

	cleanupCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	require.NoError(t, stopContainer(
		cleanupCtx,
		dockerSDK,
		"stopped-container",
		"",
		20*time.Millisecond,
	))
}

func TestStopContainer_StopsWhenStateIsUnknown(t *testing.T) {
	t.Parallel()

	var stopped atomic.Bool
	httpClient := &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/json"):
			return dockerAPIResponse(http.StatusOK, `{"State":null}`), nil
		case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/stop"):
			stopped.Store(true)
			return dockerAPIResponse(http.StatusNoContent, ""), nil
		default:
			return nil, fmt.Errorf("unexpected Docker request: %s %s", req.Method, req.URL.Path)
		}
	})}
	dockerSDK, err := client.New(
		client.WithHTTPClient(httpClient),
		client.WithAPIVersion("1.44"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = dockerSDK.Close() })

	cleanupCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	require.NoError(t, stopContainer(
		cleanupCtx,
		dockerSDK,
		"unknown-state-container",
		"",
		20*time.Millisecond,
	))
	assert.True(t, stopped.Load())
}

func TestStopContainer_IgnoresAlreadyStoppedRace(t *testing.T) {
	t.Parallel()

	httpClient := &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/json"):
			return dockerAPIResponse(http.StatusOK, `{"State":{"Running":true}}`), nil
		case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/stop"):
			return dockerAPIResponse(http.StatusNotModified, ""), nil
		default:
			return nil, fmt.Errorf("unexpected Docker request: %s %s", req.Method, req.URL.Path)
		}
	})}
	dockerSDK, err := client.New(
		client.WithHTTPClient(httpClient),
		client.WithAPIVersion("1.44"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = dockerSDK.Close() })

	cleanupCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	require.NoError(t, stopContainer(
		cleanupCtx,
		dockerSDK,
		"already-stopped-container",
		"",
		20*time.Millisecond,
	))
}

func TestStopContainer_KillsAfterPostTimeoutInspectFailure(t *testing.T) {
	t.Parallel()

	inspectErr := errors.New("inspect failed")
	var inspectCalls atomic.Int32
	var killed atomic.Bool
	httpClient := &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/stop"):
			<-req.Context().Done()
			return nil, req.Context().Err()
		case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/kill"):
			killed.Store(true)
			return dockerAPIResponse(http.StatusNoContent, ""), nil
		case req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/json"):
			if inspectCalls.Add(1) == 2 {
				return nil, inspectErr
			}
			body := fmt.Sprintf(`{"State":{"Running":%t}}`, !killed.Load())
			return dockerAPIResponse(http.StatusOK, body), nil
		default:
			return nil, fmt.Errorf("unexpected Docker request: %s %s", req.Method, req.URL.Path)
		}
	})}
	dockerSDK, err := client.New(
		client.WithHTTPClient(httpClient),
		client.WithAPIVersion("1.44"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = dockerSDK.Close() })

	cleanupCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err = stopContainer(
		cleanupCtx,
		dockerSDK,
		"inspect-failure-container",
		"",
		20*time.Millisecond,
	)

	require.NoError(t, err)
	assert.True(t, killed.Load(), "a transient inspect failure must not prevent force kill")
}

func TestStopContainer_IgnoresStoppedBeforeForceKill(t *testing.T) {
	t.Parallel()

	httpClient := &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/json"):
			return dockerAPIResponse(http.StatusOK, `{"State":{"Running":true}}`), nil
		case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/stop"):
			<-req.Context().Done()
			return nil, req.Context().Err()
		case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/kill"):
			return dockerAPIResponse(
				http.StatusConflict,
				`{"message":"Container deadbeefcafe is not running"}`,
			), nil
		default:
			return nil, fmt.Errorf("unexpected Docker request: %s %s", req.Method, req.URL.Path)
		}
	})}
	dockerSDK, err := client.New(
		client.WithHTTPClient(httpClient),
		client.WithAPIVersion("1.44"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = dockerSDK.Close() })

	cleanupCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	require.NoError(t, stopContainer(
		cleanupCtx,
		dockerSDK,
		"stopped-before-kill-container",
		"",
		20*time.Millisecond,
	))
}

func TestRemoveContainerForCleanup_CancelsBlockedRequest(t *testing.T) {
	t.Parallel()

	httpClient := &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		<-req.Context().Done()
		return nil, req.Context().Err()
	})}
	dockerSDK, err := client.New(client.WithHTTPClient(httpClient))
	require.NoError(t, err)
	t.Cleanup(func() { _ = dockerSDK.Close() })

	start := time.Now()
	removed := removeContainerForCleanup(
		context.Background(),
		dockerSDK,
		"blocked-container",
		client.ContainerRemoveOptions{Force: true},
		40*time.Millisecond,
	)

	assert.False(t, removed)
	assert.Less(t, time.Since(start), 200*time.Millisecond, "blocked cleanup request must respect its deadline")
}

func TestCleanupFailedHelperInstall_PreservesErrorsAndRetriesOnClose(t *testing.T) {
	t.Parallel()

	installErr := errors.New("install failed")
	removeErr := errors.New("remove failed")
	var removeCalls atomic.Int32
	httpClient := &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodDelete && removeCalls.Add(1) == 1 {
			return nil, removeErr
		}
		return dockerAPIResponse(http.StatusNoContent, ""), nil
	})}
	dockerSDK, err := client.New(client.WithHTTPClient(httpClient))
	require.NoError(t, err)

	cli := &Client{
		cfg:         &Config{AutoRemove: false},
		containerID: "created-container",
		cli:         dockerSDK,
	}
	cli.started.Store(true)

	err = cli.cleanupFailedHelperInstall(context.Background(), dockerSDK, "created-container", installErr)
	require.ErrorIs(t, err, installErr)
	require.ErrorIs(t, err, removeErr)
	assert.Equal(t, "created-container", cli.containerID, "failed removal must retain container ownership")

	cli.Close(context.Background())
	assert.Empty(t, cli.containerID, "Close must retry removal of the retained container")
	assert.Equal(t, int32(2), removeCalls.Load())
}
func TestNativeExecOptions_DoesNotRequireShellOrTmp(t *testing.T) {
	t.Parallel()

	opts := nativeExecOptions()
	assert.True(t, opts.TerminateOnCancel)

	got := execCommand(nil, []string{"/app/binary", "--flag"}, opts)
	assert.Equal(t, []string{"/app/binary", "--flag"}, got)
}

func TestDaemonNeedsHelperCopy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		daemonHost    string
		containerized bool
		want          bool
	}{
		{name: "local unix socket", daemonHost: "unix:///var/run/docker.sock"},
		{name: "local named pipe", daemonHost: "npipe:////./pipe/docker_engine"},
		{name: "mounted socket inside container", daemonHost: "unix:///var/run/docker.sock", containerized: true, want: true},
		{name: "remote tcp daemon", daemonHost: "tcp://docker.example:2376", want: true},
		{name: "remote ssh daemon", daemonHost: "ssh://docker.example", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, daemonNeedsHelperCopy(tt.daemonHost, tt.containerized))
		})
	}
}

func TestCreateContainerKeepAlive_StartsWhenCancellationHelperIsUnavailable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		startup      string
		containerCmd []string
		startCmd     []string
		wantCmd      []string
	}{
		{
			name:    "keepalive uses shell fallback",
			wantCmd: []string{"sh", "-c", "while true; do sleep 86400; done"},
		},
		{
			name:         "entrypoint preserves image command",
			startup:      "entrypoint",
			containerCmd: []string{"image-default"},
			wantCmd:      []string{"image-default"},
		},
		{
			name:     "command preserves configured startup command",
			startup:  "command",
			startCmd: []string{"serve", "--foreground"},
			wantCmd:  []string{"serve", "--foreground"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var createdConfig struct {
				Cmd []string `json:"Cmd"`
			}
			httpClient := &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				switch {
				case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/containers/create"):
					if err := json.NewDecoder(req.Body).Decode(&createdConfig); err != nil {
						return nil, fmt.Errorf("decode container create request: %w", err)
					}
					return dockerAPIResponse(http.StatusCreated, `{"Id":"ctr","Warnings":[]}`), nil
				case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/containers/ctr/start"):
					return dockerAPIResponse(http.StatusNoContent, ""), nil
				case req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/containers/ctr/json"):
					return dockerAPIResponse(http.StatusOK, `{"Id":"ctr","State":{"Running":true},"Config":{}}`), nil
				default:
					return nil, fmt.Errorf("unexpected Docker API request: %s %s", req.Method, req.URL.Path)
				}
			})}
			dockerSDK, err := client.New(
				client.WithHTTPClient(httpClient),
				client.WithAPIVersion("1.44"),
			)
			require.NoError(t, err)
			t.Cleanup(func() { _ = dockerSDK.Close() })

			cli := &Client{
				cfg: &Config{
					Image: "example:latest",
					Container: &container.Config{
						Image: "example:latest",
						Cmd:   tt.containerCmd,
					},
					Host:     &container.HostConfig{},
					Pull:     ir.PullPolicyNever,
					Startup:  tt.startup,
					StartCmd: tt.startCmd,
				},
				platform: specs.Platform{OS: "linux", Architecture: "mips"},
				cli:      dockerSDK,
			}

			require.NoError(t, cli.CreateContainerKeepAlive(context.Background()))
			assert.Equal(t, tt.wantCmd, createdConfig.Cmd)
		})
	}
}

// A helper that cannot be installed must not fail the run: the container is
// retried without it, falling back to the shell keepalive and in-container
// cancellation tooling.
func TestCreateContainerKeepAlive_RetriesWithoutHelper_WhenStartFails(t *testing.T) {
	t.Parallel()

	type createRequest struct {
		Cmd        []string `json:"Cmd"`
		HostConfig struct {
			Binds []string `json:"Binds"`
		} `json:"HostConfig"`
	}
	var creates []createRequest
	var removed atomic.Bool
	var starts atomic.Int32

	httpClient := &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/containers/create"):
			var got createRequest
			if err := json.NewDecoder(req.Body).Decode(&got); err != nil {
				return nil, fmt.Errorf("decode container create request: %w", err)
			}
			creates = append(creates, got)
			return dockerAPIResponse(http.StatusCreated, `{"Id":"ctr","Warnings":[]}`), nil
		case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/containers/ctr/start"):
			// The bind source resolved on this host is not visible to the daemon.
			if starts.Add(1) == 1 {
				return dockerAPIResponse(http.StatusInternalServerError,
					`{"message":"error mounting /__dagu_runner/keepalive: not a directory"}`), nil
			}
			return dockerAPIResponse(http.StatusNoContent, ""), nil
		case req.Method == http.MethodDelete && strings.HasSuffix(req.URL.Path, "/containers/ctr"):
			removed.Store(true)
			return dockerAPIResponse(http.StatusNoContent, ""), nil
		case req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/containers/ctr/json"):
			return dockerAPIResponse(http.StatusOK, `{"Id":"ctr","State":{"Running":true},"Config":{}}`), nil
		default:
			return nil, fmt.Errorf("unexpected Docker API request: %s %s", req.Method, req.URL.Path)
		}
	})}
	dockerSDK, err := client.New(
		client.WithHTTPClient(httpClient),
		client.WithAPIVersion("1.44"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = dockerSDK.Close() })

	cli := &Client{
		cfg: &Config{
			Image:     "example:latest",
			Container: &container.Config{Image: "example:latest"},
			Host:      &container.HostConfig{},
			Pull:      ir.PullPolicyNever,
		},
		platform: specs.Platform{OS: "linux", Architecture: "amd64"},
		cli:      dockerSDK,
	}

	require.NoError(t, cli.CreateContainerKeepAlive(context.Background()),
		"a helper that will not install must degrade, not fail the run")

	require.Len(t, creates, 2, "the container must be recreated once without the helper")
	assert.True(t, removed.Load(), "the container from the failed attempt must not be left behind")
	assert.Equal(t, []string{keepAliveTargetPath}, creates[0].Cmd)
	assert.Equal(t, []string{"sh", "-c", keepAliveSleepCmd}, creates[1].Cmd,
		"the retry has no helper binary, so it needs its own idle process")
	assert.Empty(t, creates[1].HostConfig.Binds, "the unusable helper bind must be dropped on retry")
	assert.Equal(t, cancelHelperNone, cli.cancelHelper)
	assert.Empty(t, cli.keepAliveTmp, "the host-side helper copy must be released")
}

func TestCgroupIndicatesContainer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{name: "cgroup v2 host with newline", content: "0::/\n", want: false},
		{name: "cgroup v2 root", content: "0::/", want: false},
		{name: "systemd user session", content: "0::/user.slice/user-1000.slice/session-2.scope\n", want: false},
		{name: "containerd host service", content: "0::/system.slice/containerd.service\n", want: false},
		{name: "docker host service", content: "0::/system.slice/docker.service\n", want: false},
		{name: "docker build host scope", content: "0::/system.slice/docker-build.scope\n", want: false},
		{name: "docker", content: "0::/system.slice/docker-deadbeefcafe.scope\n", want: true},
		{name: "docker cgroup v1", content: "10:memory:/docker/deadbeefcafe\n", want: true},
		{name: "containerd", content: "0::/containerd/deadbeefcafe\n", want: true},
		{name: "kubernetes", content: "0::/kubepods.slice/kubepods-burstable.slice\n", want: true},
		{name: "podman", content: "0::/machine.slice/libpod-deadbeefcafe.scope\n", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, cgroupIndicatesContainer(tt.content))
		})
	}
}

// The signal exec only has to reach a process the same user started, so it must
// not escalate beyond the user the step itself runs as.
func TestHelperSignalExecOptions_RunsAsStepUserWithoutPrivileges(t *testing.T) {
	t.Parallel()

	opts := helperSignalExecOptions("65534", "TERM", "token")
	assert.Equal(t, "65534", opts.User)
	assert.False(t, opts.Privileged)
	assert.Equal(t, []string{keepAliveTargetPath, "signal-token", "TERM", "token"}, opts.Cmd)
}

func TestNewExecCancelToken_ReturnsUniqueHex(t *testing.T) {
	t.Parallel()

	a := newExecCancelToken()
	b := newExecCancelToken()
	require.NotEmpty(t, a)
	require.NotEqual(t, a, b)
	_, err := hex.DecodeString(a)
	require.NoError(t, err, "token must stay shell-safe hex")
}
