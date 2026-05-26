// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intgharness

import (
	"context"
	"testing"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/stretchr/testify/require"
)

func TestSchedulerProbeHasLoadedScheduleMatchesAnySchedule(t *testing.T) {
	probe := SchedulerProbe{
		entryReader: staticEntryReader{
			dags: []*core.DAG{
				{
					Name: "scheduled",
					Schedule: []core.Schedule{
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
	dags []*core.DAG
}

func (s staticEntryReader) Init(context.Context) error {
	return nil
}

func (s staticEntryReader) Start(context.Context) {}

func (s staticEntryReader) Stop() {}

func (s staticEntryReader) DAGs() []*core.DAG {
	return s.dags
}

func (s staticEntryReader) DAGStore() exec.DAGStore {
	return nil
}
