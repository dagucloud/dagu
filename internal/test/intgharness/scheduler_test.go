// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intgharness

import (
	"context"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/stretchr/testify/require"
)

func TestSchedulerProbeHasLoadedScheduleMatchesAnySchedule(t *testing.T) {
	probe := SchedulerProbe{
		entryReader: staticEntryReader{
			dags: []*ir.DAG{
				{
					Name: "scheduled",
					Schedule: []ir.Schedule{
						{Expression: "0 10 * * *"},
						{Expression: "5 10 * * *"},
					},
				},
			},
		},
	}

	require.True(t, probe.HasLoadedSchedule("scheduled", "5 10 * * *"))
	require.False(t, probe.HasLoadedSchedule("scheduled", "15 10 * * *"))
	require.False(t, probe.HasLoadedSchedule("other", "5 10 * * *"))
}

type staticEntryReader struct {
	dags []*ir.DAG
}

func (s staticEntryReader) Init(context.Context) error {
	return nil
}

func (s staticEntryReader) Start(context.Context) {}

func (s staticEntryReader) Stop() {}

func (s staticEntryReader) DAGs() []*ir.DAG {
	return s.dags
}

func (s staticEntryReader) DAGRepository() *persis.DAGRepository {
	return nil
}
