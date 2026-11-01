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
