// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package samples

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The fixture is three years of history with a sample each 15 minutes,
// and 26 hours with a sample each second, written by a recorder.
type fixture struct {
	dir  string
	now  time.Time // one second after the last sample
	last int64     // ms of the last sample
	all  []int64   // ms of all samples
}

func failAt(ms int64) bool {
	sec := ms / 1000
	return sec%1800 == 0 && (sec/1800)%7 == 0
}

func durAt(ms int64) time.Duration { return time.Duration(40+(ms/1000)%50) * time.Millisecond }

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	now := time.Date(2029, 3, 10, 12, 30, 0, 0, time.UTC)
	end := now.Add(-26 * time.Hour).Truncate(time.Hour)
	start := now.Add(-3 * 365 * 24 * time.Hour).Truncate(time.Hour)
	fx := &fixture{dir: dir}
	err := Backfill(ctx, dir, BackfillOptions{
		Start: start, End: end, Interval: 15 * time.Minute, Now: now,
		Next: func(ms int64) (time.Duration, int, string) {
			fx.all = append(fx.all, ms)
			if failAt(ms) {
				return time.Second, 1, "refused"
			}
			return durAt(ms), 0, ""
		},
	})
	require.NoError(t, err)

	clk := &clock{t: end}
	r := newRecorder(t, dir, clk)
	n := int(now.Sub(end) / time.Second)
	feed(t, r, clk, end, n, time.Second, func(i int) (time.Duration, int, string) {
		ms := end.Add(time.Duration(i) * time.Second).UnixMilli()
		fx.all = append(fx.all, ms)
		if failAt(ms) {
			return time.Second, 1, "refused"
		}
		return durAt(ms), 0, ""
	})
	require.NoError(t, r.Flush(ctx))
	fx.last = fx.all[len(fx.all)-1]
	fx.now = time.UnixMilli(fx.last).UTC().Add(time.Second)
	return fx
}

func (fx *fixture) reader() Reader {
	return OpenReaderWith(fx.dir, Options{Now: func() time.Time { return fx.now }})
}

// count returns the count of samples with lo <= time < hi (unix seconds).
func (fx *fixture) count(lo, hi int64) (n, f uint64) {
	for _, ms := range fx.all {
		if s := ms / 1000; s >= lo && s < hi {
			n++
			if failAt(ms) {
				f++
			}
		}
	}
	return n, f
}

func TestSamplesTiers(t *testing.T) {
	fx := newFixture(t)
	rd := fx.reader()
	ctx := context.Background()
	to := fx.now
	cases := []struct {
		name string
		span time.Duration
		pts  int
		tier Tier
		max  int
	}{
		{"10min", 10 * time.Minute, 600, TierRaw, 600},
		{"6h", 6 * time.Hour, 600, TierMinute, 361},
		{"3d", 3 * 24 * time.Hour, 600, TierHour, 73},
		{"20d", 20 * 24 * time.Hour, 600, TierHour, 481},
		{"3y", 3 * 365 * 24 * time.Hour, 600, TierDay, 600},
		{"6h few", 6 * time.Hour, 10, TierHour, 7},
		{"3y few", 3 * 365 * 24 * time.Hour, 10, TierDay, 10},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			begin := time.Now()
			s, err := rd.Samples(ctx, SampleQuery{From: to.Add(-c.span), To: to, MaxPoints: c.pts})
			require.NoError(t, err)
			require.Less(t, time.Since(begin), 2*time.Second)
			require.Equal(t, c.tier, s.Tier)
			require.NotEmpty(t, s.Buckets)
			require.LessOrEqual(t, len(s.Buckets), min(c.pts, c.max))
			var total uint64
			for i, b := range s.Buckets {
				require.Positive(t, b.Count)
				require.LessOrEqual(t, b.Min, b.P50)
				require.LessOrEqual(t, b.P50, b.P95)
				require.LessOrEqual(t, b.P95, b.Max)
				if i > 0 {
					require.Greater(t, b.Time, s.Buckets[i-1].Time)
				}
				total += b.Count
			}
			// The buckets hold every sample of the window, with the open bucket.
			w := tierSec(s.Tier)
			first := floorTo(s.From.Unix(), w)
			want, _ := fx.count(first, to.Unix())
			require.Equal(t, want, total)
		})
	}
}

func TestAvailability(t *testing.T) {
	fx := newFixture(t)
	av, err := fx.reader().Availability(context.Background(), fx.now)
	require.NoError(t, err)
	now := fx.now.Unix()
	const day = 86400
	lo := map[Window]int64{
		Window24h:  now - day,
		Window7d:   ceilTo(now-7*day, 60),
		Window30d:  ceilTo(now-30*day, 3600),
		Window365d: ceilTo(now-365*day, day),
	}
	require.Len(t, av.Windows, 4)
	for _, w := range av.Windows {
		n, f := fx.count(lo[w.Window], now)
		require.Equal(t, n, w.Count, w.Window)
		require.Equal(t, f, w.Failures, w.Window)
	}

	var lastFail int64
	for _, ms := range fx.all {
		if failAt(ms) {
			lastFail = ms
		}
	}
	require.Equal(t, time.UnixMilli(lastFail).UTC(), av.LastFailureAt)
	var streak uint64
	for _, ms := range fx.all {
		if ms > lastFail {
			streak++
		}
	}
	require.Equal(t, streak, av.Streak)
	require.EqualValues(t, len(fx.all), av.TotalCount)
}

// A changed aggregate file is read again, so a rewritten closed day counts at
// once.
func TestAvailabilityAfterAggregateChange(t *testing.T) {
	fx := newFixture(t)
	ctx := context.Background()
	before, err := fx.reader().Availability(ctx, fx.now)
	require.NoError(t, err)

	threeDaysAgo := fx.now.Unix() - 3*86400
	file := newStore(fx.dir).listRange(TierMinute, threeDaysAgo, threeDaysAgo+1)[0]
	aggs, err := readAggFile(file.path)
	require.NoError(t, err)
	var buf []byte
	for _, a := range aggs[1:] {
		buf = a.appendJSON(buf)
	}
	require.NoError(t, writeFile(file.path, buf))

	after, err := fx.reader().Availability(ctx, fx.now)
	require.NoError(t, err)
	require.Equal(t, Window7d, after.Windows[1].Window)
	require.Equal(t, before.Windows[1].Count-aggs[0].N, after.Windows[1].Count)
}

// The 24 hours window starts 24 hours before now, to the second, and the
// longer windows count a sample once.
func TestAvailability24hCutoff(t *testing.T) {
	dir := t.TempDir()
	at := t0.Add(12*time.Hour + 45*time.Second)
	clk := &clock{t: at}
	r := newRecorder(t, dir, clk)
	feed(t, r, clk, at, 1, time.Second, okFn)
	require.NoError(t, r.Close(context.Background()))

	av, err := OpenReader(dir).Availability(context.Background(), at.Add(24*time.Hour-15*time.Second))
	require.NoError(t, err)
	for _, w := range av.Windows {
		require.EqualValues(t, 1, w.Count, w.Window)
	}
}

// With no failure in the raw tier, the streak goes on in the aggregates.
func TestStreakOldFailure(t *testing.T) {
	dir := t.TempDir()
	clk := &clock{t: t0}
	r := newRecorder(t, dir, clk)
	feed(t, r, clk, t0, 100, time.Minute, func(i int) (time.Duration, int, string) {
		if i == 10 {
			return time.Second, 1, "x"
		}
		return okFn(i)
	})
	require.NoError(t, r.Close(context.Background()))
	require.NoError(t, os.RemoveAll(filepath.Join(dir, "raw")))
	av, err := OpenReader(dir).Availability(context.Background(), t0.Add(30*time.Hour))
	require.NoError(t, err)
	require.EqualValues(t, 89, av.Streak)
	require.Equal(t, t0.Add(10*time.Minute), av.LastFailureAt)
	require.EqualValues(t, 100, av.TotalCount)
}

// The streak reads a raw hour without holding its samples in memory.
func TestStreakMemory(t *testing.T) {
	dir := t.TempDir()
	const n = 200_000
	var b []byte
	var lastMsg string
	for i := range n {
		exit := 0
		if i == 0 {
			exit = 1
		}
		b = appendRaw(b, int64(i)*10, 1000, exit, "x", &lastMsg)
	}
	path := newStore(dir).path(TierRaw, t0.Unix())
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, b, 0o600))

	var ms0, ms1 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&ms0)
	av, err := OpenReader(dir).Availability(context.Background(), t0.Add(time.Hour))
	runtime.ReadMemStats(&ms1)
	require.NoError(t, err)
	require.EqualValues(t, n-1, av.Streak)
	require.Equal(t, t0, av.LastFailureAt)
	require.Less(t, ms1.TotalAlloc-ms0.TotalAlloc, uint64(1<<20), "the reader does not hold the samples")
}
