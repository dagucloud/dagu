// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package persis_test

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file/slotindex"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/dagucloud/dagu/v2/internal/testutil"
	"github.com/stretchr/testify/require"
)

// BenchmarkSlotEventsFirstRead measures the first read of a DAG's slot
// history, which adds the whole run history to the slot index, for a DAG
// that ran once a minute for the given number of days.
func BenchmarkSlotEventsFirstRead(b *testing.B) {
	for _, days := range []int{3, 10, 30} {
		b.Run(fmt.Sprintf("days=%d", days), func(b *testing.B) {
			ctx := b.Context()
			dir := b.TempDir()
			runsDir := filepath.Join(dir, "dag-runs")
			dag, err := spec.LoadYAML(ctx, []byte(minutelyDAGYAML))
			require.NoError(b, err)
			end := time.Now().UTC().Truncate(24 * time.Hour)
			start := end.AddDate(0, 0, -days)
			runs := seedMinutelyRuns(b, runsDir, dag, start, end)

			repository := func(index persis.SlotIndex) *persis.DAGRunRepository {
				return testutil.NewFileDAGRunRepository(runsDir, persis.DAGRunRepositoryOptions{SlotIndex: index, Location: time.UTC})
			}
			// Earlier listings of the history have written each day's run index.
			_, err = repository(nil).ListStatuses(ctx, persis.DAGRunListOptions{ExactName: dag.Name, AllHistory: true, Unbounded: true})
			require.NoError(b, err)

			reads := 0
			b.ReportAllocs()
			for b.Loop() {
				reads++
				index := slotindex.New(filepath.Join(dir, fmt.Sprintf("slot-index-%d", reads)))
				events, err := repository(index).SlotEvents(ctx, dag, start, end)
				require.NoError(b, err)
				require.Len(b, events, runs)
			}
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(reads*runs), "ns/run")
		})
	}
}
