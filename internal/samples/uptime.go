// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package samples

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"
)

// The uptime of a run history is the time in which a run was active. A run
// that lasts hours covers many buckets, so a sample cannot keep it: the
// recorder splits the interval of each run over the buckets that it covers.
// See specs/097-service-run-history.md.
//
// The up time is in its own store, in the directory "up" of the run history:
//
//	up/minute/YYYYMMDD.jsonl   {"t":unixSeconds,"u":upMilliseconds}, one line for each minute
//	up/hour/YYYYMM.jsonl       the same for each hour
//	up/day/YYYY.jsonl          the same for each day
//	up/since                   the start of the first interval, in unix milliseconds
//
// Each tier gets the pieces of an interval at the time of the write, so a
// bucket is never a roll-up of finer buckets, and the up time of a bucket is
// the same in every tier that keeps it. A bucket holds the whole up time of the
// bucket: a write replaces its line. A store that has no "up" directory has
// no known uptime. A window that starts before "up/since" counts from it.
//
// The gap between two runs counts as active time when the sample gives a
// tolerance for it (Sample.ActiveGap). The buckets then hold that time, so the
// stored data stays one fact for every reader.

const (
	upDirName   = "up"
	upSinceName = "since"
)

// upTiers are the tiers of the up store.
var upTiers = [...]Tier{TierMinute, TierHour, TierDay}

// Uptime is the time of a window in which a run was active.
type Uptime struct {
	// Start is the start of the window, on the bucket of the tier that holds it.
	Start time.Time
	// To is the end of the window.
	To time.Time
	// Up is the active time that the history recorded in the window.
	Up time.Duration
	// Since is the start of the first recorded interval. It is zero for a
	// history that recorded none: its uptime is unknown.
	Since time.Time
	// Last is the end of the newest recorded run, also when the history has no
	// uptime. A run that started before it is already in Up.
	Last time.Time
}

// WithActive adds a run that has not ended: it is active from start to To. The
// part that the history already recorded is not added again.
func (u Uptime) WithActive(start time.Time) Uptime {
	if start.IsZero() || !start.Before(u.To) {
		return u
	}
	if u.Since.IsZero() || start.Before(u.Since) {
		u.Since = start
	}
	from := laterOf(laterOf(start, u.Last), u.Start)
	if u.To.After(from) {
		u.Up += u.To.Sub(from)
	}
	return u
}

// Ratio returns the share of the window in which a run was active, from 0 to
// 1. It reports false when the history has no record of the window.
func (u Uptime) Ratio() (float64, bool) {
	if u.Since.IsZero() {
		return 0, false
	}
	span := u.To.Sub(laterOf(u.Start, u.Since))
	if span <= 0 {
		return 0, false
	}
	return float64(min(u.Up, span)) / float64(span), true
}

func laterOf(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// upBucket is the open bucket of a tier.
type upBucket struct {
	t     int64 // start of the bucket, unix seconds
	ms    int64 // up time of the bucket, with the part that the file holds
	live  bool
	dirty bool
}

// upState is the uptime part of a recorder.
type upState struct {
	st    *store
	since int64 // unix milliseconds; 0 before the first interval
	to    int64 // end of the newest interval, unix milliseconds
	open  [len(allTiers)]upBucket
	moved bool // the open minute changed since the last settle
}

func openUp(dir string, lastEnd int64) (*upState, error) {
	u := &upState{st: newStore(filepath.Join(dir, upDirName)), to: lastEnd}
	since, err := readSince(u.st.dir)
	if err != nil {
		return nil, err
	}
	u.since = since
	return u, nil
}

// add records the interval [a, b), in unix milliseconds. The part before the
// end of the newest interval is already counted. A gap before a that is not
// longer than tolerance counts as active time, when an interval is already
// recorded. now bounds the buckets that the tiers keep.
func (u *upState) add(a, b, tolerance, now int64) error {
	if u.since != 0 && a > u.to && a-u.to <= tolerance {
		a = u.to
	}
	a = max(a, u.to)
	if b <= a {
		return nil
	}
	u.to = b
	if u.since == 0 {
		if err := writeFile(filepath.Join(u.st.dir, upSinceName), strconv.AppendInt(nil, a, 10)); err != nil {
			return err
		}
		u.since = a
	}
	for _, t := range upTiers {
		w := tierSec(t) * 1000
		lo := a
		if ret := retentionSec(t) * 1000; ret > 0 {
			lo = max(lo, now-ret)
		}
		for s := floorTo(lo, w); s < b; s += w {
			if err := u.put(t, s/1000, min(b, s+w)-max(lo, s)); err != nil {
				return err
			}
		}
	}
	// A run that covers more than one minute is the last run for a long time,
	// so its last minute is written at once. The short runs of a minute add to
	// the open minute, which is written when the minute moves.
	if floorTo(a, 60_000) != floorTo(b-1, 60_000) {
		if err := u.flushTier(TierMinute); err != nil {
			return err
		}
	}
	return u.settle()
}

func (u *upState) put(t Tier, sec, ms int64) error {
	b := &u.open[t]
	if !b.live || b.t != sec {
		if err := u.flushTier(t); err != nil {
			return err
		}
		held, err := u.st.readUp(t, sec)
		if err != nil {
			return err
		}
		*b = upBucket{t: sec, ms: held, live: true}
		u.moved = u.moved || t == TierMinute
	}
	b.ms += ms
	b.dirty = true
	return nil
}

// settle writes the open hour and the open day once the open minute moved, so
// that a crash loses the up time of one minute at most.
func (u *upState) settle() error {
	if !u.moved {
		return nil
	}
	u.moved = false
	return errors.Join(u.flushTier(TierHour), u.flushTier(TierDay))
}

func (u *upState) flushTier(t Tier) error {
	b := &u.open[t]
	if !b.live || !b.dirty {
		return nil
	}
	if err := u.st.putUp(t, b.t, b.ms); err != nil {
		return err
	}
	b.dirty = false
	return nil
}

// flush writes the open buckets and syncs the files.
func (u *upState) flush() error {
	var errs []error
	for _, t := range upTiers {
		errs = append(errs, u.flushTier(t))
	}
	return errors.Join(append(errs, u.st.syncAll())...)
}

func (u *upState) close() error {
	return errors.Join(u.flush(), u.st.closeAll())
}

// prune removes the files whose whole span is older than the retention of
// their tier. The day tier has no retention.
func (u *upState) prune(now int64) error {
	for _, t := range upTiers[:2] {
		for _, f := range u.st.list(t) {
			if f.end+retentionSec(t) > now {
				continue
			}
			u.st.forget(f.path)
			if err := os.Remove(f.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}
	}
	return nil
}

func readSince(dir string) (int64, error) {
	b, err := os.ReadFile(filepath.Join(dir, upSinceName)) //nolint:gosec
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	since, err := strconv.ParseInt(string(bytes.TrimSpace(b)), 10, 64)
	if err != nil || since <= 0 {
		// A file that a crash cut: the history counts from its first interval again.
		return 0, nil
	}
	return since, nil
}

// putUp stores the up time of a bucket: it appends the line of a new bucket and
// replaces the line of a bucket that the file holds.
func (s *store) putUp(t Tier, sec, ms int64) error {
	start, _ := fileSpan(t, sec)
	path := s.path(t, start)
	last, err := s.lastOf(path)
	if err != nil {
		return err
	}
	line := appendUpLine(nil, sec, ms)
	if sec > last {
		if err := s.appendTo(t, path, line); err != nil {
			return err
		}
		s.lastT[path] = sec
		return nil
	}
	lines := make(map[int64][]byte)
	err = eachLine(path, func(l []byte) bool {
		if tm, _, ok := parseUp(l); ok {
			lines[tm] = append(bytes.Clone(l), '\n')
		}
		return true
	})
	if err != nil {
		return err
	}
	lines[sec] = line
	times := make([]int64, 0, len(lines))
	for tm := range lines {
		times = append(times, tm)
	}
	slices.Sort(times)
	var buf []byte
	for _, tm := range times {
		buf = append(buf, lines[tm]...)
	}
	s.forget(path)
	if err := writeFile(path, buf); err != nil {
		return err
	}
	s.lastT[path] = times[len(times)-1]
	return nil
}

// readUp returns the up time that the file holds for a bucket.
func (s *store) readUp(t Tier, sec int64) (int64, error) {
	start, _ := fileSpan(t, sec)
	path := s.path(t, start)
	last, err := s.lastOf(path)
	if err != nil || last < sec {
		return 0, err
	}
	var held int64
	err = scanUp(path, sec, sec+1, func(_, ms int64) { held += ms })
	return held, err
}

func appendUpLine(b []byte, sec, ms int64) []byte {
	b = append(b, `{"t":`...)
	b = strconv.AppendInt(b, sec, 10)
	b = append(b, `,"u":`...)
	b = strconv.AppendInt(b, ms, 10)
	return append(b, "}\n"...)
}

// parseUp decodes a line. It reports false for a line that a crash cut.
func parseUp(line []byte) (sec, ms int64, ok bool) {
	sec, ok = lineTime(line)
	if !ok || !bytes.HasSuffix(line, []byte("}")) {
		return 0, 0, false
	}
	_, rest, found := bytes.Cut(line, []byte(`,"u":`))
	if !found {
		return 0, 0, false
	}
	ms, err := strconv.ParseInt(string(rest[:len(rest)-1]), 10, 64)
	return sec, ms, err == nil && ms >= 0
}

// scanUp calls fn for the buckets of a file with lo <= t < hi, in order.
func scanUp(path string, lo, hi int64, fn func(sec, ms int64)) error {
	return eachLine(path, func(line []byte) bool {
		sec, ms, ok := parseUp(line)
		if !ok || sec < lo {
			return true
		}
		if sec >= hi {
			return false
		}
		fn(sec, ms)
		return true
	})
}

type upLine struct{ sec, ms int64 }

// upBuckets returns the up time of the buckets of a tier with lo <= t < hi, in
// time order.
func (s *store) upBuckets(ctx context.Context, t Tier, lo, hi int64) ([]upLine, error) {
	var out []upLine
	for _, f := range s.listRange(t, lo, hi) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := scanUp(f.path, lo, hi, func(sec, ms int64) { out = append(out, upLine{sec, ms}) }); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// addUp adds up time to the bucket that starts at t. A bucket that has no
// sample is inserted, so that the buckets stay in time order.
func addUp(buckets []Bucket, t time.Time, up time.Duration) []Bucket {
	if up <= 0 {
		return buckets
	}
	i, found := slices.BinarySearchFunc(buckets, t, func(b Bucket, t time.Time) int { return b.Time.Compare(t) })
	if found {
		buckets[i].Up += up
		return buckets
	}
	return slices.Insert(buckets, i, Bucket{Time: t, Up: up})
}

// WithActive adds a run that has not ended: it is active from start to the end
// of the series. The part before `last`, the end of the newest recorded run, is
// already in the buckets and is not added again. A bucket that the run covers
// and that has no sample is added with its up time only.
func (s Series) WithActive(start, last time.Time) Series {
	from := laterOf(start, last)
	if start.IsZero() || !from.Before(s.To) {
		return s
	}
	w := s.Tier.Width()
	buckets := slices.Clone(s.Buckets)
	first := floorTo(s.From.Unix(), tierSec(s.Tier))
	for t := max(floorTo(from.Unix(), tierSec(s.Tier)), first); ; t += tierSec(s.Tier) {
		a, b := laterOf(from, time.Unix(t, 0)), time.Unix(t, 0).Add(w)
		if b.After(s.To) {
			b = s.To
		}
		if !b.After(a) {
			break
		}
		buckets = addUp(buckets, time.Unix(t, 0).UTC(), b.Sub(a))
	}
	for i := range buckets {
		buckets[i].Up = min(buckets[i].Up, w)
	}
	s.Buckets = buckets
	return s
}

// upRange returns the up time of the buckets of a tier with lo <= t < hi, in
// milliseconds.
func (s *store) upRange(ctx context.Context, t Tier, lo, hi int64) (int64, error) {
	var sum int64
	for _, f := range s.listRange(t, lo, hi) {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if err := scanUp(f.path, lo, hi, func(_, ms int64) { sum += ms }); err != nil {
			return 0, err
		}
	}
	return sum, nil
}

// Uptime returns the uptime of the window from `from` to `to`. Each part of the
// window comes from the finest tier that holds it, and the boundaries are on
// the buckets of that tier, so the window starts and ends on a bucket. The end
// is not after the time of the reader.
func (r *reader) Uptime(ctx context.Context, from, to time.Time) (Uptime, error) {
	return r.uptime(ctx, from, to, r.now())
}

func (r *reader) uptime(ctx context.Context, from, to, now time.Time) (Uptime, error) {
	up := newStore(filepath.Join(r.st.dir, upDirName))
	const day = int64(24 * 3600)
	n, lo := now.Unix(), from.Unix()
	hi := min(to.Unix(), n)
	h1 := ceilTo(n-7*day, 3600)
	h0 := ceilTo(n-30*day, day)
	parts := []struct {
		tier   Tier
		lo, hi int64
	}{
		{TierDay, ceilTo(lo, day), min(floorTo(hi, day), h0)},
		{TierHour, ceilTo(max(lo, h0), 3600), min(floorTo(hi, 3600), h1)},
		{TierMinute, ceilTo(max(lo, h1), 60), floorTo(hi, 60)},
	}
	parts = slices.DeleteFunc(parts, func(p struct {
		tier   Tier
		lo, hi int64
	}) bool {
		return p.lo >= p.hi
	})
	u := Uptime{Start: from.UTC(), To: from.UTC()}
	if end, ok := r.st.newestEnd(); ok {
		u.Last = time.UnixMilli(end).UTC()
	}
	if len(parts) == 0 {
		return u, nil
	}
	u.Start = time.Unix(parts[0].lo, 0).UTC()
	u.To = time.Unix(parts[len(parts)-1].hi, 0).UTC()
	if since, err := readSince(up.dir); err != nil {
		return u, err
	} else if since > 0 {
		u.Since = time.UnixMilli(since).UTC()
	}
	if u.Since.IsZero() {
		return u, nil
	}
	for _, p := range parts {
		ms, err := up.upRange(ctx, p.tier, p.lo, p.hi)
		if err != nil {
			return u, err
		}
		u.Up += time.Duration(ms) * time.Millisecond
	}
	return u, nil
}
