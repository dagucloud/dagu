// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package samples

import (
	"context"
	"errors"
	"time"
)

// ErrNotImplemented is returned by the functions of this package that a later
// change fills in.
var ErrNotImplemented = errors.New("samples: not implemented")

// ErrInvalidCursor reports an events cursor that the store did not make.
var ErrInvalidCursor = errors.New("samples: invalid cursor")

// Sample is the record of one execution of a continuous step, or of one run
// of a service DAG (specs/097-service-run-history.md).
type Sample struct {
	// Time is the start of the execution, in UTC. It has millisecond precision.
	Time time.Time
	// Duration is the length of the execution.
	Duration time.Duration
	// ExitCode is the exit code of the execution; -1 when the step did not start.
	// A run sample has 1 for a run that failed, else 0.
	ExitCode int
	// Message is the last line of the standard error of a failed execution,
	// 256 bytes at most. It is empty when the execution succeeded.
	Message string
	// RunID is the ID of the run of a run sample. Only the events keep it: a
	// raw line does not.
	RunID string
	// GapAfter is the longest time from the end of the previous sample to the
	// start of this one that records no gap event. Zero records no gap event.
	GapAfter time.Duration
	// ActiveGap is the longest time from the end of the previous sample to the
	// start of this one that counts as active time in the uptime. A longer gap
	// is not active. Zero counts no gap. The first interval of a history has no
	// gap before it.
	ActiveGap time.Duration
}

// Failed reports whether the sample is a failed sample (exit code not 0).
func (s Sample) Failed() bool { return s.ExitCode != 0 }

// Tier is one of the four tiers of the spec.
type Tier int

// The tiers, from the finest to the coarsest.
const (
	TierRaw Tier = iota
	TierMinute
	TierHour
	TierDay
)

// Width is the bucket width of the tier.
func (t Tier) Width() time.Duration {
	switch t {
	case TierRaw:
		return time.Second
	case TierMinute:
		return time.Minute
	case TierHour:
		return time.Hour
	case TierDay:
		fallthrough
	default:
		return 24 * time.Hour
	}
}

// Retention is how long the tier keeps its records; 0 means no limit.
func (t Tier) Retention() time.Duration {
	switch t {
	case TierRaw:
		return 24 * time.Hour
	case TierMinute:
		return 7 * 24 * time.Hour
	case TierHour:
		return 30 * 24 * time.Hour
	case TierDay:
		fallthrough
	default:
		return 0
	}
}

// Bucket summarizes the samples of one time bucket.
type Bucket struct {
	// Time is the start of the bucket, in UTC.
	Time     time.Time
	Count    uint64
	Failures uint64
	Min      time.Duration
	Max      time.Duration
	P50      time.Duration
	P95      time.Duration
	// Up is the time of the bucket in which a run was active. A run history
	// gives it, also for a bucket that has no sample. It is zero for a step,
	// for the raw tier and for a bucket with no recorded active time.
	Up time.Duration
}

// Series is the answer to a sample query.
type Series struct {
	Tier    Tier
	From    time.Time
	To      time.Time
	Buckets []Bucket
}

// SampleQuery asks for the samples of a window. The reader picks the finest
// tier whose retention covers From and whose bucket count in the window is not
// more than MaxPoints, and returns at most MaxPoints buckets.
type SampleQuery struct {
	From      time.Time
	To        time.Time
	MaxPoints int
}

// EventKind is the kind of an event.
type EventKind string

// The kinds of events.
const (
	EventStarted   EventKind = "started"
	EventStopped   EventKind = "stopped"
	EventFailed    EventKind = "failed"
	EventRecovered EventKind = "recovered"
	// EventGap is a time with no run, outside an outage, in a run history.
	EventGap EventKind = "gap"
)

// Event is a start, a stop, a failure or a recovery of a continuous step, or
// a failure, a recovery or a gap of a run history.
type Event struct {
	Time     time.Time
	Kind     EventKind
	ExitCode int
	Message  string
	// RunID is the run of a failed, a recovered or a gap event of a run
	// history. It is empty for a step.
	RunID string
	// Gap is the time with no run, for a gap event. The event time is the end
	// of the previous run.
	Gap time.Duration
	// FailedCount is the count of failed samples of the outage, for a
	// recovered event, for a repeated failed event, and for a stopped event
	// during an outage, so that the next start continues the count.
	FailedCount uint64
	// Outage is the length of the outage, for a recovered event.
	Outage time.Duration
}

// EventQuery lists events, the newest first.
type EventQuery struct {
	From   time.Time
	To     time.Time
	Limit  int
	Before string // opaque cursor from EventPage.NextCursor
}

// EventPage is a page of events.
type EventPage struct {
	Events     []Event
	NextCursor string
}

// Window is one availability window.
type Window string

// The availability windows.
const (
	Window24h  Window = "24h"
	Window7d   Window = "7d"
	Window30d  Window = "30d"
	Window365d Window = "365d"
)

// WindowStat is the availability of one window.
type WindowStat struct {
	Window   Window
	Count    uint64
	Failures uint64
	// Uptime is the time of the window in which a run was active. It has no
	// record (its Since is zero) for a step and for an old run history.
	Uptime Uptime
}

// Availability is the availability of each window, the current streak, and the
// total count of executions.
type Availability struct {
	Windows       []WindowStat
	Streak        uint64
	LastFailureAt time.Time // zero when none failed
	TotalCount    uint64
}

// Options configures a Recorder. A zero value uses the defaults of the spec.
type Options struct {
	// Now gives the time; time.Now when nil. Tests set it.
	Now func() time.Time
	// NoLifecycleEvents records no started and no stopped event. A run
	// history sets it.
	NoLifecycleEvents bool
	// WriteEach writes the raw line of each sample when Record returns, so
	// that a reader sees it at once. A run history sets it.
	WriteEach bool
	// Uptime records the time in which a run was active, split over the
	// buckets that it covers. A run history sets it.
	Uptime bool
	// MaxEventBytes bounds the size of the events file. Above it, the recorder
	// drops the oldest events. Zero sets no bound.
	MaxEventBytes int64
}

// NewestEnd returns the end (time plus duration) of the newest sample of the
// store in dir. Once the raw tier no longer holds that sample, it returns the
// end of the newest aggregate bucket. It reports false for a store with no
// sample.
func NewestEnd(dir string) (time.Time, bool, error) {
	ms, ok := newStore(dir).newestEnd()
	if !ok {
		return time.Time{}, false, nil
	}
	return time.UnixMilli(ms).UTC(), true, nil
}

// Recorder is the write side of the store of one continuous step. It is safe
// for use by one goroutine at a time.
type Recorder interface {
	// Record adds a sample. It holds at most one minute of samples in memory
	// and writes the raw tier, the events and the roll-ups at the boundaries
	// of the buckets.
	Record(ctx context.Context, s Sample) error
	// Flush writes what is held in memory.
	Flush(ctx context.Context) error
	// Close flushes and stops the recorder, and records a stopped event.
	Close(ctx context.Context) error
}

// Reader is the read side of the store of one continuous step. It never holds
// more than one response in memory and its cost does not depend on the count
// of samples.
type Reader interface {
	Samples(ctx context.Context, q SampleQuery) (Series, error)
	Events(ctx context.Context, q EventQuery) (EventPage, error)
	Availability(ctx context.Context, now time.Time) (Availability, error)
	// Uptime gives the uptime of the window from `from` to `to`.
	Uptime(ctx context.Context, from, to time.Time) (Uptime, error)
}

// Dir returns the directory of the store of a step, under the directory of
// the attempt of a run. The step name is made safe for a file name.
func Dir(attemptDir, stepName string) string {
	return dirFor(attemptDir, stepName)
}
