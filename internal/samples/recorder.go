// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package samples

import (
	"context"
	"errors"
	"sync"
	"time"
)

// rawFlushBytes is the size of the raw lines that the recorder holds before it
// writes them, when more than one minute of samples is not the limit.
const rawFlushBytes = 64 << 10

var errClosed = errors.New("samples: recorder is closed")

type recorder struct {
	mu     sync.Mutex
	st     *store
	now    func() time.Time
	closed bool

	trk        tracker
	lastEvent  int64 // ms of the last event written
	lastSample int64 // ms of the newest sample

	// The minute that is open.
	hasMinute bool
	minute    int64 // unix seconds
	acc       agg
	buf       []byte
	bufMsg    string // last failed message that the buffer holds or the file ends with
	rawPath   string
}

func openRecorder(ctx context.Context, dir string, opts Options) (*recorder, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	r := &recorder{st: newStore(dir), now: now}
	if err := r.st.repair(now().Unix()); err != nil {
		return nil, err
	}
	// The newest stored sample bounds the time of the next one, and its minute
	// opens again: a crash wrote no aggregate of it.
	if ms, ok := r.st.newestRaw(); ok {
		r.lastSample = ms
		if err := r.beginMinute(floorTo(ms/1000, 60)); err != nil {
			return nil, err
		}
	}
	trk, last, err := r.st.loadTracker()
	if err != nil {
		return nil, err
	}
	r.trk, r.lastEvent = trk, last
	if err := r.writeEvent(Event{Time: now().UTC(), Kind: EventStarted}); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *recorder) writeEvent(e Event) error {
	// The file stays in time order, for the cursor and for the scan of a window.
	e.Time = time.UnixMilli(max(e.Time.UnixMilli(), r.lastEvent)).UTC()
	r.lastEvent = e.Time.UnixMilli()
	return r.st.appendEvent(e)
}

func (s *store) appendEvent(e Event) error {
	a := &appender{path: s.eventsPath()}
	// The events file is rare to write, so each write opens and closes it.
	err := a.write(e.encode())
	return errors.Join(err, a.close())
}

func (r *recorder) Record(ctx context.Context, s Sample) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return errClosed
	}
	// A sample before the newest one, such as after the clock steps back, takes
	// its time, so that the samples stay in time order.
	ms := max(s.Time.UnixMilli(), r.lastSample)
	minute := floorTo(ms/1000, 60)
	switch {
	case !r.hasMinute:
		r.beginEmptyMinute(minute)
	case minute > r.minute:
		if err := r.closeMinute(minute); err != nil {
			return err
		}
		r.beginEmptyMinute(minute)
	}
	r.lastSample = ms
	us := max(s.Duration.Microseconds(), 0)
	msg := cutMessage(s.Message)
	if s.ExitCode == 0 {
		msg = ""
	}
	if e, ok := r.trk.observe(ms, s.ExitCode, msg); ok {
		if err := r.writeEvent(e); err != nil {
			return err
		}
	}
	hourStart, _ := fileSpan(TierRaw, r.minute)
	r.buf = appendRaw(r.buf, ms-hourStart*1000, us, s.ExitCode, msg, &r.bufMsg)
	r.acc.add(us, s.ExitCode != 0)
	if len(r.buf) >= rawFlushBytes {
		return r.writeRaw()
	}
	return nil
}

// beginMinute opens the minute of the newest stored sample. The samples that
// an earlier recorder wrote in it join the aggregate.
func (r *recorder) beginMinute(minute int64) error {
	r.beginEmptyMinute(minute)
	start, end := fileSpan(TierRaw, minute)
	f := fileInfo{start: start, end: end, path: r.st.path(TierRaw, start)}
	return scanRaw(f, func(ms, us int64, exit int) bool {
		if floorTo(ms/1000, 60) == minute {
			r.acc.add(us, exit != 0)
		}
		return true
	})
}

func (r *recorder) beginEmptyMinute(minute int64) {
	r.hasMinute, r.minute = true, minute
	r.acc.reset(minute)
	hour, _ := fileSpan(TierRaw, minute)
	r.rawPath = r.st.path(TierRaw, hour)
	r.bufMsg = ""
}

// writeRaw writes the raw lines that the recorder holds.
func (r *recorder) writeRaw() error {
	if len(r.buf) == 0 {
		return nil
	}
	if err := r.st.appendTo(TierRaw, r.rawPath, r.buf); err != nil {
		return err
	}
	r.buf = r.buf[:0]
	return nil
}

// closeMinute writes the open minute and rolls up the buckets that ended before
// the minute next.
func (r *recorder) closeMinute(next int64) error {
	if err := r.writeRaw(); err != nil {
		return err
	}
	if r.acc.N > 0 {
		if err := r.st.putAgg(TierMinute, &r.acc); err != nil {
			return err
		}
	}
	for _, t := range allTiers[1:TierDay] {
		w := tierSec(t + 1)
		if floorTo(next, w) == floorTo(r.minute, w) {
			break
		}
		start, end := fileSpan(t, r.minute)
		f := fileInfo{start: start, end: end, path: r.st.path(t, start)}
		lo := floorTo(r.minute, w)
		if err := r.st.rollRange(t, f, lo, lo+w, next); err != nil {
			return err
		}
	}
	if floorTo(next, 3600) != floorTo(r.minute, 3600) {
		return r.st.prune(r.now().Unix())
	}
	return nil
}

func (r *recorder) Flush(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return errClosed
	}
	return r.flush()
}

func (r *recorder) flush() error {
	if err := r.writeRaw(); err != nil {
		return err
	}
	return r.st.syncAll()
}

func (r *recorder) Close(_ context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	err := r.finish()
	return errors.Join(err, r.st.closeAll())
}

// finish writes what the recorder holds, with the aggregates of the open
// buckets, and the stopped event.
func (r *recorder) finish() error {
	if r.hasMinute {
		if err := r.writeRaw(); err != nil {
			return err
		}
		if r.acc.N > 0 {
			if err := r.st.putAgg(TierMinute, &r.acc); err != nil {
				return err
			}
		}
		for _, t := range allTiers[1:TierDay] {
			start, end := fileSpan(t, r.minute)
			f := fileInfo{start: start, end: end, path: r.st.path(t, start)}
			if err := r.st.roll(t, f, noLimit); err != nil {
				return err
			}
		}
	}
	// The stop is not before the samples it ends, so that the next start counts
	// no failed sample of them twice.
	stopped := Event{Time: time.UnixMilli(max(r.now().UnixMilli(), r.lastSample)).UTC(), Kind: EventStopped}
	if r.trk.inOutage {
		stopped.FailedCount = r.trk.failed
	}
	if err := r.writeEvent(stopped); err != nil {
		return err
	}
	return r.st.syncAll()
}

// OpenRecorder opens the store of a step for writing, and builds the roll-ups
// that a stop or a crash left out.
func OpenRecorder(ctx context.Context, dir string, opts Options) (Recorder, error) {
	r, err := openRecorder(ctx, dir, opts)
	if err != nil {
		return nil, err
	}
	return r, nil
}
