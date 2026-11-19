// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package backfill_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/backfill"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A DAG's runs tell their slots apart only when its definition reads the
// slot, its window, or its dates, or hands the slot to a sub-DAG.
func TestReadsSlot(t *testing.T) {
	t.Parallel()

	const schedule = "schedule: \"0 * * * *\"\n"
	for _, tc := range []struct {
		name string
		yaml string
		base string
		want bool
	}{
		{name: "no slot", yaml: "steps:\n  - run: echo hi\n"},
		{name: "timezone only", yaml: "steps:\n  - run: echo ${context.run.timezone}\n"},
		{name: "scheduled_at", yaml: "steps:\n  - run: echo ${context.run.scheduled_at}\n", want: true},
		{name: "window", yaml: "steps:\n  - run: echo $context.run.interval_start\n", want: true},
		{name: "legacy scheduled_at", yaml: "steps:\n  - run: echo ${run.scheduled_at}\n", want: true},
		{name: "step env", yaml: "steps:\n  - run: ./load.sh\n    env:\n      - UNTIL: ${context.run.interval_end}\n", want: true},
		{
			name: "dates",
			yaml: "dates:\n  target_day: -1d\nsteps:\n  - run: echo hi\n",
			want: true,
		},
		{name: "dates param", yaml: "dates:\n  target_day: -1d\nparams:\n  - name: day\n    eval: ${dates.target_day}\nsteps:\n  - run: echo hi\n", want: true},
		{name: "base config", yaml: "steps:\n  - run: ./load.sh\n", base: "env:\n  - SLOT: ${context.run.scheduled_at}\n", want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var opts []spec.LoadOption
			if tc.base != "" {
				opts = append(opts, spec.WithBaseConfigContent([]byte(tc.base)))
			}
			dag, err := spec.LoadYAML(t.Context(), []byte("name: etl\n"+schedule+tc.yaml), opts...)
			require.NoError(t, err)
			assert.Equal(t, tc.want, backfill.ReadsSlot(dag))
		})
	}

	// A sub-DAG inherits the slot, and may read it.
	t.Run("sub-DAG", func(t *testing.T) {
		t.Parallel()
		dag := &ir.DAG{Name: "etl", Steps: []ir.Step{{Name: "child", SubDAG: &ir.SubDAG{Name: "child"}}}}
		assert.True(t, backfill.ReadsSlot(dag))
	})
}
