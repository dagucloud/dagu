// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package wait

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	cmnconfig "github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/executor/registry"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
)

const (
	executorType = "wait"

	opDuration = "duration"
	opUntil    = "until"
	opFile     = "file"
	opHTTP     = "http"

	stateExists  = "exists"
	stateMissing = "missing"
)

var _ executor.Executor = (*executorImpl)(nil)

type executorImpl struct {
	mu      sync.Mutex
	stdout  io.Writer
	stderr  io.Writer
	op      string
	cfg     parsedConfig
	workDir string
	kill    context.Context
	cancel  context.CancelFunc
}

func newExecutor(ctx context.Context, step ir.Step) (executor.Executor, error) {
	cfg := defaultConfig()
	if err := decodeConfig(step.ExecutorConfig.Config, &cfg); err != nil {
		return nil, err
	}

	op := stepOperation(step)
	parsed, err := parseConfig(op, cfg, false)
	if err != nil {
		return nil, err
	}

	kill, cancel := context.WithCancel(ctx)
	env := runtime.GetEnv(ctx)
	return &executorImpl{
		stdout:  os.Stdout,
		stderr:  os.Stderr,
		op:      op,
		cfg:     parsed,
		workDir: env.WorkingDir,
		kill:    kill,
		cancel:  cancel,
	}, nil
}

func validateStep(step ir.Step) error {
	if step.ExecutorConfig.Type != executorType {
		return nil
	}
	cfg := defaultConfig()
	if err := decodeConfig(step.ExecutorConfig.Config, &cfg); err != nil {
		return err
	}
	_, err := parseConfig(stepOperation(step), cfg, true)
	return err
}

func stepOperation(step ir.Step) string {
	if len(step.Commands) == 0 {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(step.Commands[0].Command))
}

func (e *executorImpl) SetStdout(out io.Writer) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.stdout = out
}

func (e *executorImpl) SetStderr(out io.Writer) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.stderr = out
}

func (e *executorImpl) Kill(_ os.Signal) error {
	e.cancel()
	return nil
}

func (e *executorImpl) Run(ctx context.Context) error {
	ctx, stop := e.runContext(ctx)
	defer stop()

	switch e.op {
	case opDuration:
		return waitFor(ctx, e.cfg.duration)
	case opUntil:
		return e.waitUntil(ctx)
	case opFile:
		return e.waitFile(ctx)
	case opHTTP:
		return e.waitHTTP(ctx)
	default:
		return fmt.Errorf("wait: unsupported operation %q", e.op)
	}
}

func (e *executorImpl) runContext(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	go func() {
		select {
		case <-e.kill.Done():
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}

func waitFor(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()

	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (e *executorImpl) waitUntil(ctx context.Context) error {
	deadline := e.deadline(ctx)
	e.logDeadline(deadline)
	switch e.cfg.ifPast {
	case ifPastFail:
		if !time.Now().Before(deadline) {
			return fmt.Errorf("wait: until %q resolved to %s which is in the past", e.cfg.Until, deadline.Format(time.RFC3339Nano))
		}
	case ifPastWindow:
		if time.Since(deadline) > e.cfg.window {
			return fmt.Errorf("wait: until %q resolved to %s outside window %s", e.cfg.Until, deadline.Format(time.RFC3339Nano), e.cfg.Window)
		}
	}
	return waitUntil(ctx, deadline)
}

func waitUntil(ctx context.Context, until time.Time) error {
	duration := time.Until(until)
	if duration <= 0 {
		return ctx.Err()
	}
	return waitFor(ctx, duration)
}

// deadline returns the instant with.until and with.offset resolve to.
func (e *executorImpl) deadline(ctx context.Context) time.Time {
	if e.cfg.clock == nil {
		return e.cfg.until.Add(e.cfg.offset)
	}
	loc := e.cfg.location
	if loc == nil {
		loc = configuredLocation(ctx)
	}
	return clockDeadline(logicalDay(ctx, loc), *e.cfg.clock, loc, e.cfg.offset)
}

// clockDeadline maps clock onto base's day in loc and applies offset. A
// deadline that already passed stays on that day for if_past to handle.
func clockDeadline(base time.Time, clock clockTime, loc *time.Location, offset time.Duration) time.Time {
	return wallClock(base, clock, loc).Add(offset)
}

// wallClock returns the instant loc's clocks read clock on day. As in RFC
// 5545, a time skipped by a daylight-saving transition is read with the UTC
// offset before the gap, and a time repeated by one resolves to its first
// occurrence.
func wallClock(day time.Time, clock clockTime, loc *time.Location) time.Time {
	t := time.Date(day.Year(), day.Month(), day.Day(), clock.hour, clock.minute, clock.second, 0, loc)
	wall := time.Date(day.Year(), day.Month(), day.Day(), clock.hour, clock.minute, clock.second, 0, time.UTC)
	// time.Date leaves the zone ambiguous near a transition, and the one that
	// affects wall is a bound of the zone it picked.
	start, end := t.ZoneBounds()
	for _, transition := range []time.Time{start, end} {
		if transition.IsZero() {
			continue
		}
		_, before := transition.Add(-time.Nanosecond).Zone()
		_, after := transition.Zone()
		from := transition.UTC().Add(time.Duration(min(before, after)) * time.Second)
		to := transition.UTC().Add(time.Duration(max(before, after)) * time.Second)
		if !wall.Before(from) && wall.Before(to) {
			return wall.Add(-time.Duration(before) * time.Second).In(loc)
		}
	}
	return t
}

// logicalDay returns the run's logical day in loc: the schedule time when
// present, otherwise when the current attempt started, otherwise now.
func logicalDay(ctx context.Context, loc *time.Location) time.Time {
	rCtx := runtime.GetDAGContext(ctx)
	for _, raw := range []string{rCtx.ScheduleTime, rCtx.RunStartedAt} {
		if t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(raw)); err == nil {
			return t.In(loc)
		}
	}
	return time.Now().In(loc)
}

func (e *executorImpl) logDeadline(deadline time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	zone, _ := deadline.Zone()
	_, _ = fmt.Fprintf(e.stderr, "wait: deadline %s (zone %s, if_past %s)\n",
		deadline.Format(time.RFC3339Nano), zone, e.cfg.ifPast)
}

// configuredLocation returns the run-configured zone (core.tz) when the
// executor context carries it, falling back to the process local zone.
func configuredLocation(ctx context.Context) *time.Location {
	if loc := cmnconfig.GetConfig(ctx).Core.Location; loc != nil {
		return loc
	}
	return time.Local
}

func (e *executorImpl) waitFile(ctx context.Context) error {
	return poll(ctx, e.cfg.pollInterval, func() (bool, error) {
		path := e.resolvePath(e.cfg.Path)
		_, err := os.Stat(path)
		switch e.cfg.State {
		case stateExists:
			if err == nil {
				return true, nil
			}
			if errors.Is(err, os.ErrNotExist) {
				return false, nil
			}
			return false, err
		case stateMissing:
			if errors.Is(err, os.ErrNotExist) {
				return true, nil
			}
			if err == nil {
				return false, nil
			}
			return false, err
		default:
			return false, fmt.Errorf("wait: unsupported file state %q", e.cfg.State)
		}
	})
}

func (e *executorImpl) waitHTTP(ctx context.Context) error {
	client := &http.Client{Timeout: e.cfg.requestTimeout}
	return poll(ctx, e.cfg.pollInterval, func() (bool, error) {
		req, err := http.NewRequestWithContext(ctx, strings.ToUpper(e.cfg.Method), e.cfg.URL, strings.NewReader(e.cfg.Body))
		if err != nil {
			return false, err
		}
		for key, value := range e.cfg.Headers {
			req.Header.Set(key, value)
		}

		resp, err := client.Do(req)
		if err != nil {
			return false, nil
		}
		defer func() { _ = resp.Body.Close() }()
		_, _ = io.Copy(io.Discard, resp.Body)
		return resp.StatusCode == e.cfg.expectedStatus, nil
	})
}

func (e *executorImpl) resolvePath(path string) string {
	if filepath.IsAbs(path) || e.workDir == "" {
		return path
	}
	return filepath.Join(e.workDir, path)
}

func poll(ctx context.Context, interval time.Duration, check func() (bool, error)) error {
	for {
		ok, err := check()
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		if err := waitFor(ctx, interval); err != nil {
			return err
		}
	}
}

func init() {
	executor.RegisterExecutor(executorType, newExecutor, validateStep, registry.ExecutorCapabilities{
		Command:    true,
		PathInputs: []string{"path"},
	})
}
