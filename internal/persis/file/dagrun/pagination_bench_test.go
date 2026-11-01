// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/stretchr/testify/require"
)

// benchDayRuns is the number of runs in the benchmark's one busy day, about
// six hours at one run a second.
const benchDayRuns = 20_000

// seedBusyDay writes benchDayRuns finished runs of one DAG into a single day
// directly, since creating them through the store takes quadratic time.
func seedBusyDay(b *testing.B, baseDir string) time.Time {
	b.Helper()
	day := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	dayDir := filepath.Join(baseDir, "busy", "dag-runs", "2026", "03", "01")
	for i := range benchDayRuns {
		created := day.Add(time.Duration(i) * time.Second)
		id := fmt.Sprintf("run-%05d", i)
		attemptDir := filepath.Join(
			dayDir,
			DAGRunDirPrefix+created.Format(dateTimeFormatUTC)+"_"+id,
			AttemptDirPrefix+created.Format("20060102_150405_000Z")+"_attempt",
		)
		require.NoError(b, os.MkdirAll(attemptDir, 0o750))
		status := ir.DAGRunStatus{
			Name:       "busy",
			DAGRunID:   id,
			AttemptID:  "attempt",
			Status:     ir.Succeeded,
			StartedAt:  created.Format(time.RFC3339),
			FinishedAt: created.Add(time.Second).Format(time.RFC3339),
		}
		if i%100 == 0 {
			status.Status = ir.Failed
		}
		data, err := json.Marshal(status)
		require.NoError(b, err)
		require.NoError(b, os.WriteFile(filepath.Join(attemptDir, JSONLStatusFile), append(data, '\n'), 0o600))
	}
	return day
}

func BenchmarkQueryStatusesFirstPage(b *testing.B) {
	baseDir := b.TempDir()
	day := seedBusyDay(b, baseDir)
	whole := statusListTuning{minDayRuns: math.MaxInt, pagesPerDay: 1, resolveShare: 1}

	newStore := func(tuning statusListTuning) *Store {
		store := NewStore(baseDir, WithHistoryFileCache(fileutil.NewCache[*ir.DAGRunStatus]("bench", 5000, time.Hour)))
		store.listTuning = tuning
		return store
	}
	firstPage := persis.DAGRunStatusQuery{From: persis.NewUTC(day), Limit: 100}
	// Reading the day whole once writes its index.
	_, err := newStore(whole).QueryStatuses(b.Context(), firstPage)
	require.NoError(b, err)

	pageFifty := firstPage
	for range 49 {
		page, err := newStore(whole).QueryStatuses(b.Context(), pageFifty)
		require.NoError(b, err)
		pageFifty.Cursor = page.NextCursor
	}

	cases := []struct {
		name   string
		tuning statusListTuning
		query  persis.DAGRunStatusQuery
	}{
		{name: "Whole", tuning: whole, query: firstPage},
		{name: "Lazy", tuning: defaultStatusListTuning, query: firstPage},
		{name: "LazyPage50", tuning: defaultStatusListTuning, query: pageFifty},
		{name: "WholePage50", tuning: whole, query: pageFifty},
		{name: "LazySelectiveFilter", tuning: defaultStatusListTuning, query: persis.DAGRunStatusQuery{
			From: firstPage.From, Limit: 100, Statuses: []ir.Status{ir.Failed},
		}},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			store := newStore(tc.tuning)
			b.ReportAllocs()
			for b.Loop() {
				page, err := store.QueryStatuses(b.Context(), tc.query)
				require.NoError(b, err)
				require.Len(b, page.Items, 100)
			}
		})
	}
}
