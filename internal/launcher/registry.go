// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package launcher

import (
	"context"
	"log/slog"
	"maps"
	"os"
	"os/exec"
	"sync"
	"syscall"

	"github.com/dagucloud/dagu/v2/internal/cmn/cmdutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
)

// processRegistryKey is the context key carrying a *ProcessRegistry.
type processRegistryKey struct{}

// ProcessRegistry tracks subprocesses launched through this package so a
// supervising process (server, scheduler, start-all) can forward shutdown
// signals to them when signal_handling.enable_propagation is enabled.
type ProcessRegistry struct {
	mu    sync.Mutex
	procs map[*exec.Cmd]chan struct{}
	done  chan struct{}
}

// NewProcessRegistry returns an empty ProcessRegistry.
func NewProcessRegistry() *ProcessRegistry {
	return &ProcessRegistry{procs: make(map[*exec.Cmd]chan struct{})}
}

// ContextWithProcessRegistry attaches reg to ctx so launcher entry points can
// register subprocesses they start.
func ContextWithProcessRegistry(ctx context.Context, reg *ProcessRegistry) context.Context {
	if reg == nil {
		return ctx
	}
	return context.WithValue(ctx, processRegistryKey{}, reg)
}

// ProcessRegistryFrom returns the ProcessRegistry attached to ctx, or nil when
// signal propagation is not enabled for this context.
func ProcessRegistryFrom(ctx context.Context) *ProcessRegistry {
	if ctx == nil {
		return nil
	}
	reg, _ := ctx.Value(processRegistryKey{}).(*ProcessRegistry)
	return reg
}

// PropagateSignal forwards SIGINT or SIGTERM to tracked subprocesses. The
// returned channel closes after those processes exit. Without a registry or
// for any other signal, the returned channel is already closed.
func PropagateSignal(ctx context.Context, sig os.Signal) <-chan struct{} {
	if sig == syscall.SIGINT || sig == syscall.SIGTERM {
		if reg := ProcessRegistryFrom(ctx); reg != nil {
			return reg.Propagate(ctx, sig)
		}
	}
	done := make(chan struct{})
	close(done)
	return done
}

// startTracked starts cmd and enrolls it before shutdown can take its snapshot.
// The returned function records process exit. Runs started after the snapshot
// remain outside the shutdown operation.
func startTracked(ctx context.Context, cmd *exec.Cmd) (func(), error) {
	reg := ProcessRegistryFrom(ctx)
	if reg != nil {
		reg.mu.Lock()
		defer reg.mu.Unlock()
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	if reg == nil || reg.done != nil {
		return func() {}, nil
	}
	done := make(chan struct{})
	reg.procs[cmd] = done
	return func() {
		reg.mu.Lock()
		delete(reg.procs, cmd)
		close(done)
		reg.mu.Unlock()
	}, nil
}

// Propagate forwards sig to each currently tracked subprocess's process tree.
// The returned channel closes after all of those processes exit, regardless of
// context cancellation. Later calls return the same channel without signaling
// again. Processes registered afterward are excluded. Per-process errors are
// logged and do not stop propagation to the remaining processes.
func (r *ProcessRegistry) Propagate(ctx context.Context, sig os.Signal) <-chan struct{} {
	r.mu.Lock()
	if r.done != nil {
		done := r.done
		r.mu.Unlock()
		return done
	}
	done := make(chan struct{})
	r.done = done
	procs := maps.Clone(r.procs)
	r.mu.Unlock()

	if len(procs) == 0 {
		close(done)
		return done
	}
	intent := cmdutil.TerminationFromSignal(sig)
	logger.Info(ctx, "Propagating signal to running DAG processes",
		tag.Signal(intent.SignalName()),
		slog.Int("processes", len(procs)),
	)
	for cmd := range procs {
		// cmd.Process is immutable after Start; ProcessState is written by
		// Wait and must not be read here. Processes that exited but are not
		// yet reaped simply fail with ESRCH, which is logged below.
		if cmd == nil || cmd.Process == nil {
			continue
		}
		if err := cmdutil.TerminateProcessGroup(cmd, intent); err != nil {
			logger.Warn(ctx, "Failed to propagate signal to DAG process",
				tag.PID(cmd.Process.Pid),
				tag.Signal(intent.SignalName()),
				tag.Error(err),
			)
		}
	}
	go func() {
		for _, exited := range procs {
			<-exited
		}
		close(done)
	}()
	return done
}
