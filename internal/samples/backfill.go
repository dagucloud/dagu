// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package samples

import (
	"bufio"
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// BackfillOptions describes a stretch of history for Backfill.
type BackfillOptions struct {
	// Start and End bound the stretch, [Start, End). Both are multiples of one
	// minute.
	Start, End time.Time
	// Interval is the time between two samples, in whole milliseconds: a
	// divisor of one minute, or a whole count of minutes.
	Interval time.Duration
	// Now is the time that the retention of the tiers counts from. A file that
	// the retention of its tier would remove at Now is not written.
	Now time.Time
	// Next gives the sample at a time, in unix milliseconds: the duration,
	// the exit code and the message.
	Next func(ms int64) (time.Duration, int, string)
}

// Backfill writes the minute, hour and day tiers and the events of a long
// stretch of history, without a raw tier and without a Recorder. It is for
// tools and tests that need years of samples at once: the cost is the cost of
// the Next function and of one histogram update for each sample. The directory
// must hold no store yet. A Recorder opened after it continues the store from
// End.
func Backfill(ctx context.Context, dir string, o BackfillOptions) (err error) {
	if o.Interval <= 0 || o.Next == nil {
		return errf("backfill needs an interval and a Next function")
	}
	if o.Interval%time.Millisecond != 0 {
		return errf("backfill needs an interval of whole milliseconds")
	}
	startS, endS := o.Start.Unix(), o.End.Unix()
	if startS%60 != 0 || endS%60 != 0 || endS <= startS {
		return errf("backfill needs a start and an end on a minute, with the end after the start")
	}
	if o.Interval < time.Minute && time.Minute%o.Interval != 0 || o.Interval >= time.Minute && o.Interval%time.Minute != 0 {
		return errf("backfill needs an interval that divides a minute or is a whole count of minutes")
	}
	stride := max(int64(o.Interval/time.Minute), 1)
	if _, err := os.Stat(filepath.Join(dir, eventsFileName)); err == nil {
		return errf("backfill needs an empty store: %s has events", dir)
	}
	b := &backfiller{st: newStore(dir), now: o.Now.Unix()}
	b.writers[TierMinute] = newBulkWriter(b.st, TierMinute)
	b.writers[TierHour] = newBulkWriter(b.st, TierHour)
	b.writers[TierDay] = newBulkWriter(b.st, TierDay)
	b.ev = newBulkEvents(b.st.eventsPath())
	defer func() {
		err = errors.Join(err, b.ev.close(), b.writers[TierMinute].close(), b.writers[TierHour].close(), b.writers[TierDay].close())
	}()
	if err := b.ev.write(Event{Time: o.Start.UTC(), Kind: EventStarted}); err != nil {
		return err
	}
	step := o.Interval.Milliseconds()
	var trk tracker
	for minute := startS; minute < endS; minute += 60 {
		if err := ctx.Err(); err != nil {
			return err
		}
		b.minute.reset(minute)
		if (minute-startS)/60%stride != 0 {
			if err := b.endMinute(minute); err != nil {
				return err
			}
			continue
		}
		for ms := minute * 1000; ms < (minute+60)*1000; ms += step {
			d, exit, msg := o.Next(ms)
			us := max(d.Microseconds(), 0)
			if exit == 0 {
				msg = ""
			}
			if e, ok := trk.observe(ms, exit, cutMessage(msg)); ok {
				if err := b.ev.write(e); err != nil {
					return err
				}
			}
			b.minute.add(us, exit != 0)
		}
		if err := b.endMinute(minute); err != nil {
			return err
		}
	}
	if err := b.finish(); err != nil {
		return err
	}
	stopped := Event{Time: o.End.UTC(), Kind: EventStopped}
	if trk.inOutage {
		stopped.FailedCount = trk.failed
	}
	return b.ev.write(stopped)
}

type backfiller struct {
	st      *store
	now     int64
	writers [len(tierDirs)]*bulkWriter
	ev      *bulkEvents
	minute  agg
	hour    agg
	day     agg
}

// endMinute adds the minute to the hour and the day, and writes the buckets
// that end with it.
func (b *backfiller) endMinute(minute int64) error {
	if b.minute.N > 0 {
		if err := b.put(TierMinute, &b.minute); err != nil {
			return err
		}
		if b.hour.N == 0 {
			b.hour.reset(floorTo(minute, 3600))
		}
		b.hour.merge(&b.minute)
	}
	if (minute+60)%3600 != 0 {
		return nil
	}
	if b.hour.N > 0 {
		if err := b.put(TierHour, &b.hour); err != nil {
			return err
		}
		if b.day.N == 0 {
			b.day.reset(floorTo(minute, 86400))
		}
		b.day.merge(&b.hour)
		b.hour.reset(0)
	}
	if (minute+60)%86400 != 0 || b.day.N == 0 {
		return nil
	}
	if err := b.put(TierDay, &b.day); err != nil {
		return err
	}
	b.day.reset(0)
	return nil
}

// finish writes the hour and the day that are open at the end, as a Recorder
// does when it closes.
func (b *backfiller) finish() error {
	if b.hour.N > 0 {
		if err := b.put(TierHour, &b.hour); err != nil {
			return err
		}
		if b.day.N == 0 {
			b.day.reset(floorTo(b.hour.T, 86400))
		}
		b.day.merge(&b.hour)
	}
	if b.day.N == 0 {
		return nil
	}
	return b.put(TierDay, &b.day)
}

// put writes an aggregate, unless the retention removes its file.
func (b *backfiller) put(t Tier, a *agg) error {
	start, end := fileSpan(t, a.T)
	if r := retentionSec(t); r > 0 && end+r <= b.now {
		return nil
	}
	return b.writers[t].write(start, a)
}

// bulkWriter writes the aggregates of one tier, in time order, with a buffer.
type bulkWriter struct {
	st   *store
	tier Tier
	path string
	f    *os.File
	w    *bufio.Writer
	buf  []byte
}

func newBulkWriter(st *store, t Tier) *bulkWriter { return &bulkWriter{st: st, tier: t} }

func (w *bulkWriter) write(fileStart int64, a *agg) error {
	path := w.st.path(w.tier, fileStart)
	if path != w.path {
		if err := w.close(); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			return err
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600) //nolint:gosec
		if err != nil {
			return err
		}
		w.path, w.f, w.w = path, f, bufio.NewWriterSize(f, 256<<10)
	}
	w.buf = a.appendJSON(w.buf[:0])
	_, err := w.w.Write(w.buf)
	return err
}

func (w *bulkWriter) close() error {
	if w.f == nil {
		return nil
	}
	err := errors.Join(w.w.Flush(), w.f.Sync(), w.f.Close())
	w.f, w.w, w.path = nil, nil, ""
	return err
}

// bulkEvents writes events with a buffer.
type bulkEvents struct {
	path string
	f    *os.File
	w    *bufio.Writer
}

func newBulkEvents(path string) *bulkEvents { return &bulkEvents{path: path} }

func (e *bulkEvents) write(ev Event) error {
	if e.f == nil {
		if err := os.MkdirAll(filepath.Dir(e.path), 0o750); err != nil {
			return err
		}
		f, err := os.OpenFile(e.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600) //nolint:gosec
		if err != nil {
			return err
		}
		e.f, e.w = f, bufio.NewWriter(f)
	}
	_, err := e.w.Write(ev.encode())
	return err
}

func (e *bulkEvents) close() error {
	if e.f == nil {
		return nil
	}
	err := errors.Join(e.w.Flush(), e.f.Sync(), e.f.Close())
	e.f, e.w = nil, nil
	return err
}
