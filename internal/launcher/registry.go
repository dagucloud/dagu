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
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/cmdutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
)

// processRegistryKey is the context key carrying a *ProcessRegistry.
type processRegistryKey struct{}

// ProcessRegistry tracks subprocesses launched through this package so a
// supervising process (server, scheduler, start-all) can wait for them to
// finish when it stops, and forward shutdown signals to them when
// signal_handling.enable_propagation is enabled.
type ProcessRegistry struct {
	mu    sync.Mutex
	procs map[*exec.Cmd]chan struct{}
	done  chan struct{}
	// idle closes when the last tracked process exits; nil until a caller
	// waits for that.
	idle chan struct{}
	// stopped closes once StopRuns has ended its work; nil before it runs.
	stopped        chan struct{}
	forwardSignals bool
}

// RegistryOption configures a ProcessRegistry.
type RegistryOption func(*ProcessRegistry)

// WithoutSignalForwarding makes a registry track its subprocesses for a
// supervisor to wait on, and leave them running when the supervisor stops.
func WithoutSignalForwarding() RegistryOption {
	return func(r *ProcessRegistry) { r.forwardSignals = false }
}

// NewProcessRegistry returns an empty ProcessRegistry, which forwards shutdown
// signals unless an option says otherwise.
func NewProcessRegistry(opts ...RegistryOption) *ProcessRegistry {
	r := &ProcessRegistry{procs: make(map[*exec.Cmd]chan struct{}), forwardSignals: true}
	for _, opt := range opts {
		opt(r)
	}
	return r
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
// the supervisor neither drains nor signals its subprocesses.
func ProcessRegistryFrom(ctx context.Context) *ProcessRegistry {
	if ctx == nil {
		return nil
	}
	reg, _ := ctx.Value(processRegistryKey{}).(*ProcessRegistry)
	return reg
}

// StopRuns ends the subprocesses tracked in ctx for a supervisor stopping on
// sig. It waits up to drain for them to exit, then forwards SIGINT or SIGTERM
// to those left when the registry forwards signals. The returned channel
// closes once the forwarded signal has ended them, or once the drain ends when
// none is forwarded. Every call on a registry returns the channel of the
// first. Without a registry, the returned channel is already closed.
func StopRuns(ctx context.Context, sig os.Signal, drain time.Duration) <-chan struct{} {
	reg := ProcessRegistryFrom(ctx)
	if reg == nil {
		return closedChannel()
	}
	reg.mu.Lock()
	if reg.stopped != nil {
		stopped := reg.stopped
		reg.mu.Unlock()
		return stopped
	}
	stopped := make(chan struct{})
	reg.stopped = stopped
	reg.mu.Unlock()

	if drain <= 0 {
		// The signal goes out before the caller stops its services.
		propagated := reg.forward(ctx, sig)
		select {
		case <-propagated:
			close(stopped)
		default:
			go func() {
				<-propagated
				close(stopped)
			}()
		}
		return stopped
	}
	go func() {
		defer close(stopped)
		reg.drain(ctx, drain)
		<-reg.forward(ctx, sig)
	}()
	return stopped
}

// forward forwards SIGINT or SIGTERM to the tracked subprocesses when the
// registry forwards signals.
func (r *ProcessRegistry) forward(ctx context.Context, sig os.Signal) <-chan struct{} {
	if r.forwardSignals && (sig == syscall.SIGINT || sig == syscall.SIGTERM) {
		return r.Propagate(ctx, sig)
	}
	return closedChannel()
}

// drain waits up to timeout for every tracked subprocess to exit, including
// ones that start meanwhile.
func (r *ProcessRegistry) drain(ctx context.Context, timeout time.Duration) {
	idle := r.Idle()
	select {
	case <-idle:
		return
	default:
	}
	logger.Info(ctx, "Waiting for running DAG processes to finish",
		slog.Int("processes", r.running()),
		slog.Duration("timeout", timeout),
	)
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-idle:
		logger.Info(ctx, "Running DAG processes finished")
	case <-timer.C:
		logger.Warn(ctx, "DAG processes still running after the drain timeout",
			slog.Int("processes", r.running()),
		)
	}
}

// Idle returns a channel that closes once no tracked subprocess is running.
func (r *ProcessRegistry) Idle() <-chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.procs) == 0 {
		return closedChannel()
	}
	if r.idle == nil {
		r.idle = make(chan struct{})
	}
	return r.idle
}

func (r *ProcessRegistry) running() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.procs)
}

func closedChannel() <-chan struct{} {
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
		if len(reg.procs) == 0 && reg.idle != nil {
			close(reg.idle)
			reg.idle = nil
		}
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
