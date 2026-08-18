// SPDX-License-Identifier: GPL-3.0-or-later

package docker

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
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
		done <- waitUntilContainerStopped(ctx, inspect, stop, 10*time.Millisecond)
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
	)

	require.NoError(t, err)
}

func TestWaitUntilContainerStopped_Returns_WhenStopIsNoOpAndContainerKeepsRunning(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan error, 1)
	go func() {
		done <- waitUntilContainerStoppedWithGrace(ctx,
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

	err := waitUntilContainerStoppedWithGrace(ctx,
		func(context.Context) (bool, bool, error) { return true, false, nil },
		func(context.Context) error { return want },
		10*time.Millisecond,
		time.Second,
	)

	require.ErrorIs(t, err, want)
}

func TestWaitUntilContainerStopped_ReturnsInspectError(t *testing.T) {
	t.Parallel()

	want := errors.New("inspect failed")
	err := waitUntilContainerStopped(context.Background(),
		func(context.Context) (bool, bool, error) { return false, false, want },
		func(context.Context) error { return nil },
		10*time.Millisecond,
	)

	require.ErrorIs(t, err, want)
}

func TestWaitUntilContainerStopped_CancelsStopAndInspect_WhenTheyBlock(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan error, 1)
	go func() {
		done <- waitUntilContainerStoppedWithGrace(ctx,
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
		done <- waitUntilContainerStoppedWithGrace(ctx,
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

func TestClientExec_StopsStepProcess_WhenContextCanceled(t *testing.T) {
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
	_, runErr := cli.Exec(runCtx, []string{"sleep", "60"}, io.Discard, io.Discard, nativeExecOptions())
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

func TestClientExec_CleansUpWhenCancellationRacesWithAttach(t *testing.T) {
	t.Parallel()

	attachStarted := make(chan struct{})
	releaseAttach := make(chan struct{})
	cleanupStarted := make(chan struct{})
	var createdExecs atomic.Int32
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
			execStopped.Store(true)
			close(cleanupStarted)
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

func TestClientExec_StopsStepProcess_WhenPIDFileAndCanceled(t *testing.T) {
	dockerSDK := newDockerSDKOrSkip(t)
	t.Cleanup(func() { _ = dockerSDK.Close() })

	pullCtx, pullCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer pullCancel()
	pullImageOrSkip(t, dockerSDK, pullCtx, "alpine:latest")

	name := fmt.Sprintf("dagu-exec-pidfile-timeout-%d", time.Now().UnixNano())
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
	_, runErr := cli.Exec(runCtx, []string{"sleep", "60"}, io.Discard, io.Discard, ExecOptions{
		Direct:            true,
		TerminateOnCancel: true,
		PIDFile:           fmt.Sprintf("/tmp/dagu-pidfile-test-%d.pid", time.Now().UnixNano()),
	})
	elapsed := time.Since(start)

	require.Less(t, elapsed, 15*time.Second, "pid-file exec must return on timeout (took %s, err=%v)", elapsed, runErr)
	require.Error(t, runErr)
	assertNoProcessInContainer(t, dockerSDK, name, "sleep 60")
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

func newDockerSDKOrSkip(t *testing.T) *client.Client {
	t.Helper()

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
	)

	require.NoError(t, err)
}

func TestStopContainerByID_ReturnsUnavailable_WhenClientOrIDMissing(t *testing.T) {
	t.Parallel()

	require.ErrorIs(t, stopContainerByID(context.Background(), nil, "ctr"), errContainerStopUnavailable)
	cli := &client.Client{}
	require.ErrorIs(t, stopContainerByID(context.Background(), cli, ""), errContainerStopUnavailable)
}

func TestStopContainerByID_IgnoresMissingContainer(t *testing.T) {
	dockerSDK := newDockerSDKOrSkip(t)
	t.Cleanup(func() { _ = dockerSDK.Close() })

	err := stopContainerByID(context.Background(), dockerSDK, "dagu-no-such-container")
	require.NoError(t, err)
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
	removed := removeContainerForCleanupWithTimeout(
		context.Background(),
		dockerSDK,
		"blocked-container",
		client.ContainerRemoveOptions{Force: true},
		40*time.Millisecond,
	)

	assert.False(t, removed)
	assert.Less(t, time.Since(start), 200*time.Millisecond, "blocked cleanup request must respect its deadline")
}

func TestWrapCommandWithPIDFile_ExecsUserProcess(t *testing.T) {
	t.Parallel()

	got := wrapCommandWithPIDFile([]string{"sleep", "60"}, "/tmp/dagu/pid")
	require.GreaterOrEqual(t, len(got), 5)
	assert.Equal(t, []string{"/bin/sh", "-c"}, got[:2], "wrapper must use /bin/sh so PATH-less images still find it")
	assert.Contains(t, got[2], `exec "$@"`, "exec keeps the user process as the recorded PID so SIGTERM hits it")
	assert.NotContains(t, got[2], `"$@" &`, "backgrounding the user process orphans it from the exec on Alpine")
	assert.Equal(t, "dagu-exec-wrapper", got[3])
	assert.Equal(t, "/tmp/dagu/pid", got[4])
	assert.Equal(t, []string{"sleep", "60"}, got[5:])
}

func TestNativeExecOptions_DoesNotRequireShellOrTmp(t *testing.T) {
	t.Parallel()

	opts := nativeExecOptions()
	assert.True(t, opts.TerminateOnCancel)
	assert.Empty(t, opts.PIDFile)

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

func TestHelperSignalExecOptions_EnforcesTrustBoundary(t *testing.T) {
	t.Parallel()

	bound := helperSignalExecOptions(cancelHelperBound, "65534", "TERM", "token")
	assert.Equal(t, "0", bound.User)
	assert.True(t, bound.Privileged)
	assert.Equal(t, []string{keepAliveTargetPath, "signal-token", "TERM", "token"}, bound.Cmd)

	copied := helperSignalExecOptions(cancelHelperCopied, "65534", "KILL", "token")
	assert.Equal(t, "65534", copied.User)
	assert.False(t, copied.Privileged)
	assert.Equal(t, []string{keepAliveTargetPath, "signal-token", "KILL", "token"}, copied.Cmd)
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
