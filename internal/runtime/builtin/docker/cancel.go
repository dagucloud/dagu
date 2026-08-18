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

const (
	defaultCancelStopWait     = 10 * time.Second
	defaultContainerStopGrace = 5 * time.Second
)

// waitUntilContainerStopped polls until the container is gone or not running.
// If ctx is canceled while the container is still running, stop is called once
// and the container is then awaited for at most maxAfterCancel.
func waitUntilContainerStopped(
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
			return fmt.Errorf("stop container after cancel: %w", errors.Join(ctx.Err(), err))
		}
	}

	for {
		running, notFound, err := inspect(cleanupCtx)
		if err != nil {
			if cleanupCtx.Err() != nil {
				return fmt.Errorf("container cleanup after cancel: %w", errors.Join(ctx.Err(), cleanupCtx.Err(), err))
			}
			return fmt.Errorf("inspect container after cancel: %w", errors.Join(ctx.Err(), err))
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

// stopContainer stops a container and waits for it to leave the running state.
// It sends signal (empty selects the daemon default), allows stopGrace for the
// process to exit, then escalates to SIGKILL. A container that is already gone
// or stopped is not an error. Cleanup runs on its own cleanupWait deadline so it
// completes after the caller's context has been canceled.
func stopContainer(
	cli *client.Client,
	containerID string,
	signal string,
	stopGrace time.Duration,
	cleanupWait time.Duration,
) error {
	if cli == nil || containerID == "" {
		return errContainerStopUnavailable
	}
	if stopGrace <= 0 {
		stopGrace = defaultContainerStopGrace
	}
	if cleanupWait <= 0 {
		cleanupWait = defaultCancelStopWait
	}

	cleanupCtx, cancel := context.WithTimeout(context.Background(), cleanupWait)
	defer cancel()
	info, err := inspectContainer(cleanupCtx, cli, containerID)
	if err != nil {
		if errdefs.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("inspect container before stop: %w", err)
	}
	if info.State != nil && !info.State.Running {
		return nil
	}

	stopCtx, stopCancel := context.WithTimeout(cleanupCtx, stopGrace)
	_, stopErr := cli.ContainerStop(stopCtx, containerID, client.ContainerStopOptions{Signal: signal})
	stopCancel()
	if stopErr == nil || errdefs.IsNotFound(stopErr) {
		return nil
	}
	if !errors.Is(stopErr, context.Canceled) && !errors.Is(stopErr, context.DeadlineExceeded) {
		return stopErr
	}

	info, inspectErr := inspectContainer(cleanupCtx, cli, containerID)
	if inspectErr != nil {
		if errdefs.IsNotFound(inspectErr) {
			return nil
		}
	}
	if inspectErr == nil && info.State != nil && !info.State.Running {
		return nil
	}

	if _, err := cli.ContainerKill(cleanupCtx, containerID, client.ContainerKillOptions{Signal: "SIGKILL"}); err != nil {
		if errdefs.IsNotFound(err) || errdefs.IsConflict(err) {
			return nil
		}
		return fmt.Errorf("force stop container: %w", errors.Join(stopErr, inspectErr, err))
	}

	for {
		info, err := inspectContainer(cleanupCtx, cli, containerID)
		if err != nil {
			if errdefs.IsNotFound(err) {
				return nil
			}
			return fmt.Errorf("inspect container after force stop: %w", errors.Join(stopErr, inspectErr, err))
		}
		if info.State != nil && !info.State.Running {
			return nil
		}
		if err := waitForContainerPoll(cleanupCtx, defaultPollInterval); err != nil {
			return fmt.Errorf("container still running after force stop: %w", errors.Join(stopErr, inspectErr, err))
		}
	}
}
