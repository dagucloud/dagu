// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package proc

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProcMetaValidate(t *testing.T) {
	t.Parallel()

	valid := ProcMeta{
		StartedAt:    1,
		Name:         "daily",
		DAGRunID:     "run-1",
		AttemptID:    "attempt-1",
		RootName:     "root",
		RootDAGRunID: "root-run",
	}
	require.NoError(t, valid.Validate())

	tests := []struct {
		name   string
		mutate func(*ProcMeta)
	}{
		{name: "missing name", mutate: func(meta *ProcMeta) { meta.Name = "" }},
		{name: "invalid run ID", mutate: func(meta *ProcMeta) { meta.DAGRunID = "bad/run" }},
		{name: "missing attempt ID", mutate: func(meta *ProcMeta) { meta.AttemptID = "" }},
		{name: "unsafe attempt ID", mutate: func(meta *ProcMeta) { meta.AttemptID = "bad/attempt" }},
		{name: "missing start time", mutate: func(meta *ProcMeta) { meta.StartedAt = 0 }},
		{name: "partial root", mutate: func(meta *ProcMeta) { meta.RootName = "" }},
		{name: "invalid root run ID", mutate: func(meta *ProcMeta) { meta.RootDAGRunID = "bad/root" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			meta := valid
			test.mutate(&meta)
			require.Error(t, meta.Validate())
		})
	}
}
