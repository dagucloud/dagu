// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package samples

import (
	"context"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The percentiles of a histogram are within the width of a bin of the exact
// percentiles.
func TestPercentileAccuracy(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	var a agg
	var us []int64
	for range 20000 {
		v := int64(math.Exp(math.Log(40000) + 0.8*rng.NormFloat64()))
		us = append(us, v)
		a.add(v, false)
	}
	slices.Sort(us)
	exact := func(p float64) float64 { return float64(us[int(math.Ceil(p*float64(len(us))))-1]) }
	for _, p := range []float64{0.5, 0.95} {
		got := float64(a.quantile(p))
		require.InDelta(t, exact(p), got, exact(p)*0.0225, "p%v", p)
	}
	var one agg
	one.add(41234, false)
	require.Equal(t, int64(41234), one.quantile(0.5))

	// Executions of a month or more keep the same precision.
	var long agg
	var longUS []int64
	for i := range 20 {
		v := (30*24*time.Hour + time.Duration(i)*36*time.Hour).Microseconds()
		longUS = append(longUS, v)
		long.add(v, false)
	}
	for _, p := range []float64{0.5, 0.95} {
		want := float64(longUS[int(math.Ceil(p*float64(len(longUS))))-1])
		require.InDelta(t, want, float64(long.quantile(p)), want*0.0225, "p%v", p)
	}
}

// Two histograms add, so that the roll-up of parts gives the aggregate of the whole.
func TestMergeEqualsWhole(t *testing.T) {
	var whole, left, right agg
	for i := range 1000 {
		v := int64(100 + i*7)
		whole.add(v, i%10 == 0)
		if i < 400 {
			left.add(v, i%10 == 0)
		} else {
			right.add(v, i%10 == 0)
		}
	}
	left.merge(&right)
	require.Equal(t, whole, left)
}

// A second roll-up gives the same files, and the rebuilt tiers equal the tiers of the recorder.
func TestRollupIdempotent(t *testing.T) {
	dir := t.TempDir()
	clk := &clock{t: t0}
	r := newRecorder(t, dir, clk)
	feed(t, r, clk, t0.Add(10*time.Minute), 3*3600, time.Second, func(i int) (time.Duration, int, string) {
		if i%977 == 0 {
			return 5 * time.Second, 1, "boom"
		}
		return okFn(i)
	})
	require.NoError(t, r.Close(context.Background()))
	before := snapshot(t, dir)

	st := newStore(dir)
	far := t0.Add(5 * time.Hour).Unix()
	require.NoError(t, st.repair(far))
	require.Equal(t, before, snapshot(t, dir))
	require.NoError(t, st.repair(far))
	require.Equal(t, before, snapshot(t, dir))

	n, f := sumAggs(t, dir, TierMinute)
	require.EqualValues(t, 3*3600, n)
	require.EqualValues(t, 12, f)
	for _, tier := range []Tier{TierHour, TierDay} {
		n2, f2 := sumAggs(t, dir, tier)
		require.Equal(t, n, n2, tier)
		require.Equal(t, f, f2, tier)
	}
}

// A tier drops a file only when its whole span is past the retention, and the
// next tier keeps its aggregates.
func TestRetention(t *testing.T) {
	dir := t.TempDir()
	clk := &clock{t: t0}
	r := newRecorder(t, dir, clk)
	const n = 10 * 24 * 120
	feed(t, r, clk, t0, n, 30*time.Second, okFn)
	require.NoError(t, r.Close(context.Background()))
	now := clk.t.Unix()

	st := newStore(dir)
	for _, tier := range []Tier{TierRaw, TierMinute, TierHour} {
		files := st.list(tier)
		require.NotEmpty(t, files, tier)
		for _, f := range files {
			require.Greater(t, f.end+retentionSec(tier), now, "%v %s", tier, f.path)
		}
	}
	require.Less(t, len(st.list(TierRaw)), 26)
	require.Less(t, len(st.list(TierMinute)), 9)
	require.Greater(t, st.list(TierMinute)[0].start, t0.Unix())

	// The minute tier holds only the last days, the hour and day tiers hold all.
	minutes, _ := sumAggs(t, dir, TierMinute)
	require.Less(t, minutes, uint64(n))
	hours, _ := sumAggs(t, dir, TierHour)
	days, _ := sumAggs(t, dir, TierDay)
	require.EqualValues(t, n, hours)
	require.EqualValues(t, n, days)
}

// A crash leaves temporary files, cut lines and lost aggregates. The next open repairs them.
func TestCrashRepair(t *testing.T) {
	dir := t.TempDir()
	clk := &clock{t: t0}
	r := newRecorder(t, dir, clk)
	feed(t, r, clk, t0, 3*3600, time.Second, okFn)
	require.NoError(t, r.Close(context.Background()))
	want := tierSnapshot(t, dir)
	require.NotEmpty(t, want)

	// Lost tiers are rebuilt from the finer tiers.
	for _, d := range []string{"minute", "hour", "day"} {
		require.NoError(t, os.RemoveAll(filepath.Join(dir, d)))
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "raw", "2026030101.jsonl.tmp"), []byte("x"), 0o600))
	clk.t = t0.Add(25 * time.Hour)
	r = newRecorder(t, dir, clk)
	require.NoError(t, r.Close(context.Background()))
	require.Equal(t, want, tierSnapshot(t, dir))
	require.NoFileExists(t, filepath.Join(dir, "raw", "2026030101.jsonl.tmp"))

	// A cut raw line is ignored; the minute is built from the lines that are whole.
	raw := filepath.Join(dir, "raw", "2026030102.jsonl")
	b, err := os.ReadFile(raw) //nolint:gosec
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(raw, b[:len(b)-3], 0o600))
	clk.t = t0.Add(26 * time.Hour)
	r = newRecorder(t, dir, clk)
	require.NoError(t, r.Close(context.Background()))
	aggs, err := readAggFile(filepath.Join(dir, "minute", "20260301.jsonl"))
	require.NoError(t, err)
	require.Equal(t, uint64(59), aggs[len(aggs)-1].N)
}

// A crash loses the samples of the open minute and nothing else.
func TestCrashLosesOneMinute(t *testing.T) {
	dir := t.TempDir()
	clk := &clock{t: t0}
	r := newRecorder(t, dir, clk)
	feed(t, r, clk, t0, 150, time.Second, okFn)
	// No Close: the recorder is dropped. The operating system releases the
	// files of a process that ends, so the test releases them too.
	require.NoError(t, r.(*recorder).st.closeAll())
	aggs, err := readAggFile(filepath.Join(dir, "minute", "20260301.jsonl"))
	require.NoError(t, err)
	require.Len(t, aggs, 2)

	clk.t = t0.Add(time.Hour)
	r = newRecorder(t, dir, clk)
	feed(t, r, clk, clk.t, 10, time.Second, okFn)
	require.NoError(t, r.Close(context.Background()))
	n, _ := sumAggs(t, dir, TierMinute)
	require.EqualValues(t, 130, n)

	page, err := OpenReader(dir).Events(context.Background(), EventQuery{})
	require.NoError(t, err)
	var kinds []EventKind
	for _, e := range page.Events {
		kinds = append(kinds, e.Kind)
	}
	require.Equal(t, []EventKind{EventStopped, EventStarted, EventStarted}, kinds)
}

func TestEvents(t *testing.T) {
	dir := t.TempDir()
	clk := &clock{t: t0}
	ctx := context.Background()
	r := newRecorder(t, dir, clk)
	// 10 ok, 150 minutes failed, 10 ok.
	feed(t, r, clk, t0, 170, time.Minute, func(i int) (time.Duration, int, string) {
		if i >= 10 && i < 160 {
			return time.Second, 7, "refused"
		}
		return okFn(i)
	})
	require.NoError(t, r.Close(ctx))
	rd := OpenReader(dir)

	page, err := rd.Events(ctx, EventQuery{})
	require.NoError(t, err)
	var got []string
	for _, e := range page.Events {
		got = append(got, string(e.Kind))
	}
	// Newest first: stopped, recovered, failed after 2 h, failed after 1 h, failed, started.
	require.Equal(t, []string{"stopped", "recovered", "failed", "failed", "failed", "started"}, got)
	rec := page.Events[1]
	require.Equal(t, uint64(150), rec.FailedCount)
	require.Equal(t, 150*time.Minute, rec.Outage)
	require.Equal(t, t0.Add(160*time.Minute), rec.Time)
	require.Equal(t, uint64(121), page.Events[2].FailedCount)
	require.Equal(t, uint64(61), page.Events[3].FailedCount)
	require.Equal(t, uint64(1), page.Events[4].FailedCount)
	require.Equal(t, "refused", page.Events[4].Message)
	require.Equal(t, 7, page.Events[4].ExitCode)
	require.Empty(t, page.NextCursor)

	// Pages by cursor give the same list.
	var paged []Event
	cur := ""
	for {
		p, err := rd.Events(ctx, EventQuery{Limit: 2, Before: cur})
		require.NoError(t, err)
		paged = append(paged, p.Events...)
		if p.NextCursor == "" {
			break
		}
		require.Len(t, p.Events, 2)
		cur = p.NextCursor
	}
	require.Equal(t, page.Events, paged)

	// The window cuts the list.
	w, err := rd.Events(ctx, EventQuery{From: t0.Add(time.Hour), To: t0.Add(161 * time.Minute)})
	require.NoError(t, err)
	require.Len(t, w.Events, 3)
	_, err = rd.Events(ctx, EventQuery{Before: "bad"})
	require.ErrorIs(t, err, ErrInvalidCursor)
	// A cursor that decodes but that the store did not give is rejected too.
	for _, c := range []string{encodeCursor(0), encodeCursor(1), encodeCursor(1 << 40)} {
		_, err = rd.Events(ctx, EventQuery{Before: c})
		require.ErrorIs(t, err, ErrInvalidCursor, c)
	}
	// A store without events still rejects a cursor it did not make.
	for _, c := range []string{"bad", encodeCursor(5)} {
		_, err = OpenReader(filepath.Join(t.TempDir(), "none")).Events(ctx, EventQuery{Before: c})
		require.ErrorIs(t, err, ErrInvalidCursor, c)
	}
}

// An outage goes on over a restart: no second failed event, one recovered event with the whole outage.
func TestOutageAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	clk := &clock{t: t0}
	ctx := context.Background()
	fail := func(int) (time.Duration, int, string) { return time.Second, 1, "x" }
	r := newRecorder(t, dir, clk)
	feed(t, r, clk, t0, 5, time.Second, fail)
	require.NoError(t, r.Close(ctx))
	clk.t = t0.Add(time.Minute)
	r = newRecorder(t, dir, clk)
	feed(t, r, clk, t0.Add(time.Minute), 5, time.Second, fail)
	feed(t, r, clk, t0.Add(2*time.Minute), 1, time.Second, okFn)
	require.NoError(t, r.Close(ctx))

	page, err := OpenReader(dir).Events(ctx, EventQuery{})
	require.NoError(t, err)
	var got []string
	for _, e := range page.Events {
		got = append(got, string(e.Kind))
	}
	require.Equal(t, []string{"stopped", "recovered", "started", "stopped", "failed", "started"}, got)
	require.Equal(t, 2*time.Minute, page.Events[1].Outage)
	// The count goes on over the restart: five failed samples before it, five after.
	require.EqualValues(t, 10, page.Events[1].FailedCount)
}

// An outage goes on over a crash, which writes no stopped event: the count
// keeps the failed samples that the store holds after the last counted event.
func TestOutageAcrossCrash(t *testing.T) {
	dir := t.TempDir()
	clk := &clock{t: t0}
	ctx := context.Background()
	fail := func(int) (time.Duration, int, string) { return time.Second, 1, "x" }
	r := newRecorder(t, dir, clk)
	feed(t, r, clk, t0, 120, time.Second, fail)
	require.NoError(t, r.Flush(ctx))
	require.NoError(t, r.(*recorder).st.closeAll())

	clk.t = t0.Add(3 * time.Minute)
	r = newRecorder(t, dir, clk)
	feed(t, r, clk, clk.t, 1, time.Second, okFn)
	require.NoError(t, r.Close(ctx))

	page, err := OpenReader(dir).Events(ctx, EventQuery{Limit: 2})
	require.NoError(t, err)
	rec := page.Events[1]
	require.Equal(t, EventRecovered, rec.Kind)
	require.EqualValues(t, 120, rec.FailedCount)
}

// A sample that a flush wrote before a crash stays in the aggregates, though
// its minute had not ended when the store opened again.
func TestCrashKeepsFlushedMinute(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	clk := &clock{t: t0.Add(5 * time.Second)}
	r := newRecorder(t, dir, clk)
	feed(t, r, clk, clk.t, 1, time.Second, okFn)
	require.NoError(t, r.Flush(ctx))
	require.NoError(t, r.(*recorder).st.closeAll())

	clk.t = t0.Add(50 * time.Second)
	r = newRecorder(t, dir, clk)
	feed(t, r, clk, t0.Add(65*time.Second), 1, time.Second, okFn)
	require.NoError(t, r.Close(ctx))

	rd := OpenReaderWith(dir, Options{Now: clk.now})
	s, err := rd.Samples(ctx, SampleQuery{From: t0, To: t0.Add(2 * time.Minute), MaxPoints: 10})
	require.NoError(t, err)
	require.Equal(t, TierMinute, s.Tier)
	require.Len(t, s.Buckets, 2)
	av, err := rd.Availability(ctx, clk.t)
	require.NoError(t, err)
	require.EqualValues(t, 2, av.TotalCount)
}

// A crash can cut the last line of a file. The next record starts on a line
// of its own, so that it is read.
func TestAppendAfterCutLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cut.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(`{"cut`), 0o600))
	a := &appender{path: path}
	require.NoError(t, a.write([]byte("{}\n")))
	require.NoError(t, a.close())
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "{\"cut\n{}\n", string(got))
}

// Two steps never share a store, whatever their names.
func TestDirIsUniquePerStep(t *testing.T) {
	names := []string{"a.b", "a b", "a_b", "Beat", "beat", "抽出", "変換", strings.Repeat("x", 100), strings.Repeat("x", 101)}
	seen := map[string]string{}
	for _, name := range names {
		// Some file systems ignore case, so the directories differ in more than case.
		dir := strings.ToLower(Dir("attempt", name))
		require.NotContains(t, seen, dir, "%q and %q", seen[dir], name)
		seen[dir] = name
	}
	// A name that is kept as it is gives a plain directory.
	require.Equal(t, filepath.Join("attempt", "samples", "beat"), Dir("attempt", "beat"))
}

// A backfill that stops early still closes its files, and writes what it has.
func TestBackfillClosesOnError(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	start := time.Date(2029, 3, 10, 0, 0, 0, 0, time.UTC)
	err := Backfill(ctx, dir, BackfillOptions{
		Start: start, End: start.Add(time.Hour), Interval: time.Minute, Now: start.Add(time.Hour),
		Next: func(int64) (time.Duration, int, string) {
			cancel()
			return time.Second, 0, ""
		},
	})
	require.ErrorIs(t, err, context.Canceled)
	events, err := os.ReadFile(filepath.Join(dir, eventsFileName))
	require.NoError(t, err)
	require.Contains(t, string(events), string(EventStarted))
	require.NoError(t, os.RemoveAll(dir))

	err = Backfill(context.Background(), t.TempDir(), BackfillOptions{
		Start: start, End: start.Add(time.Hour), Interval: 500 * time.Microsecond, Now: start.Add(time.Hour),
		Next: func(int64) (time.Duration, int, string) { return time.Second, 0, "" },
	})
	require.Error(t, err)
}

// A backfill that ends inside an hour still writes that hour and its day, so
// that the tiers hold every sample, also past the retention of the finer tiers.
func TestBackfillWritesOpenBuckets(t *testing.T) {
	start := time.Date(2029, 3, 10, 0, 0, 0, 0, time.UTC)
	end := start.Add(26*time.Hour + 30*time.Minute)
	const n = 26*60 + 30
	for _, now := range []time.Time{end, end.Add(60 * 24 * time.Hour)} {
		dir := t.TempDir()
		err := Backfill(context.Background(), dir, BackfillOptions{
			Start: start, End: end, Interval: time.Minute, Now: now,
			Next: func(int64) (time.Duration, int, string) { return time.Second, 0, "" },
		})
		require.NoError(t, err)
		tiers := []Tier{TierMinute, TierHour, TierDay}
		if now.After(end) {
			tiers = []Tier{TierDay}
		}
		for _, tier := range tiers {
			got, _ := sumAggs(t, dir, tier)
			require.EqualValues(t, n, got, "%v at %v", tier, now)
		}
	}
}

// A backfill that ends inside an outage leaves the count to the next start.
func TestBackfillOutageAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	start := time.Date(2029, 3, 10, 0, 0, 0, 0, time.UTC)
	end := start.Add(90 * time.Minute)
	err := Backfill(ctx, dir, BackfillOptions{
		Start: start, End: end, Interval: time.Minute, Now: end,
		Next: func(int64) (time.Duration, int, string) { return time.Second, 1, "x" },
	})
	require.NoError(t, err)
	clk := &clock{t: end}
	r := newRecorder(t, dir, clk)
	feed(t, r, clk, end, 1, time.Second, okFn)
	require.NoError(t, r.Close(ctx))

	page, err := OpenReader(dir).Events(ctx, EventQuery{})
	require.NoError(t, err)
	rec := page.Events[1]
	require.Equal(t, EventRecovered, rec.Kind)
	require.EqualValues(t, 90, rec.FailedCount)
	require.Equal(t, 90*time.Minute, rec.Outage)
}

// A clock that steps back stores the sample at the time of the newest sample,
// so that the raw buckets stay in time order.
func TestClockStepsBack(t *testing.T) {
	dir := t.TempDir()
	clk := &clock{t: t0}
	r := newRecorder(t, dir, clk)
	ctx := context.Background()
	for _, ts := range []time.Time{t0.Add(59 * time.Second), t0.Add(-time.Second), t0.Add(10 * time.Second), t0.Add(70 * time.Second)} {
		require.NoError(t, r.Record(ctx, Sample{Time: ts, Duration: time.Second}))
	}
	require.NoError(t, r.Flush(ctx))

	rd := OpenReaderWith(dir, Options{Now: func() time.Time { return t0.Add(2 * time.Minute) }})
	s, err := rd.Samples(ctx, SampleQuery{From: t0.Add(-time.Minute), To: t0.Add(2 * time.Minute)})
	require.NoError(t, err)
	require.Equal(t, TierRaw, s.Tier)
	var times []time.Time
	var counts []uint64
	for _, b := range s.Buckets {
		times = append(times, b.Time)
		counts = append(counts, b.Count)
	}
	require.Equal(t, []time.Time{t0.Add(59 * time.Second), t0.Add(70 * time.Second)}, times)
	require.Equal(t, []uint64{3, 1}, counts)
	require.NoError(t, r.Close(ctx))

	// The rule holds over a restart: the newest stored sample bounds the next.
	clk.t = t0.Add(30 * time.Second)
	r = newRecorder(t, dir, clk)
	require.NoError(t, r.Record(ctx, Sample{Time: clk.t, Duration: time.Second}))
	require.NoError(t, r.Close(ctx))
	s, err = rd.Samples(ctx, SampleQuery{From: t0.Add(-time.Minute), To: t0.Add(2 * time.Minute)})
	require.NoError(t, err)
	times, counts = nil, nil
	for _, b := range s.Buckets {
		times = append(times, b.Time)
		counts = append(counts, b.Count)
	}
	require.Equal(t, []time.Time{t0.Add(59 * time.Second), t0.Add(70 * time.Second)}, times)
	require.Equal(t, []uint64{3, 2}, counts)
}

func TestEmptyStore(t *testing.T) {
	rd := OpenReader(filepath.Join(t.TempDir(), "none"))
	ctx := context.Background()
	s, err := rd.Samples(ctx, SampleQuery{From: t0, To: t0.Add(time.Hour), MaxPoints: 100})
	require.NoError(t, err)
	require.Empty(t, s.Buckets)
	e, err := rd.Events(ctx, EventQuery{})
	require.NoError(t, err)
	require.Empty(t, e.Events)
	a, err := rd.Availability(ctx, t0)
	require.NoError(t, err)
	require.Zero(t, a.TotalCount)
	require.Len(t, a.Windows, 4)
}

// The recorder holds one minute in memory and the files grow by the spec.
func TestBounds(t *testing.T) {
	dir := t.TempDir()
	clk := &clock{t: t0}
	r := newRecorder(t, dir, clk)
	rec := r.(*recorder)
	var ms0, ms1 runtime.MemStats
	maxBuf := 0
	const n = 100_000
	for i := range n {
		ts := t0.Add(time.Duration(i) * time.Second)
		clk.t = ts
		exit, msg := 0, ""
		if i%5000 < 3 {
			exit, msg = 1, "refused"
		}
		require.NoError(t, r.Record(context.Background(), Sample{Time: ts, Duration: okDur(i), ExitCode: exit, Message: msg}))
		maxBuf = max(maxBuf, len(rec.buf))
		if i == 3600 {
			runtime.GC()
			runtime.ReadMemStats(&ms0)
		}
	}
	runtime.GC()
	runtime.ReadMemStats(&ms1)
	require.NoError(t, r.Close(context.Background()))

	require.LessOrEqual(t, maxBuf, 60*40, "the buffer holds one minute")
	require.Less(t, int64(ms1.HeapAlloc)-int64(ms0.HeapAlloc), int64(2<<20), "the heap does not grow with the samples")

	var raw, other int64
	for k, v := range snapshot(t, dir) {
		if strings.HasPrefix(k, "raw") {
			raw += int64(len(v))
		} else {
			other += int64(len(v))
		}
	}
	days := float64(n) / 86400
	require.Less(t, float64(raw), 3*1024*1024*(days+1))
	require.Less(t, raw, int64(2<<20), "raw files hold the last day only")
	require.Less(t, other, int64(1<<20), "the aggregates are small")
}

func okDur(i int) time.Duration { return time.Duration(30+i%40) * time.Millisecond }

// runOpts are the options of a run history.
func runOpts(clk *clock) Options {
	return Options{Now: clk.now, NoLifecycleEvents: true, WriteEach: true}
}

func openWith(t *testing.T, dir string, o Options) Recorder {
	t.Helper()
	r, err := OpenRecorder(context.Background(), dir, o)
	require.NoError(t, err)
	return r
}

func allEvents(t *testing.T, dir string) []Event {
	t.Helper()
	page, err := OpenReader(dir).Events(context.Background(), EventQuery{Limit: maxEvents})
	require.NoError(t, err)
	return page.Events
}

func kinds(evs []Event) []EventKind {
	var out []EventKind
	for _, e := range evs {
		out = append(out, e.Kind)
	}
	return out
}

func mustFirst(t *testing.T, rd Reader) Event {
	t.Helper()
	p, err := rd.Events(context.Background(), EventQuery{Limit: 1})
	require.NoError(t, err)
	require.Len(t, p.Events, 1)
	return p.Events[0]
}

// With NoLifecycleEvents, the store has no started and no stopped event, and an
// outage goes on over a stop and over a crash.
func TestNoLifecycleEvents(t *testing.T) {
	ctx := context.Background()
	fail := func(int) (time.Duration, int, string) { return time.Second, 1, "x" }

	dir := t.TempDir()
	clk := &clock{t: t0}
	o := Options{Now: clk.now, NoLifecycleEvents: true}
	r := openWith(t, dir, o)
	require.NoError(t, r.Close(ctx))
	require.NoFileExists(t, filepath.Join(dir, eventsFileName))

	r = openWith(t, dir, o)
	feed(t, r, clk, t0, 5, time.Second, fail)
	require.NoError(t, r.Close(ctx))
	r = openWith(t, dir, o)
	feed(t, r, clk, t0.Add(time.Minute), 5, time.Second, fail)
	feed(t, r, clk, t0.Add(2*time.Minute), 1, time.Second, okFn)
	require.NoError(t, r.Close(ctx))
	evs := allEvents(t, dir)
	require.Equal(t, []EventKind{EventRecovered, EventFailed}, kinds(evs))
	require.Equal(t, 2*time.Minute, evs[0].Outage)
	require.EqualValues(t, 10, evs[0].FailedCount)

	// A crash: the first recorder is not closed.
	dir = t.TempDir()
	o.WriteEach = true
	r = openWith(t, dir, o)
	feed(t, r, clk, t0, 5, time.Second, fail)
	r = openWith(t, dir, o)
	feed(t, r, clk, t0.Add(time.Minute), 5, time.Second, fail)
	feed(t, r, clk, t0.Add(2*time.Minute), 1, time.Second, okFn)
	require.NoError(t, r.Close(ctx))
	evs = allEvents(t, dir)
	require.Equal(t, []EventKind{EventRecovered, EventFailed}, kinds(evs))
	require.EqualValues(t, 10, evs[0].FailedCount)
}

// With WriteEach, a reader opened at the same time sees a sample at once.
func TestWriteEach(t *testing.T) {
	dir := t.TempDir()
	clk := &clock{t: t0}
	ctx := context.Background()
	r := openWith(t, dir, runOpts(clk))
	rd := OpenReaderWith(dir, Options{Now: clk.now})
	for i := 1; i <= 3; i++ {
		feed(t, r, clk, t0.Add(time.Duration(i)*time.Second), 1, time.Second, okFn)
		s, err := rd.Samples(ctx, SampleQuery{From: t0, To: t0.Add(time.Hour), MaxPoints: 100})
		require.NoError(t, err)
		var n uint64
		for _, b := range s.Buckets {
			n += b.Count
		}
		require.EqualValues(t, i, n)
		a, err := rd.Availability(ctx, clk.t)
		require.NoError(t, err)
		require.EqualValues(t, i, a.Windows[0].Count)
	}
}

// The failed and the recovered events keep the run ID of the sample that caused
// them. The raw line does not.
func TestEventRunID(t *testing.T) {
	dir := t.TempDir()
	clk := &clock{t: t0}
	r := openWith(t, dir, runOpts(clk))
	ctx := context.Background()
	require.NoError(t, r.Record(ctx, Sample{Time: t0, Duration: time.Second, ExitCode: 1, Message: "boom", RunID: "run-a"}))
	require.NoError(t, r.Record(ctx, Sample{Time: t0.Add(time.Minute), Duration: time.Second, RunID: "run-b"}))
	require.NoError(t, r.Close(ctx))
	evs := allEvents(t, dir)
	require.Equal(t, []EventKind{EventRecovered, EventFailed}, kinds(evs))
	require.Equal(t, "run-b", evs[0].RunID)
	require.Equal(t, "run-a", evs[1].RunID)
	for name, content := range snapshot(t, dir) {
		if filepath.Dir(name) == "raw" {
			require.NotContains(t, content, "run-")
		}
	}
}

// A gap event needs a long pause and no open outage, and it survives a reopen.
func TestGapEvent(t *testing.T) {
	dir := t.TempDir()
	clk := &clock{t: t0}
	o := runOpts(clk)
	ctx := context.Background()
	r := openWith(t, dir, o)
	rec := func(at time.Duration, exit int, id string) {
		t.Helper()
		clk.t = t0.Add(at)
		require.NoError(t, r.Record(ctx, Sample{
			Time: t0.Add(at), Duration: 10 * time.Second, ExitCode: exit, RunID: id, GapAfter: time.Minute,
		}))
	}
	rec(0, 0, "r1")              // first sample: no gap
	rec(40*time.Second, 0, "r2") // 30 s after the end: under the threshold
	rec(5*time.Minute, 0, "r3")  // 4 min 10 s after the end of r2: gap
	rec(10*time.Minute, 1, "r4") // gap, then the outage opens
	rec(30*time.Minute, 1, "r5") // in the outage: no gap
	rec(time.Hour, 0, "r6")      // recovery: no gap
	require.NoError(t, r.Close(ctx))
	r = openWith(t, dir, o)
	rec(3*time.Hour, 0, "r7") // the end of r6 comes back from the store
	require.NoError(t, r.Close(ctx))

	var gaps []Event
	for _, e := range allEvents(t, dir) {
		if e.Kind == EventGap {
			gaps = append(gaps, e)
		}
	}
	slices.Reverse(gaps)
	require.Len(t, gaps, 3)
	want := []struct {
		id  string
		end time.Duration
		gap time.Duration
	}{
		{"r3", 50 * time.Second, 4*time.Minute + 10*time.Second},
		{"r4", 5*time.Minute + 10*time.Second, 4*time.Minute + 50*time.Second},
		{"r7", time.Hour + 10*time.Second, time.Hour + 59*time.Minute + 50*time.Second},
	}
	for i, w := range want {
		require.Equal(t, w.id, gaps[i].RunID)
		require.Equal(t, t0.Add(w.end), gaps[i].Time)
		require.Equal(t, w.gap, gaps[i].Gap)
	}
	// Event times never decrease.
	evs := allEvents(t, dir)
	for i := 1; i < len(evs); i++ {
		require.False(t, evs[i-1].Time.Before(evs[i].Time))
	}
}

// The events file stays under its bound, and a cursor stays exact over the
// compactions.
func TestEventsBound(t *testing.T) {
	const bound = 4 << 20
	dir := t.TempDir()
	clk := &clock{t: t0}
	o := Options{Now: clk.now, NoLifecycleEvents: true, MaxEventBytes: bound}
	ctx := context.Background()
	rd := OpenReader(dir)
	r := openWith(t, dir, o)
	// Every other sample fails: each sample makes an event.
	fn := func(i int) (time.Duration, int, string) { return time.Second, i % 2, "failed run" }
	feed(t, r, clk, t0, 20000, time.Second, fn)
	early, err := rd.Events(ctx, EventQuery{Limit: 1})
	require.NoError(t, err)
	require.NotEmpty(t, early.NextCursor)
	feed(t, r, clk, t0.Add(20000*time.Second), 80000, time.Second, func(i int) (time.Duration, int, string) { return fn(i + 20000) })
	require.NoError(t, r.Close(ctx))
	st, err := os.Stat(filepath.Join(dir, eventsFileName))
	require.NoError(t, err)
	require.Less(t, st.Size(), int64(bound))

	// The cursor points to an event that the compactions dropped.
	_, err = rd.Events(ctx, EventQuery{Before: early.NextCursor})
	require.ErrorIs(t, err, ErrInvalidCursor)

	// The pages cover the kept events with no hole, the newest first.
	var prev time.Time
	n, cur := 0, ""
	for {
		p, err := rd.Events(ctx, EventQuery{Limit: 500, Before: cur})
		require.NoError(t, err)
		for _, e := range p.Events {
			if n > 0 {
				require.Equal(t, prev.Add(-time.Second), e.Time)
			}
			prev = e.Time
			n++
		}
		if p.NextCursor == "" {
			break
		}
		cur = p.NextCursor
	}
	require.Greater(t, n, 20000)
	require.Equal(t, t0.Add(99999*time.Second), mustFirst(t, rd).Time)
}

// After a compaction, a cursor is invalid, or it gives the events just before
// the event that it was taken at.
func TestCursorOverCompaction(t *testing.T) {
	dir := t.TempDir()
	clk := &clock{t: t0}
	o := Options{Now: clk.now, NoLifecycleEvents: true, MaxEventBytes: 3000}
	ctx := context.Background()
	rd := OpenReader(dir)
	r := openWith(t, dir, o)
	type issued struct {
		cursor string
		at     time.Time // the oldest event of the page
	}
	var cursors []issued
	for i := range 400 {
		feed(t, r, clk, t0.Add(time.Duration(i)*time.Second), 1, time.Second, func(int) (time.Duration, int, string) { return time.Second, i % 2, "x" })
		if p, err := rd.Events(ctx, EventQuery{Limit: 3}); err == nil && p.NextCursor != "" {
			cursors = append(cursors, issued{p.NextCursor, p.Events[len(p.Events)-1].Time})
		}
		for _, c := range cursors {
			p, err := rd.Events(ctx, EventQuery{Limit: 3, Before: c.cursor})
			if err != nil {
				require.ErrorIs(t, err, ErrInvalidCursor)
				continue
			}
			for k, e := range p.Events {
				require.Equal(t, c.at.Add(-time.Duration(k+1)*time.Second), e.Time)
			}
		}
		cursors = cursors[max(len(cursors)-40, 0):]
	}
	st, err := os.Stat(filepath.Join(dir, eventsFileName))
	require.NoError(t, err)
	require.Less(t, st.Size(), int64(3000))
}

// An outage that opens after a compaction goes on over a restart.
func TestOutageAfterCompaction(t *testing.T) {
	dir := t.TempDir()
	clk := &clock{t: t0}
	o := Options{Now: clk.now, NoLifecycleEvents: true, WriteEach: true, MaxEventBytes: 3000}
	ctx := context.Background()
	fail := func(int) (time.Duration, int, string) { return time.Second, 1, "x" }
	r := openWith(t, dir, o)
	feed(t, r, clk, t0, 201, time.Second, func(i int) (time.Duration, int, string) { return time.Second, i % 2, "x" })
	require.NoError(t, r.Close(ctx))
	data, err := os.ReadFile(filepath.Join(dir, eventsFileName))
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(string(data), headerPrefix))

	start := t0.Add(10 * time.Minute)
	r = openWith(t, dir, o)
	feed(t, r, clk, start, 3, time.Second, fail)
	require.NoError(t, r.Close(ctx))
	r = openWith(t, dir, o)
	feed(t, r, clk, start.Add(time.Minute), 1, time.Second, fail)
	feed(t, r, clk, start.Add(2*time.Minute), 1, time.Second, okFn)
	require.NoError(t, r.Close(ctx))
	e := mustFirst(t, OpenReader(dir))
	require.Equal(t, EventRecovered, e.Kind)
	require.Equal(t, 2*time.Minute, e.Outage)
	require.EqualValues(t, 4, e.FailedCount)
}

func TestNewestEnd(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "store")
	_, ok, err := NewestEnd(dir)
	require.NoError(t, err)
	require.False(t, ok)
	require.NoError(t, os.MkdirAll(dir, 0o750))
	_, ok, err = NewestEnd(dir)
	require.NoError(t, err)
	require.False(t, ok)

	clk := &clock{t: t0}
	r := openWith(t, dir, runOpts(clk))
	at := t0.Add(90*time.Second + 250*time.Millisecond)
	require.NoError(t, r.Record(context.Background(), Sample{Time: at, Duration: 2 * time.Second}))
	require.NoError(t, r.Close(context.Background()))
	end, ok, err := NewestEnd(dir)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, at.Add(2*time.Second), end)

	// Without the raw tier, the end of the minute bucket of the sample.
	require.NoError(t, os.RemoveAll(filepath.Join(dir, "raw")))
	end, ok, err = NewestEnd(dir)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, t0.Add(2*time.Minute), end)

	// Without the minute tier, the end of the hour bucket.
	require.NoError(t, os.RemoveAll(filepath.Join(dir, "minute")))
	end, ok, err = NewestEnd(dir)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, t0.Add(time.Hour), end)
}

func BenchmarkRecord(b *testing.B) {
	dir := b.TempDir()
	clk := &clock{t: t0}
	r, err := OpenRecorder(context.Background(), dir, Options{Now: clk.now})
	require.NoError(b, err)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ts := t0.Add(time.Duration(i) * time.Second)
		clk.t = ts
		_ = r.Record(context.Background(), Sample{Time: ts, Duration: okDur(i)})
	}
	_ = r.Close(context.Background())
}
