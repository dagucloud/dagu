// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"

	"github.com/dagucloud/dagu/v2/internal/cloudreport"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/schedulerstate"
	"github.com/dagucloud/dagu/v2/internal/service/scheduler"
)

// cloudReportServices reads the service DAGs that reports to Dagu Console
// list as services, as GET /service-dags reads them.
type cloudReportServices struct {
	dags    *persis.DAGRepository
	runs    *persis.DAGRunRepository
	history runtime.Manager
	backoff schedulerstate.RestartBackoffStore
	pause   schedulerstate.PauseStore
}

var _ cloudreport.ServiceDAGs = cloudReportServices{}

// List returns the service DAGs by name with their state. What cannot be
// read of the restart backoff or of a pause only leaves out when the next
// restart is.
func (s cloudReportServices) List(ctx context.Context) ([]cloudreport.ServiceDAG, error) {
	var backoff map[string]schedulerstate.RestartBackoff
	if s.backoff != nil {
		backoff, _ = s.backoff.Load(ctx)
	}
	held := false
	if s.pause != nil {
		if pause, err := s.pause.Get(ctx); err == nil && pause.Paused && !pause.WakeHold() {
			held = true
		}
	}
	views, _, err := scheduler.ListServiceDAGs(ctx, scheduler.ServiceDAGSources{
		DAGs: s.dags, Runs: s.runs, History: s.history, Backoff: backoff, Held: held,
	})
	if err != nil {
		return nil, err
	}
	out := make([]cloudreport.ServiceDAG, 0, len(views))
	for _, v := range views {
		dag := cloudreport.ServiceDAG{
			Name: v.Item.Name, RestartPolicy: string(v.Item.RestartPolicy), RestartWait: v.Item.RestartWait,
			State: string(v.State), NextRestartAt: v.NextRestartAt,
		}
		if v.Latest.DAGRunID != "" {
			latest := v.Latest
			dag.Latest = &latest
		}
		out = append(out, dag)
	}
	return out, nil
}

// Names returns the names of the service DAGs.
func (s cloudReportServices) Names(ctx context.Context) ([]string, error) {
	return s.dags.ServiceDAGNames(ctx)
}

// HistoryDir returns the directory of a service DAG's run history.
func (s cloudReportServices) HistoryDir(name string) (string, bool) {
	return s.runs.RunHistoryDir(name)
}
