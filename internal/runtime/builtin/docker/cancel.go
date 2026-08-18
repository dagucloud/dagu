// SPDX-License-Identifier: GPL-3.0-or-later

package docker

import (
	"context"
	"errors"
	"fmt"
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

	for {
		if err := ctx.Err(); err != nil {
			return stopAndWaitForContainer(ctx, inspect, stop, poll, maxAfterCancel)
		}

		running, notFound, err := inspect(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return stopAndWaitForContainer(ctx, inspect, stop, poll, maxAfterCancel)
			}
			return err
		}
		if notFound || !running {
			return nil
		}

		if err := waitForContainerPoll(ctx, poll); err != nil {
			return stopAndWaitForContainer(ctx, inspect, stop, poll, maxAfterCancel)
		}
	}
}

func stopAndWaitForContainer(
	ctx context.Context,
	inspect func(context.Context) (running bool, notFound bool, err error),
	stop func(context.Context) error,
	poll time.Duration,
	maxAfterCancel time.Duration,
) error {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), maxAfterCancel)
	defer cancel()

	if stop != nil {
		if err := stop(cleanupCtx); err != nil {
			return fmt.Errorf("stop container after cancel: %w", err)
		}
	}

	for {
		running, notFound, err := inspect(cleanupCtx)
		if err != nil {
			if cleanupCtx.Err() != nil {
				return fmt.Errorf("container cleanup after cancel: %w", errors.Join(ctx.Err(), cleanupCtx.Err()))
			}
			return err
		}
		if notFound || !running {
			return nil
		}

		if err := waitForContainerPoll(cleanupCtx, poll); err != nil {
			return fmt.Errorf("container still running after cancel: %w", errors.Join(ctx.Err(), err))
		}
	}
}

func waitForContainerPoll(ctx context.Context, poll time.Duration) error {
	timer := time.NewTimer(poll)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
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
