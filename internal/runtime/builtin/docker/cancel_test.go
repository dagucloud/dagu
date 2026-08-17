// SPDX-License-Identifier: GPL-3.0-or-later

package docker

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moby/moby/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

func TestNewExecCancelToken_ReturnsUniqueHex(t *testing.T) {
	t.Parallel()

	a := newExecCancelToken()
	b := newExecCancelToken()
	require.NotEmpty(t, a)
	require.NotEqual(t, a, b)
	_, err := hex.DecodeString(a)
	require.NoError(t, err, "token must stay shell-safe hex")
}
