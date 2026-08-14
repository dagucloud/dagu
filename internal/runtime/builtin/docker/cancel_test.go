// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package docker

import (
	"context"
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
	stop := func() error {
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
		func() error {
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
		func() error { t.Fatal("stop must not run when inspect reports not found"); return nil },
		10*time.Millisecond,
	)

	require.NoError(t, err)
}

func TestWaitUntilContainerStopped_ReturnsInspectError(t *testing.T) {
	t.Parallel()

	want := errors.New("inspect failed")
	err := waitUntilContainerStopped(context.Background(),
		func(context.Context) (bool, bool, error) { return false, false, want },
		func() error { return nil },
		10*time.Millisecond,
	)

	require.ErrorIs(t, err, want)
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
		func() error { t.Fatal("stop must not run while ctx is active"); return nil },
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

func TestNativeExecOptions_UsesFallbackName_WhenStepNameEmpty(t *testing.T) {
	t.Parallel()

	opts := nativeExecOptions("")
	assert.True(t, opts.TerminateOnCancel)
	assert.Contains(t, opts.PIDFile, "dagu-exec-step-")
}

func TestNativeExecOptions_TerminatesProcessOnCancel(t *testing.T) {
	t.Parallel()

	opts := nativeExecOptions("build-step")
	assert.True(t, opts.TerminateOnCancel, "shared-container execs must kill the step process on timeout")
	assert.NotEmpty(t, opts.PIDFile, "TerminateOnCancel needs a pid file so only the exec is killed")
	assert.Contains(t, opts.PIDFile, "build-step")
}
