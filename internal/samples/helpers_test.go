// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package samples

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var t0 = time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

// clock is a clock that the test moves.
type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newRecorder(t *testing.T, dir string, clk *clock) Recorder {
	t.Helper()
	r, err := OpenRecorder(context.Background(), dir, Options{Now: clk.now})
	require.NoError(t, err)
	return r
}

// feed records n samples from start, one each interval. fn gives the duration
// and the exit code of the sample i.
func feed(t *testing.T, r Recorder, clk *clock, start time.Time, n int, interval time.Duration, fn func(i int) (time.Duration, int, string)) {
	t.Helper()
	ctx := context.Background()
	for i := range n {
		ts := start.Add(time.Duration(i) * interval)
		clk.t = ts
		d, exit, msg := fn(i)
		require.NoError(t, r.Record(ctx, Sample{Time: ts, Duration: d, ExitCode: exit, Message: msg}))
	}
}

func okFn(i int) (time.Duration, int, string) {
	return time.Duration(30+i%40) * time.Millisecond, 0, ""
}

// snapshot returns the content of all files under dir.
func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p) //nolint:gosec
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		out[rel] = string(b)
		return nil
	})
	require.NoError(t, err)
	return out
}

func tierSnapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	all := snapshot(t, dir)
	for k := range all {
		if filepath.Dir(k) == "raw" || k == eventsFileName {
			delete(all, k)
		}
	}
	return all
}

func sumAggs(t *testing.T, dir string, tier Tier) (n, f uint64) {
	t.Helper()
	st := newStore(dir)
	for _, fi := range st.list(tier) {
		aggs, err := readAggFile(fi.path)
		require.NoError(t, err)
		for _, a := range aggs {
			n += a.N
			f += a.F
		}
	}
	return n, f
}
