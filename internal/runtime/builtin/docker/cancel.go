// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package docker

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
)

// waitUntilContainerStopped polls until the container is gone or not running.
// If ctx is canceled while the container is still running, stop is called once
// so timeout_sec cannot hang in Client.Run's post-wait join.
func waitUntilContainerStopped(
	ctx context.Context,
	inspect func(context.Context) (running bool, notFound bool, err error),
	stop func() error,
	poll time.Duration,
) error {
	if poll <= 0 {
		poll = defaultPollInterval
	}

	var stopOnce sync.Once
	requestStop := func() {
		stopOnce.Do(func() {
			if stop != nil {
				_ = stop()
			}
		})
	}

	for {
		if ctx.Err() != nil {
			requestStop()
		}

		running, notFound, err := inspect(context.Background())
		if err != nil {
			return err
		}
		if notFound || !running {
			return nil
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
