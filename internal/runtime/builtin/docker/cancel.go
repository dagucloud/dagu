// SPDX-License-Identifier: GPL-3.0-or-later

package docker

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/containerd/errdefs"
	"github.com/moby/moby/client"
)

var errContainerStopUnavailable = errors.New("docker client or container id is unavailable")

const defaultCancelStopWait = 10 * time.Second

// waitUntilContainerStopped polls until the container is gone or not running.
// If ctx is canceled while the container is still running, stop is called once
// so timeout_sec cannot hang in Client.Run's post-wait join.
func waitUntilContainerStopped(
	ctx context.Context,
	inspect func(context.Context) (running bool, notFound bool, err error),
	stop func(context.Context) error,
	poll time.Duration,
) error {
	return waitUntilContainerStoppedWithGrace(ctx, inspect, stop, poll, defaultCancelStopWait)
}

func waitUntilContainerStoppedWithGrace(
	ctx context.Context,
	inspect func(context.Context) (running bool, notFound bool, err error),
	stop func(context.Context) error,
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
		stopOnce      sync.Once
		stopErr       error
		canceledAt    time.Time
		cleanupCtx    = context.Background()
		cleanupCancel context.CancelFunc
	)
	defer func() {
		if cleanupCancel != nil {
			cleanupCancel()
		}
	}()
	requestStop := func() {
		stopOnce.Do(func() {
			if stop == nil {
				return
			}
			stopErr = stop(cleanupCtx)
		})
	}

	for {
		if err := ctx.Err(); err != nil {
			if canceledAt.IsZero() {
				canceledAt = time.Now()
				cleanupCtx, cleanupCancel = context.WithTimeout(context.Background(), maxAfterCancel)
			}
			requestStop()
			if stopErr != nil {
				return fmt.Errorf("stop container after cancel: %w", stopErr)
			}
		}

		running, notFound, err := inspect(cleanupCtx)
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

func nativeExecOptions() ExecOptions {
	return ExecOptions{TerminateOnCancel: true}
}

// stopContainerByID force-stops a container by ID. Unlike Client.Stop this
// does not no-op when started is false, so the cancel join cannot hang.
func stopContainerByID(ctx context.Context, cli *client.Client, containerID string) error {
	if cli == nil || containerID == "" {
		return errContainerStopUnavailable
	}
	if ctx == nil {
		ctx = context.Background()
	}
	_, err := cli.ContainerStop(ctx, containerID, client.ContainerStopOptions{Signal: "SIGKILL"})
	if err != nil && errdefs.IsNotFound(err) {
		return nil
	}
	return err
}
