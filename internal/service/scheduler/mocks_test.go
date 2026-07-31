// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler_test

import (
	"context"

	"github.com/dagucloud/dagu/v2/internal/core"
	"github.com/dagucloud/dagu/v2/internal/core/exec"
	"github.com/dagucloud/dagu/v2/internal/service/scheduler"
)

var _ scheduler.EntryReader = (*mockJobManager)(nil)

type mockJobManager struct {
	LoadedDAGs []*core.DAG
}

func newMockJobManager() *mockJobManager {
	return &mockJobManager{}
}

func (er *mockJobManager) Init(_ context.Context) error {
	return nil
}

func (er *mockJobManager) Start(_ context.Context) {
}

func (er *mockJobManager) Stop() {
}

func (er *mockJobManager) DAGs() []*core.DAG {
	return er.LoadedDAGs
}

func (*mockJobManager) DAGStore() exec.DAGStore {
	return nil
}
