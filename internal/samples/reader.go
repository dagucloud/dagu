// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package samples

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"time"
)

const (
	defaultMaxPoints = 600
	defaultEvents    = 100
	maxEvents        = 500
)

type reader struct {
	st  *store
	now func() time.Time
}

// OpenReader opens the store of a step for reading. A step with no store gives
// an empty reader.
func OpenReader(dir string) Reader {
	return OpenReaderWith(dir, Options{})
}

// OpenReaderWith is OpenReader with options. Only Now applies: it gives the
// time that the retention of the tiers counts from.
func OpenReaderWith(dir string, opts Options) Reader {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &reader{st: newStore(dir), now: now}
}

// view is the state of one query.
type view struct {
	r      *reader
	ctx    context.Context
	newest map[Tier]newestBucket
}

type newestBucket struct {
	start int64
	ok    bool
}

func (r *reader) view(ctx context.Context) *view {
	return &view{r: r, ctx: ctx, newest: make(map[Tier]newestBucket)}
}

// newestStart returns the start of the newest bucket of a tier that has
// samples in the finer data.
func (v *view) newestStart(t Tier) (int64, bool) {
	if nb, ok := v.newest[t]; ok {
		return nb.start, nb.ok
	}
	var nb newestBucket
	if t == TierRaw {
		if ms, ok := v.r.st.newestRaw(); ok {
			nb = newestBucket{ms / 1000, true}
		}
	} else if s, ok := v.newestStart(t - 1); ok {
		nb = newestBucket{floorTo(s, tierSec(t)), true}
	}
	v.newest[t] = nb
	return nb.start, nb.ok
}

// rawSamples calls fn for the samples with lo <= second < hi.
func (v *view) rawSamples(lo, hi int64, fn func(ms, us int64, exit int)) error {
	for _, f := range v.r.st.listRange(TierRaw, lo, hi) {
		if err := v.ctx.Err(); err != nil {
			return err
		}
		err := scanRaw(f, func(ms, us int64, exit int) bool {
			sec := ms / 1000
			if sec >= hi {
				return false
			}
			if sec >= lo {
				fn(ms, us, exit)
			}
			return true
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// aggRange returns the aggregates of a tier with lo <= T < hi, in time order.
// The newest bucket of the tier is built from the finer tier, so that it shows
// the samples that are not rolled up yet.
func (v *view) aggRange(t Tier, lo, hi int64) ([]agg, error) {
	var out []agg
	if t == TierRaw {
		var cur agg
		flush := func() {
			if cur.N > 0 {
				out = append(out, cur.clone())
			}
		}
		err := v.rawSamples(lo, hi, func(ms, us int64, exit int) {
			if sec := ms / 1000; cur.N == 0 || cur.T != sec {
				flush()
				cur.reset(sec)
			}
			cur.add(us, exit != 0)
		})
		flush()
		return out, err
	}
	for _, f := range v.r.st.listRange(t, lo, hi) {
		if err := v.ctx.Err(); err != nil {
			return nil, err
		}
		if err := scanAggs(f.path, lo, hi, func(a agg) { out = append(out, a) }); err != nil {
			return nil, err
		}
	}
	s, ok := v.newestStart(t)
	if !ok || s < lo || s >= hi {
		return out, nil
	}
	a, err := v.build(t, s)
	if err != nil {
		return nil, err
	}
	if n := len(out); n > 0 && out[n-1].T == s {
		out = out[:n-1]
	}
	if a.N > 0 {
		out = append(out, a)
	}
	return out, nil
}

// build makes the aggregate of a bucket from the finer tier.
func (v *view) build(t Tier, start int64) (agg, error) {
	a := agg{T: start}
	end := start + tierSec(t)
	parts, err := v.aggRange(t-1, start, end)
	if err != nil {
		return a, err
	}
	for i := range parts {
		a.merge(&parts[i])
	}
	return a, nil
}

func (r *reader) Samples(ctx context.Context, q SampleQuery) (Series, error) {
	now := r.now().Unix()
	to := q.To
	if to.IsZero() {
		to = time.Unix(now, 0)
	}
	from := q.From
	if from.IsZero() {
		from = to.Add(-24 * time.Hour)
	}
	to, from = to.UTC(), from.UTC()
	maxPoints := q.MaxPoints
	if maxPoints <= 0 {
		maxPoints = defaultMaxPoints
	}
	series := Series{Tier: TierDay, From: from, To: to}
	if !to.After(from) {
		return series, nil
	}
	fromS, toS := from.Unix(), to.Unix()
	if to.Nanosecond() > 0 {
		toS++
	}
	tier, first := TierDay, int64(0)
	chosen := false
	for _, t := range allTiers {
		w := tierSec(t)
		covers := t == TierDay || fromS >= now-retentionSec(t)
		f := floorTo(fromS, w)
		count := (floorTo(toS-1, w)-f)/w + 1
		if covers && count <= int64(maxPoints) {
			tier, first, chosen = t, f, true
			break
		}
	}
	w := tierSec(tier)
	if !chosen {
		first = floorTo(toS-1, w) - int64(maxPoints-1)*w
		series.From = time.Unix(max(first, fromS), 0).UTC()
	}
	series.Tier = tier
	aggs, err := r.view(ctx).aggRange(tier, first, toS)
	if err != nil {
		return series, err
	}
	if len(aggs) > maxPoints {
		aggs = aggs[len(aggs)-maxPoints:]
	}
	series.Buckets = make([]Bucket, len(aggs))
	for i := range aggs {
		series.Buckets[i] = aggs[i].bucket()
	}
	return series, nil
}

func (r *reader) Events(ctx context.Context, q EventQuery) (EventPage, error) {
	var page EventPage
	before := int64(-1)
	if q.Before != "" {
		off, err := decodeCursor(q.Before)
		if err != nil {
			return page, err
		}
		before = off
	}
	f, err := os.Open(r.st.eventsPath())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			if before >= 0 {
				return page, fmt.Errorf("%w %q", ErrInvalidCursor, q.Before)
			}
			return page, nil
		}
		return page, err
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return page, err
	}
	end := st.Size()
	if before >= 0 {
		issued, err := isEventStart(f, before, end)
		if err != nil {
			return page, err
		}
		if !issued {
			return page, fmt.Errorf("%w %q", ErrInvalidCursor, q.Before)
		}
		end = before
	}
	limit := q.Limit
	if limit <= 0 {
		limit = defaultEvents
	}
	limit = min(limit, maxEvents)
	var lastOff int64
	err = scanLinesBackward(f, end, func(off int64, line []byte) bool {
		if ctx.Err() != nil {
			return false
		}
		e, ok := parseEvent(line)
		if !ok {
			return true
		}
		if !q.From.IsZero() && e.Time.Before(q.From) {
			return false
		}
		if !q.To.IsZero() && !e.Time.Before(q.To) {
			return true
		}
		if len(page.Events) == limit {
			page.NextCursor = encodeCursor(lastOff)
			return false
		}
		page.Events = append(page.Events, e)
		lastOff = off
		return true
	})
	if err != nil {
		return page, err
	}
	return page, ctx.Err()
}

// isEventStart reports whether off can be a cursor of the events file of the
// given size: the start of a line that follows another line. The file only
// grows, so every cursor that the store gave stays one.
func isEventStart(f *os.File, off, size int64) (bool, error) {
	if off <= 0 || off >= size {
		return false, nil
	}
	b := make([]byte, 1)
	if _, err := f.ReadAt(b, off-1); err != nil {
		return false, err
	}
	return b[0] == '\n', nil
}

type count struct{ n, f uint64 }

func (c *count) add(o count) { c.n += o.n; c.f += o.f }

func (r *reader) Availability(ctx context.Context, nowT time.Time) (Availability, error) {
	v := r.view(ctx)
	now := nowT.Unix()
	const day = int64(24 * 3600)
	// Each part of the longest window comes from the finest tier that holds it.
	// The parts do not overlap: the boundaries are on the buckets of the finer
	// tier. The raw tier holds the whole 24 hours window, to the second.
	m1 := ceilTo(now-day, 60)
	h1 := ceilTo(now-7*day, 3600)
	h0 := ceilTo(now-30*day, day)

	var raw24, rawC, m7, m, h30, h, d count
	err := v.rawSamples(now-day, noLimit, func(ms, _ int64, exit int) {
		c := count{n: 1}
		if exit != 0 {
			c.f = 1
		}
		raw24.add(c)
		if ms/1000 >= m1 {
			rawC.add(c)
		}
	})
	if err != nil {
		return Availability{}, err
	}
	sum := func(t Tier, lo, hi int64, sinks ...func(a aggCount)) error {
		for _, f := range r.st.listRange(t, lo, hi) {
			if err := ctx.Err(); err != nil {
				return err
			}
			counts, err := readAggCounts(f.path)
			if err != nil {
				return err
			}
			for _, a := range counts {
				if a.t < lo || a.t >= hi {
					continue
				}
				for _, s := range sinks {
					s(a)
				}
			}
		}
		return nil
	}
	into := func(c *count, from int64) func(a aggCount) {
		return func(a aggCount) {
			if a.t >= from {
				c.add(count{a.n, a.f})
			}
		}
	}
	if err := sum(TierMinute, ceilTo(now-7*day, 60), m1, into(&m7, minInt), into(&m, h1)); err != nil {
		return Availability{}, err
	}
	if err := sum(TierHour, ceilTo(now-30*day, 3600), h1, into(&h30, minInt), into(&h, h0)); err != nil {
		return Availability{}, err
	}
	if err := sum(TierDay, ceilTo(now-365*day, day), h0, into(&d, minInt)); err != nil {
		return Availability{}, err
	}
	stat := func(w Window, parts ...count) WindowStat {
		s := WindowStat{Window: w}
		for _, p := range parts {
			s.Count += p.n
			s.Failures += p.f
		}
		return s
	}
	av := Availability{Windows: []WindowStat{
		stat(Window24h, raw24),
		stat(Window7d, rawC, m7),
		stat(Window30d, rawC, m, h30),
		stat(Window365d, rawC, m, h, d),
	}}
	streak, last, err := v.streak()
	if err != nil {
		return av, err
	}
	av.Streak, av.LastFailureAt = streak, last
	days, err := v.aggRange(TierDay, minInt, noLimit)
	if err != nil {
		return av, err
	}
	for i := range days {
		av.TotalCount += days[i].N
	}
	return av, nil
}

// streak returns the count of samples after the last failed sample, and the
// time of that sample. When the finer tiers no longer hold the failed sample,
// the time is the start of its bucket, and the count leaves out the samples
// after the failure in that bucket: it is a lower bound.
func (v *view) streak() (uint64, time.Time, error) {
	var streak uint64
	oldest := int64(noLimit)
	rawFiles := v.r.st.list(TierRaw)
	for _, rawFile := range slices.Backward(rawFiles) {
		if err := v.ctx.Err(); err != nil {
			return 0, time.Time{}, err
		}
		// after counts the samples after the last failed sample of the file.
		var after uint64
		var lastFail int64
		failed := false
		err := scanRaw(rawFile, func(ms, _ int64, exit int) bool {
			if exit != 0 {
				lastFail, failed, after = ms, true, 0
			} else {
				after++
			}
			return true
		})
		if err != nil {
			return 0, time.Time{}, err
		}
		if failed {
			return streak + after, time.UnixMilli(lastFail).UTC(), nil
		}
		streak += after
	}
	if len(rawFiles) > 0 {
		oldest = rawFiles[0].start
	}
	for _, t := range allTiers[1:] {
		files := v.r.st.list(t)
		for _, file := range slices.Backward(files) {
			if err := v.ctx.Err(); err != nil {
				return 0, time.Time{}, err
			}
			counts, err := readAggCounts(file.path)
			if err != nil {
				return 0, time.Time{}, err
			}
			for _, a := range slices.Backward(counts) {
				if a.t >= oldest {
					continue
				}
				if a.f > 0 {
					return streak, time.Unix(a.t, 0).UTC(), nil
				}
				streak += a.n
			}
		}
		if len(files) > 0 {
			oldest = min(oldest, files[0].start)
		}
	}
	return streak, time.Time{}, nil
}
