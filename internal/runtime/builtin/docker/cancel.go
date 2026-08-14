// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package docker

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/containerd/errdefs"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/moby/moby/client"
)

var errContainerStopUnavailable = errors.New("docker client or container id is unavailable")

// waitUntilContainerStopped polls until the container is gone or not running.
// If ctx is canceled while the container is still running, stop is called once
// so timeout_sec cannot hang in Client.Run's post-wait join.
const defaultCancelStopWait = 10 * time.Second

func waitUntilContainerStopped(
	ctx context.Context,
	inspect func(context.Context) (running bool, notFound bool, err error),
	stop func() error,
	poll time.Duration,
) error {
	return waitUntilContainerStoppedWithGrace(ctx, inspect, stop, poll, defaultCancelStopWait)
}

func waitUntilContainerStoppedWithGrace(
	ctx context.Context,
	inspect func(context.Context) (running bool, notFound bool, err error),
	stop func() error,
	poll time.Duration,
	maxAfterCancel time.Duration,
) error {
	if poll <= 0 {
		poll = defaultPollInterval
	}
	if maxAfterCancel <= 0 {
		maxAfterCancel = defaultCancelStopWait
	}

	var (
		stopOnce   sync.Once
		stopErr    error
		canceledAt time.Time
	)
	requestStop := func() {
		stopOnce.Do(func() {
			if stop == nil {
				return
			}
			stopErr = stop()
		})
	}

	for {
		if err := ctx.Err(); err != nil {
			if canceledAt.IsZero() {
				canceledAt = time.Now()
			}
			requestStop()
			if stopErr != nil {
				return fmt.Errorf("stop container after cancel: %w", stopErr)
			}
		}

		running, notFound, err := inspect(context.Background())
		if err != nil {
			return err
		}
		if notFound || !running {
			return nil
		}

		if !canceledAt.IsZero() && time.Since(canceledAt) >= maxAfterCancel {
			return fmt.Errorf("container still running after cancel: %w", ctx.Err())
		}

		time.Sleep(poll)
	}
}

func nativeExecOptions(stepName string) ExecOptions {
	safeStepName := fileutil.SafeName(stepName)
	if safeStepName == "" {
		safeStepName = "step"
	}
	return ExecOptions{
		TerminateOnCancel: true,
		PIDFile:           fmt.Sprintf("/tmp/dagu-exec-%s-%d.pid", safeStepName, time.Now().UnixNano()),
	}
}

// stopContainerByID force-stops a container by ID. Unlike Client.Stop this
// does not no-op when started is false, so the cancel join cannot hang.
func stopContainerByID(cli *client.Client, containerID string) error {
	if cli == nil || containerID == "" {
		return errContainerStopUnavailable
	}
	_, err := cli.ContainerStop(context.Background(), containerID, client.ContainerStopOptions{Signal: "SIGKILL"})
	if err != nil && errdefs.IsNotFound(err) {
		return nil
	}
	return err
}
