// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package cmdutil

import (
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"
)

const parentExitWatcherOK = "ok"

// StartParentExitWatcher starts a small watchdog process that kills cmd's
// process group if this parent process dies before the returned stop function
// sends a normal-shutdown token.
func StartParentExitWatcher(cmd *exec.Cmd) (func(), error) {
	if cmd == nil || cmd.Process == nil {
		return func() {}, nil
	}

	readPipe, writePipe, err := os.Pipe()
	if err != nil {
		return nil, err
	}

	const script = `if IFS= read -r line <&3 && [ "$line" = "` + parentExitWatcherOK + `" ]; then exit 0; fi; kill -KILL -"$1" 2>/dev/null; exit 0`
	watcher := exec.Command("/bin/sh", "-c", script, "dagu-parent-exit-watcher", strconv.Itoa(cmd.Process.Pid)) //nolint:gosec
	watcher.ExtraFiles = []*os.File{readPipe}
	setupCommand(watcher)

	if err := watcher.Start(); err != nil {
		_ = readPipe.Close()
		_ = writePipe.Close()
		return nil, err
	}
	_ = readPipe.Close()

	done := make(chan error, 1)
	go func() {
		done <- watcher.Wait()
	}()

	var once sync.Once
	return func() {
		once.Do(func() {
			_, _ = writePipe.WriteString(parentExitWatcherOK + "\n")
			_ = writePipe.Close()

			select {
			case <-done:
			case <-time.After(2 * time.Second):
				_ = KillProcessGroup(watcher, os.Kill)
				select {
				case <-done:
				case <-time.After(time.Second):
				}
			}
		})
	}, nil
}
