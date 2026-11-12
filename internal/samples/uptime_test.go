// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package samples

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func upOpts(clk *clock) Options {
	o := runOpts(clk)
	o.Uptime = true
	return o
}

// recordRun records a run from start for d, at the time of its end.
func recordRun(t *testing.T, r Recorder, clk *clock, start time.Time, d time.Duration) {
	t.Helper()
	clk.t = start.Add(d)
	require.NoError(t, r.Record(context.Background(), Sample{Time: start, Duration: d}))
}

// upSum returns the up time of the lines of a tier, and the count of lines.
func upSum(t *testing.T, dir string, tier Tier) (time.Duration, int) {
	t.Helper()
	var sum int64
	n := 0
	for _, f := range newStore(filepath.Join(dir, upDirName)).list(tier) {
		require.NoError(t, scanUp(f.path, -1<<62, 1<<62, func(_, ms int64) { sum += ms; n++ }))
	}
	return time.Duration(sum) * time.Millisecond, n
}

func upBucketOf(t *testing.T, dir string, tier Tier, at time.Time) time.Duration {
	t.Helper()
	ms, err := newStore(filepath.Join(dir, upDirName)).readUp(tier, at.Unix())
	require.NoError(t, err)
	return time.Duration(ms) * time.Millisecond
}

func uptimeAt(t *testing.T, dir string, now time.Time, window time.Duration) Uptime {
	t.Helper()
	u, err := OpenReaderWith(dir, Options{Now: func() time.Time { return now }}).Uptime(context.Background(), now.Add(-window), now)
	require.NoError(t, err)
	return u
}

// A run of three hours is split over the buckets that it covers, in every tier,
// and not put in the bucket of its start.
func TestUptimeApportion(t *testing.T) {
	dir := t.TempDir()
	clk := &clock{t: t0}
	r := openWith(t, dir, upOpts(clk))
	start := t0.Add(30 * time.Minute)
	recordRun(t, r, clk, start, 3*time.Hour)
	require.NoError(t, r.Close(context.Background()))

	for i, want := range []time.Duration{30 * time.Minute, time.Hour, time.Hour, 30 * time.Minute} {
		require.Equal(t, want, upBucketOf(t, dir, TierHour, t0.Add(time.Duration(i)*time.Hour)), "hour %d", i)
	}
	require.Zero(t, upBucketOf(t, dir, TierHour, t0.Add(4*time.Hour)))
	require.Equal(t, 3*time.Hour, upBucketOf(t, dir, TierDay, t0))
	require.Equal(t, time.Minute, upBucketOf(t, dir, TierMinute, t0.Add(31*time.Minute)))
	require.Equal(t, time.Minute, upBucketOf(t, dir, TierMinute, t0.Add(3*time.Hour+29*time.Minute)))
	require.Zero(t, upBucketOf(t, dir, TierMinute, t0.Add(3*time.Hour+30*time.Minute)))
	sum, lines := upSum(t, dir, TierMinute)
	require.Equal(t, 3*time.Hour, sum)
	require.Equal(t, 180, lines)
}

// A run across midnight gives each day its own part.
func TestUptimeAcrossMidnight(t *testing.T) {
	dir := t.TempDir()
	clk := &clock{t: t0}
	r := openWith(t, dir, upOpts(clk))
	start := t0.Add(-time.Hour)
	recordRun(t, r, clk, start, 3*time.Hour)
	require.NoError(t, r.Close(context.Background()))
	require.Equal(t, time.Hour, upBucketOf(t, dir, TierDay, t0.Add(-24*time.Hour)))
	require.Equal(t, 2*time.Hour, upBucketOf(t, dir, TierDay, t0))
	// The parts are in the files of two days, and the reader sums them.
	u := uptimeAt(t, dir, t0.Add(4*time.Hour), 24*time.Hour)
	require.Equal(t, 3*time.Hour, u.Up)
}

// Two runs and a gap: the gap is not up time.
func TestUptimeGap(t *testing.T) {
	dir := t.TempDir()
	clk := &clock{t: t0}
	r := openWith(t, dir, upOpts(clk))
	recordRun(t, r, clk, t0, time.Hour)
	recordRun(t, r, clk, t0.Add(2*time.Hour), time.Hour)
	require.NoError(t, r.Close(context.Background()))
	u := uptimeAt(t, dir, t0.Add(4*time.Hour), 4*time.Hour)
	require.Equal(t, 2*time.Hour, u.Up)
	ratio, ok := u.Ratio()
	require.True(t, ok)
	require.InDelta(t, 0.5, ratio, 1e-9)
}

// recordTolerant records a run whose gap after the previous run counts as active
// time when it is not longer than tolerance.
func recordTolerant(t *testing.T, r Recorder, clk *clock, start time.Time, d, tolerance time.Duration) {
	t.Helper()
	clk.t = start.Add(d)
	require.NoError(t, r.Record(context.Background(), Sample{Time: start, Duration: d, ActiveGap: tolerance}))
}

// A gap that is not longer than the tolerance is active time, in every tier. A
// gap that is longer stays absent.
func TestUptimeTolerance(t *testing.T) {
	const tol = 11 * time.Second
	tests := []struct {
		name string
		gap  time.Duration
		want time.Duration // up time of two runs of 10 s with this gap
	}{
		{"Short", 4 * time.Second, 24 * time.Second},
		{"AtTolerance", tol, 31 * time.Second},
		{"AboveTolerance", tol + time.Second, 20 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			clk := &clock{t: t0}
			r := openWith(t, dir, upOpts(clk))
			recordTolerant(t, r, clk, t0.Add(5*time.Second), 10*time.Second, tol)
			recordTolerant(t, r, clk, t0.Add(15*time.Second+tt.gap), 10*time.Second, tol)
			require.NoError(t, r.Close(context.Background()))
			for _, tier := range upTiers {
				sum, _ := upSum(t, dir, tier)
				require.Equal(t, tt.want, sum, "tier %d", tier)
			}
		})
	}
}

// The first run of a history has no gap before it, also when an older history
// without up time has samples: the up time starts at the start of the run.
func TestUptimeToleranceFirstRun(t *testing.T) {
	dir := t.TempDir()
	clk := &clock{t: t0}
	r := openWith(t, dir, runOpts(clk)) // no Uptime: the history of an earlier version
	recordRun(t, r, clk, t0, 10*time.Second)
	require.NoError(t, r.Close(context.Background()))

	r = openWith(t, dir, upOpts(clk))
	recordTolerant(t, r, clk, t0.Add(15*time.Second), 10*time.Second, 11*time.Second)
	require.NoError(t, r.Close(context.Background()))
	sum, _ := upSum(t, dir, TierDay)
	require.Equal(t, 10*time.Second, sum)
	u := uptimeAt(t, dir, t0.Add(time.Hour), time.Hour)
	require.Equal(t, t0.Add(15*time.Second), u.Since)
}

// A run that overlaps the previous one adds only the part that is not counted,
// also with a tolerance.
func TestUptimeToleranceOverlap(t *testing.T) {
	dir := t.TempDir()
	clk := &clock{t: t0}
	r := openWith(t, dir, upOpts(clk))
	recordTolerant(t, r, clk, t0, 100*time.Second, 11*time.Second)
	recordTolerant(t, r, clk, t0.Add(50*time.Second), 100*time.Second, 11*time.Second)
	require.NoError(t, r.Close(context.Background()))
	sum, _ := upSum(t, dir, TierDay)
	require.Equal(t, 150*time.Second, sum)
}

// A fast service that restarts as designed reads near 100 %, and a stop that
// is longer than the tolerance is absent.
func TestUptimeToleranceRatio(t *testing.T) {
	dir := t.TempDir()
	clk := &clock{t: t0}
	r := openWith(t, dir, upOpts(clk))
	// A run of 100 ms each 4 s for 10 minutes, a stop of 5 minutes, and the
	// same runs for 5 minutes.
	for at := time.Duration(0); at < 10*time.Minute; at += 4 * time.Second {
		recordTolerant(t, r, clk, t0.Add(at), 100*time.Millisecond, 11*time.Second)
	}
	for at := 15 * time.Minute; at < 20*time.Minute; at += 4 * time.Second {
		recordTolerant(t, r, clk, t0.Add(at), 100*time.Millisecond, 11*time.Second)
	}
	require.NoError(t, r.Close(context.Background()))
	u := uptimeAt(t, dir, t0.Add(20*time.Minute), 20*time.Minute)
	ratio, ok := u.Ratio()
	require.True(t, ok)
	require.InDelta(t, 0.75, ratio, 0.01)
}

// A tier is the sum of its parts: the minute, hour and day tiers give the same
// total for a history that the minute tier still holds.
func TestUptimeSumAcrossTiers(t *testing.T) {
	dir := t.TempDir()
	clk := &clock{t: t0}
	r := openWith(t, dir, upOpts(clk))
	// Short runs, a run of 26 hours, and runs that cross a minute and an hour.
	for i := range 30 {
		recordRun(t, r, clk, t0.Add(time.Duration(i)*7*time.Second+500*time.Millisecond), 3*time.Second)
	}
	recordRun(t, r, clk, t0.Add(30*time.Minute), 26*time.Hour)
	recordRun(t, r, clk, t0.Add(27*time.Hour+59*time.Minute+30*time.Second), 90*time.Second)
	require.NoError(t, r.Close(context.Background()))
	m, _ := upSum(t, dir, TierMinute)
	h, _ := upSum(t, dir, TierHour)
	d, _ := upSum(t, dir, TierDay)
	require.Equal(t, 90*time.Second+26*time.Hour+90*time.Second, m)
	require.Equal(t, m, h)
	require.Equal(t, m, d)
}

// A run that overlaps the previous one adds only the time that is not counted.
func TestUptimeOverlap(t *testing.T) {
	dir := t.TempDir()
	clk := &clock{t: t0}
	r := openWith(t, dir, upOpts(clk))
	recordRun(t, r, clk, t0, 100*time.Second)
	recordRun(t, r, clk, t0.Add(50*time.Second), 100*time.Second)
	require.NoError(t, r.Close(context.Background()))
	sum, _ := upSum(t, dir, TierDay)
	require.Equal(t, 150*time.Second, sum)
}

// A reopened recorder continues the open buckets: no line is written twice and
// the part that the file holds is kept.
func TestUptimeReopen(t *testing.T) {
	dir := t.TempDir()
	clk := &clock{t: t0}
	r := openWith(t, dir, upOpts(clk))
	recordRun(t, r, clk, t0.Add(10*time.Second), 10*time.Second)
	require.NoError(t, r.Close(context.Background()))
	r = openWith(t, dir, upOpts(clk))
	recordRun(t, r, clk, t0.Add(30*time.Second), 10*time.Second)
	require.NoError(t, r.Close(context.Background()))
	for _, tier := range upTiers {
		sum, lines := upSum(t, dir, tier)
		require.Equal(t, 20*time.Second, sum, "tier %d", tier)
		require.Equal(t, 1, lines, "tier %d", tier)
	}
}

// A crash loses the up time of the open minute, in the minute tier only: the
// hour and the day are written when the minute moves.
func TestUptimeCrash(t *testing.T) {
	dir := t.TempDir()
	clk := &clock{t: t0}
	r := openWith(t, dir, upOpts(clk))
	recordRun(t, r, clk, t0.Add(10*time.Second), 20*time.Second)
	recordRun(t, r, clk, t0.Add(70*time.Second), 20*time.Second)
	_ = r // the recorder is not closed
	require.Equal(t, 20*time.Second, upBucketOf(t, dir, TierMinute, t0))
	require.Zero(t, upBucketOf(t, dir, TierMinute, t0.Add(time.Minute)))
	require.Equal(t, 40*time.Second, upBucketOf(t, dir, TierHour, t0))
	require.Equal(t, 40*time.Second, upBucketOf(t, dir, TierDay, t0))

	// The next recorder counts from the end of the newest sample.
	r = openWith(t, dir, upOpts(clk))
	recordRun(t, r, clk, t0.Add(130*time.Second), 20*time.Second)
	require.NoError(t, r.Close(context.Background()))
	day, _ := upSum(t, dir, TierDay)
	require.Equal(t, 60*time.Second, day)
	minute, _ := upSum(t, dir, TierMinute)
	require.Equal(t, 40*time.Second, minute)
}

// A run that lasts longer than a tier keeps leaves no bucket older than the
// retention of the tier.
func TestUptimeRetention(t *testing.T) {
	dir := t.TempDir()
	now := t0.Add(40 * 24 * time.Hour)
	clk := &clock{t: now}
	r := openWith(t, dir, upOpts(clk))
	require.NoError(t, r.Record(context.Background(), Sample{Time: t0, Duration: 40 * 24 * time.Hour}))
	require.NoError(t, r.Close(context.Background()))
	m, minutes := upSum(t, dir, TierMinute)
	h, hours := upSum(t, dir, TierHour)
	d, days := upSum(t, dir, TierDay)
	require.InDelta(t, 7*24*60, minutes, 2)
	require.InDelta(t, 30*24, hours, 2)
	require.Equal(t, 40, days)
	require.InDelta(t, float64(7*24*time.Hour), float64(m), float64(time.Minute))
	require.InDelta(t, float64(30*24*time.Hour), float64(h), float64(time.Hour))
	require.Equal(t, 40*24*time.Hour, d)

	// The window of 365 days reads the three tiers once each: 30 days from the
	// hour and the minute tiers, and 10 days from the day tier.
	u := uptimeAt(t, dir, now, 365*24*time.Hour)
	ratio, ok := u.Ratio()
	require.True(t, ok)
	require.InDelta(t, 1.0, ratio, 0.001)
}

// A history that has no up time gives no uptime, not 0 %. A run history that
// gets the feature later counts from its first interval.
func TestUptimeUnknown(t *testing.T) {
	dir := t.TempDir()
	clk := &clock{t: t0}
	o := runOpts(clk) // no Uptime: the format of a history that was written earlier
	r := openWith(t, dir, o)
	recordRun(t, r, clk, t0, time.Hour)
	require.NoError(t, r.Close(context.Background()))
	require.NoDirExists(t, filepath.Join(dir, upDirName))

	now := t0.Add(3 * time.Hour)
	rd := OpenReaderWith(dir, Options{Now: func() time.Time { return now }})
	av, err := rd.Availability(context.Background(), now)
	require.NoError(t, err)
	require.EqualValues(t, 1, av.Windows[0].Count)
	for _, w := range av.Windows {
		_, ok := w.Uptime.Ratio()
		require.False(t, ok, w.Window)
		require.True(t, w.Uptime.Since.IsZero(), w.Window)
	}

	// The feature starts at t0+2h: the window is counted from there.
	clk.t = now
	r = openWith(t, dir, upOpts(clk))
	recordRun(t, r, clk, t0.Add(2*time.Hour), 30*time.Minute)
	require.NoError(t, r.Close(context.Background()))
	u := uptimeAt(t, dir, now, 24*time.Hour)
	require.Equal(t, t0.Add(2*time.Hour), u.Since)
	ratio, ok := u.Ratio()
	require.True(t, ok)
	require.InDelta(t, 0.5, ratio, 1e-9)
}

// The run in progress is added at the time of the query, from its start, clipped
// to the window, and never over the part that the history recorded.
func TestUptimeActiveRun(t *testing.T) {
	dir := t.TempDir()
	clk := &clock{t: t0}
	r := openWith(t, dir, upOpts(clk))
	recordRun(t, r, clk, t0, time.Hour)
	require.NoError(t, r.Close(context.Background()))
	now := t0.Add(3 * time.Hour)

	u := uptimeAt(t, dir, now, 3*time.Hour)
	require.Equal(t, time.Hour, u.Up)
	active := u.WithActive(t0.Add(90 * time.Minute))
	require.Equal(t, 2*time.Hour+30*time.Minute, active.Up)
	ratio, _ := active.Ratio()
	require.InDelta(t, 2.5/3, ratio, 1e-9)

	// A start before the end of the recorded run is not counted twice.
	active = u.WithActive(t0.Add(30 * time.Minute))
	require.Equal(t, 3*time.Hour, active.Up)

	// A start before the window is clipped to it.
	u6 := uptimeAt(t, dir, now, 2*time.Hour)
	require.Zero(t, u6.Up)
	active = u6.WithActive(t0.Add(-10 * time.Hour))
	ratio, ok := active.Ratio()
	require.True(t, ok)
	require.InDelta(t, 1.0, ratio, 1e-9)

	// A history with no run, and a run in progress: the window counts from
	// the start of that run.
	empty := uptimeAt(t, t.TempDir(), now, 6*time.Hour)
	_, ok = empty.Ratio()
	require.False(t, ok)
	active = empty.WithActive(now.Add(-2 * time.Hour))
	require.Equal(t, now.Add(-2*time.Hour), active.Since)
	ratio, ok = active.Ratio()
	require.True(t, ok)
	require.InDelta(t, 1.0, ratio, 1e-9)

	// A run that has not started gives nothing.
	require.Equal(t, empty, empty.WithActive(time.Time{}))
}

// The availability gives the uptime of the four windows.
func TestAvailabilityUptime(t *testing.T) {
	dir := t.TempDir()
	clk := &clock{t: t0}
	r := openWith(t, dir, upOpts(clk))
	recordRun(t, r, clk, t0, 6*time.Hour)
	require.NoError(t, r.Close(context.Background()))
	now := t0.Add(12 * time.Hour)
	av, err := OpenReaderWith(dir, Options{Now: func() time.Time { return now }}).Availability(context.Background(), now)
	require.NoError(t, err)
	for _, w := range av.Windows {
		ratio, ok := w.Uptime.Ratio()
		require.True(t, ok, w.Window)
		// The history starts at t0, so each window counts 12 hours.
		require.InDelta(t, 0.5, ratio, 1e-9, w.Window)
	}
}

// A window in the past counts the up time between its two ends, on the
// buckets of the tier. The reader does not count beyond its own time.
func TestUptimePastWindow(t *testing.T) {
	dir := t.TempDir()
	clk := &clock{t: t0}
	r := openWith(t, dir, upOpts(clk))
	recordRun(t, r, clk, t0, time.Hour)
	recordRun(t, r, clk, t0.Add(2*time.Hour), time.Hour)
	require.NoError(t, r.Close(context.Background()))
	now := t0.Add(10 * time.Hour)
	rd := OpenReaderWith(dir, Options{Now: func() time.Time { return now }})

	u, err := rd.Uptime(context.Background(), t0.Add(30*time.Minute), t0.Add(2*time.Hour+30*time.Minute))
	require.NoError(t, err)
	require.Equal(t, t0.Add(30*time.Minute), u.Start)
	require.Equal(t, t0.Add(2*time.Hour+30*time.Minute), u.To)
	require.Equal(t, time.Hour, u.Up)
	ratio, ok := u.Ratio()
	require.True(t, ok)
	require.InDelta(t, 0.5, ratio, 1e-9)

	// An end after the reader time is cut to it.
	u, err = rd.Uptime(context.Background(), t0, now.Add(5*time.Hour))
	require.NoError(t, err)
	require.Equal(t, now, u.To)

	// A window of less than one minute has no bucket.
	u, err = rd.Uptime(context.Background(), t0.Add(10*time.Second), t0.Add(50*time.Second))
	require.NoError(t, err)
	_, ok = u.Ratio()
	require.False(t, ok)
}

// The last minute of a run that covers more than one minute is written at once:
// the next run can last hours, and no write follows it.
func TestUptimeLongRunWritesItsLastMinute(t *testing.T) {
	dir := t.TempDir()
	clk := &clock{t: t0}
	r := openWith(t, dir, upOpts(clk))
	recordRun(t, r, clk, t0.Add(10*time.Second), 125*time.Second)
	// The recorder is not closed and not flushed.
	require.Equal(t, 15*time.Second, upBucketOf(t, dir, TierMinute, t0.Add(2*time.Minute)))
	require.Equal(t, 125*time.Second, upBucketOf(t, dir, TierHour, t0))
	// A run that starts in the same minute adds to it.
	recordRun(t, r, clk, t0.Add(2*time.Minute+30*time.Second), 10*time.Second)
	require.NoError(t, r.Close(context.Background()))
	require.Equal(t, 25*time.Second, upBucketOf(t, dir, TierMinute, t0.Add(2*time.Minute)))
	sum, _ := upSum(t, dir, TierMinute)
	require.Equal(t, 135*time.Second, sum)
}

// A bucket that a run covers has its up time, also when no run started in it.
// The live run adds its time from the end of the newest recorded run.
func TestSeriesUp(t *testing.T) {
	dir := t.TempDir()
	clk := &clock{t: t0}
	r := openWith(t, dir, upOpts(clk))
	recordRun(t, r, clk, t0.Add(30*time.Minute), 3*time.Hour)
	require.NoError(t, r.Close(context.Background()))
	now := t0.Add(6 * time.Hour)
	reader := OpenReaderWith(dir, Options{Now: func() time.Time { return now }})
	ctx := context.Background()

	// 6 hours at 60 points: the minute tier. The run has one sample, in its
	// start minute, and an up time in each minute that it covers.
	series, err := reader.Samples(ctx, SampleQuery{From: t0, To: now, MaxPoints: 400})
	require.NoError(t, err)
	require.Equal(t, TierMinute, series.Tier)
	require.Len(t, series.Buckets, 180)
	require.Equal(t, uint64(1), series.Buckets[0].Count)
	require.Equal(t, time.Minute, series.Buckets[0].Up)
	require.Zero(t, series.Buckets[1].Count)
	require.Equal(t, time.Minute, series.Buckets[1].Up)

	// The hour tier: the up time of each hour, and no bucket without up time.
	series, err = reader.Samples(ctx, SampleQuery{From: t0, To: now, MaxPoints: 12})
	require.NoError(t, err)
	require.Equal(t, TierHour, series.Tier)
	var ups []time.Duration
	for _, b := range series.Buckets {
		ups = append(ups, b.Up)
	}
	require.Equal(t, []time.Duration{30 * time.Minute, time.Hour, time.Hour, 30 * time.Minute}, ups)

	// A run in progress from 5:10 adds its time from its start, in the hours
	// that it covers, up to the end of the series.
	u, err := reader.Uptime(ctx, t0, now)
	require.NoError(t, err)
	live := series.WithActive(t0.Add(5*time.Hour+10*time.Minute), u.Last)
	require.Len(t, live.Buckets, 5)
	require.Equal(t, t0.Add(5*time.Hour), live.Buckets[4].Time)
	require.Equal(t, 50*time.Minute, live.Buckets[4].Up)
	require.Equal(t, 4, len(series.Buckets), "the series is not changed")

	// A run in progress that started before the end of the newest recorded run
	// adds nothing before that end: it is not counted twice.
	live = series.WithActive(t0.Add(time.Hour), u.Last)
	require.Equal(t, series.Buckets[1].Up, live.Buckets[1].Up)
	require.Equal(t, series.Buckets[2].Up, live.Buckets[2].Up)
	require.Equal(t, time.Hour, live.Buckets[3].Up)
}

// A history with no up store has no up time, and a step never has it.
func TestSeriesNoUp(t *testing.T) {
	dir := t.TempDir()
	clk := &clock{t: t0}
	r := newRecorder(t, dir, clk)
	feed(t, r, clk, t0, 10, time.Minute, okFn)
	require.NoError(t, r.Close(context.Background()))
	now := t0.Add(time.Hour)
	series, err := OpenReaderWith(dir, Options{Now: func() time.Time { return now }}).
		Samples(context.Background(), SampleQuery{From: t0, To: now, MaxPoints: 100})
	require.NoError(t, err)
	require.NotEmpty(t, series.Buckets)
	for _, b := range series.Buckets {
		require.Zero(t, b.Up)
	}
}
