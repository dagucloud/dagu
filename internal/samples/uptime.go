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

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
)

// The uptime of a run history is the time in which a run was active. A run
// that lasts hours covers many buckets, so a sample cannot keep it: the
// recorder splits the interval of each run over the buckets that it covers.
// See specs/100-service-run-history.md.
//
// The up time is in its own store, in the directory "up" of the run history:
//
//	up/minute/YYYYMMDD.jsonl   {"t":unixSeconds,"u":upMilliseconds}, one line for each minute
//	up/hour/YYYYMM.jsonl       the same for each hour
//	up/day/YYYY.jsonl          the same for each day
//	up/since                   the start of the first interval, in unix milliseconds
//
// Each tier gets the pieces of an interval at the time of the write, so that a
// run older than the retention of a finer tier still counts in the coarser
// tiers, and the up time of a bucket is the same in every tier that keeps it.
// A bucket holds the whole up time of the bucket. The recorder writes a bucket
// when it closes, and when the recorder flushes or closes; a write replaces
// the line of a bucket that the file holds. The open bucket of the hour and
// the day tier can lack the runs after its last write: a reader takes the
// buckets from the newest line of a tier on from the finer tier too, and keeps
// the larger of a line and the sum of its finer buckets. A recorder that opens
// writes them by the same rule. A crash loses the up time of the open minute.
// A store that has no "up" directory has no known uptime. A window that starts
// before "up/since" counts from it.
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
}

// WithActive adds a run that has not ended: it is active from start to To.
// last is the end of the newest recorded run. When the history has recorded
// uptime, the part of the run before last is already in Up and is not added
// again. Otherwise the run counts from its start.
func (u Uptime) WithActive(start, last time.Time) Uptime {
	if start.IsZero() || !start.Before(u.To) {
		return u
	}
	from := laterOf(start, u.Start)
	if !u.Since.IsZero() {
		from = laterOf(from, last)
	}
	if u.Since.IsZero() || start.Before(u.Since) {
		u.Since = start
	}
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
}

func openUp(ctx context.Context, dir string, lastEnd int64) (*upState, error) {
	u := &upState{st: newStore(filepath.Join(dir, upDirName)), to: lastEnd}
	since, err := readSince(u.st.dir)
	if err != nil {
		return nil, err
	}
	u.since = since
	if err := u.repair(ctx); err != nil {
		return nil, err
	}
	return u, nil
}

// repair writes the buckets of the hour and the day tier that a stop without
// a close left out. A bucket keeps the larger of its line and the sum of its
// finer buckets, so that no part counts twice.
func (u *upState) repair(ctx context.Context) error {
	for _, t := range upTiers[1:] {
		newest, err := u.st.newestUp(t)
		if err != nil {
			return err
		}
		finer, err := u.st.upTier(ctx, t-1, newest, noLimit)
		if err != nil {
			return err
		}
		w := tierSec(t)
		for i := 0; i < len(finer); {
			b, sum := floorTo(finer[i].sec, w), int64(0)
			for ; i < len(finer) && floorTo(finer[i].sec, w) == b; i++ {
				sum += finer[i].ms
			}
			held, err := u.st.readUp(t, b)
			if err != nil {
				return err
			}
			if sum > held {
				if err := u.st.putUp(t, b, sum); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// add records the interval [a, b), in unix milliseconds. The part before the
// end of the newest interval is already counted. A gap before a that is not
// longer than tolerance counts as active time, when an interval is already
// recorded. now bounds the buckets that the tiers keep.
func (u *upState) add(a, b, tolerance, now int64) error {
	run := a // the start of the run, before the gap and the overlap
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
		if lo >= b {
			continue // the whole interval is older than the retention of the tier
		}
		for s := floorTo(lo, w); s < b; s += w {
			if err := u.put(t, s/1000, min(b, s+w)-max(lo, s)); err != nil {
				return err
			}
		}
	}
	// A run that covers more than one minute is the last run for a long time,
	// so its last minute is written at once. The short runs of a minute add to
	// the open minute, which is written when the minute moves, also when the
	// gap before a run joins it to the previous minute.
	if floorTo(run, 60_000) != floorTo(b-1, 60_000) {
		if err := u.flushTier(TierMinute); err != nil {
			return err
		}
	}
	// A finer tier that no longer keeps a part of the interval cannot rebuild
	// the coarser bucket, so that bucket is written at once.
	for _, t := range upTiers[1:] {
		if a < now-retentionSec(t-1)*1000 {
			if err := u.flushTier(t); err != nil {
				return err
			}
		}
	}
	return nil
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
	}
	b.ms += ms
	b.dirty = true
	return nil
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
			if err := fileutil.Remove(f.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
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
	// A replacement within one modification time tick can keep the size.
	upLines.Invalidate(path)
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

// upLineCacheSize bounds the cached up files; the lines of a minute file take
// about 23 KB.
const upLineCacheSize = 128

// upLines keeps the lines of up files across queries, because the availability
// reads a week of minute files that rarely change.
var upLines = fileutil.NewCache[[]upLine]("samples_up_lines", upLineCacheSize, aggCountTTL)

// readUpLines returns the lines of an up file, in time order. A file is read
// again when its size or modification time changes, when the store replaces
// it, and at the latest after aggCountTTL.
func readUpLines(path string) ([]upLine, error) {
	lines, err := upLines.LoadLatest(path, func() ([]upLine, error) {
		var out []upLine
		err := scanUp(path, minInt, noLimit, func(sec, ms int64) { out = append(out, upLine{sec, ms}) })
		return out, err
	})
	if errors.Is(err, fs.ErrNotExist) {
		upLines.Invalidate(path)
		return nil, nil
	}
	return lines, err
}

// eachUp calls fn for the buckets of a tier with lo <= t < hi, in time order.
func (s *store) eachUp(ctx context.Context, t Tier, lo, hi int64, fn func(sec, ms int64)) error {
	for _, f := range s.listRange(t, lo, hi) {
		if err := ctx.Err(); err != nil {
			return err
		}
		lines, err := readUpLines(f.path)
		if err != nil {
			return err
		}
		for _, l := range lines {
			if l.sec >= hi {
				break
			}
			if l.sec >= lo {
				fn(l.sec, l.ms)
			}
		}
	}
	return nil
}

// upTier returns the up time of the buckets of a tier with lo <= t < hi, in
// time order. The hour and the day tier read their buckets from the newest
// line on from the finer tier too, and a bucket keeps the larger of its line
// and the sum of its finer buckets: a close, a flush or an open of a recorder
// writes a bucket that is still open.
func (s *store) upTier(ctx context.Context, t Tier, lo, hi int64) ([]upLine, error) {
	var out []upLine
	if err := s.eachUp(ctx, t, lo, hi, func(sec, ms int64) { out = append(out, upLine{sec, ms}) }); err != nil {
		return nil, err
	}
	if t == TierMinute {
		return out, nil
	}
	newest, err := s.newestUp(t)
	if err != nil {
		return nil, err
	}
	w := tierSec(t)
	from := max(ceilTo(lo, w), newest)
	if from >= hi {
		return out, nil
	}
	end := hi
	if hi < noLimit-w {
		end = ceilTo(hi, w) // a bucket before hi takes all of its finer buckets
	}
	finer, err := s.upTier(ctx, t-1, from, end)
	if err != nil {
		return nil, err
	}
	for i := 0; i < len(finer); {
		b, sum := floorTo(finer[i].sec, w), int64(0)
		for ; i < len(finer) && floorTo(finer[i].sec, w) == b; i++ {
			sum += finer[i].ms
		}
		if n := len(out); n > 0 && out[n-1].sec == b {
			out[n-1].ms = max(out[n-1].ms, sum)
		} else {
			out = append(out, upLine{b, sum})
		}
	}
	return out, nil
}

// newestUp returns the time of the newest bucket that a tier holds, or minInt.
func (s *store) newestUp(t Tier) (int64, error) {
	for _, f := range slices.Backward(s.list(t)) {
		lines, err := readUpLines(f.path)
		if err != nil {
			return 0, err
		}
		if len(lines) > 0 {
			return lines[len(lines)-1].sec, nil
		}
	}
	return minInt, nil
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
// of the series. last is the end of the newest recorded run, or zero for a
// series with no recorded up time. The part of the run before last is already
// in the buckets and is not added again. A bucket that the run covers and that
// has no sample is added with its up time only. A raw series holds no up time
// and is returned as is.
func (s Series) WithActive(start, last time.Time) Series {
	from := laterOf(start, last)
	if start.IsZero() || s.Tier == TierRaw || !from.Before(s.To) {
		return s
	}
	sec := tierSec(s.Tier)
	w := s.Tier.Width()
	first := max(floorTo(from.Unix(), sec), floorTo(s.From.Unix(), sec))
	out := make([]Bucket, 0, len(s.Buckets)+int((s.To.Unix()-first)/sec)+1)
	i := 0
	for t := first; ; t += sec {
		bt := time.Unix(t, 0).UTC()
		a, b := laterOf(from, bt), bt.Add(w)
		if b.After(s.To) {
			b = s.To
		}
		if !b.After(a) {
			break
		}
		for i < len(s.Buckets) && s.Buckets[i].Time.Before(bt) {
			out = append(out, s.Buckets[i])
			i++
		}
		if i < len(s.Buckets) && s.Buckets[i].Time.Equal(bt) {
			bk := s.Buckets[i]
			bk.Up = min(bk.Up+b.Sub(a), w)
			out = append(out, bk)
			i++
			continue
		}
		out = append(out, Bucket{Time: bt, Up: b.Sub(a)})
	}
	s.Buckets = append(out, s.Buckets[i:]...)
	return s
}

// Uptime returns the uptime of the window from `from` to `to`, as uptimes
// gives it at the time of the reader.
func (r *reader) Uptime(ctx context.Context, from, to time.Time) (Uptime, error) {
	ups, err := r.uptimes(ctx, []upWindow{{from, to}}, r.now())
	return ups[0], err
}

// NewestEnd returns the end of the newest sample of the store.
func (r *reader) NewestEnd(_ context.Context) (time.Time, bool, error) {
	return NewestEnd(r.st.dir)
}

// upWindow is the window of an uptime query.
type upWindow struct{ from, to time.Time }

// upPart is the part of a window that one tier gives, in unix seconds.
type upPart struct {
	tier   Tier
	lo, hi int64
}

// upParts splits the window [lo, hi) over the tiers at the time now: each part
// comes from the finest tier that holds it, on the buckets of that tier. The
// window ends at now at the latest.
func upParts(lo, hi, now int64) []upPart {
	const day = int64(24 * 3600)
	hi = min(hi, now)
	h1 := ceilTo(now-7*day, 3600)
	h0 := ceilTo(now-30*day, day)
	parts := []upPart{
		{TierDay, ceilTo(lo, day), min(floorTo(hi, day), h0)},
		{TierHour, ceilTo(max(lo, h0), 3600), min(floorTo(hi, 3600), h1)},
		{TierMinute, ceilTo(max(lo, h1), 60), floorTo(hi, 60)},
	}
	return slices.DeleteFunc(parts, func(p upPart) bool { return p.lo >= p.hi })
}

// uptimes returns the uptime of each window. Each part of a window comes from
// the finest tier that holds it, and the boundaries are on the buckets of that
// tier, so a window starts and ends on a bucket. A window ends at now at the
// latest. The windows share one read of "up/since" and of each tier.
func (r *reader) uptimes(ctx context.Context, wins []upWindow, now time.Time) ([]Uptime, error) {
	out := make([]Uptime, len(wins))
	parts := make([][]upPart, len(wins))
	anyPart := false
	for i, w := range wins {
		out[i] = Uptime{Start: w.from.UTC(), To: w.from.UTC()}
		ps := upParts(w.from.Unix(), w.to.Unix(), now.Unix())
		if len(ps) == 0 {
			continue
		}
		parts[i], anyPart = ps, true
		out[i].Start = time.Unix(ps[0].lo, 0).UTC()
		out[i].To = time.Unix(ps[len(ps)-1].hi, 0).UTC()
	}
	if !anyPart {
		return out, nil
	}
	up := newStore(filepath.Join(r.st.dir, upDirName))
	since, err := readSince(up.dir)
	if err != nil || since == 0 {
		return out, err
	}
	for i := range out {
		if parts[i] != nil {
			out[i].Since = time.UnixMilli(since).UTC()
		}
	}
	for _, t := range upTiers {
		lo, hi := int64(noLimit), int64(minInt)
		for _, ps := range parts {
			for _, p := range ps {
				if p.tier == t {
					lo, hi = min(lo, p.lo), max(hi, p.hi)
				}
			}
		}
		if lo >= hi {
			continue
		}
		lines, err := up.upTier(ctx, t, lo, hi)
		if err != nil {
			return out, err
		}
		for _, l := range lines {
			for i, ps := range parts {
				for _, p := range ps {
					if p.tier == t && l.sec >= p.lo && l.sec < p.hi {
						out[i].Up += time.Duration(l.ms) * time.Millisecond
					}
				}
			}
		}
	}
	return out, nil
}
