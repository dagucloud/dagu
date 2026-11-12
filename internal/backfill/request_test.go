// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package backfill_test

import (
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/backfill"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequestResolve(t *testing.T) {
	t.Parallel()
	sched := dailyAt(t, "0 2 * * *")
	dag := &ir.DAG{Name: "etl", Schedule: sched.Schedules}

	req, options, err := backfill.Request{From: "2026-10-01", To: "2026-10-03"}.Resolve(dag, sched, 0, at(20, 0))
	require.NoError(t, err)
	assert.Equal(t, at(1, 0), req.From)
	assert.Equal(t, backfill.Options{From: at(1, 0), To: req.To, Rerun: backfill.RerunNone, MaxConcurrency: 1, OnFailure: backfill.OnFailureStop}, options)

	req, options, err = backfill.Request{Slots: []string{"2026-10-02T02:00"}}.Resolve(dag, sched, 0, at(20, 0))
	require.NoError(t, err)
	assert.Equal(t, []time.Time{at(2, 2)}, req.Slots)
	assert.Equal(t, req.Slots, options.Slots)

	for _, tc := range []struct {
		name string
		req  backfill.Request
		dag  *ir.DAG
		want string
	}{
		{"slots with a range", backfill.Request{Slots: []string{"2026-10-02T02:00"}, From: "2026-10-01"}, dag, "cannot be combined"},
		{"no range", backfill.Request{}, dag, "give from"},
		{"bad rerun", backfill.Request{From: "2026-10-01", Rerun: "some"}, dag, "invalid rerun"},
		{"bad on-failure", backfill.Request{From: "2026-10-01", OnFailure: "retry"}, dag, "invalid on-failure"},
		{"off-schedule slot", backfill.Request{Slots: []string{"2026-10-02T03:00"}}, dag, "not a fire time"},
		{"no schedule", backfill.Request{From: "2026-10-01"}, &ir.DAG{Name: "adhoc"}, "no schedule"},
	} {
		_, _, err := tc.req.Resolve(tc.dag, sched, 0, at(20, 0))
		require.ErrorIs(t, err, backfill.ErrInvalidRequest, tc.name)
		assert.ErrorContains(t, err, tc.want, tc.name)
	}

	// A slot named for a profile whose schedules are all another's cannot be
	// checked against a fire time, so it is refused.
	staging := dailyAt(t, "0 2 * * *")
	staging.Schedules[0].Profile = "staging"
	staging.Profile = "prod"
	_, _, err = backfill.Request{Slots: []string{"2026-10-02T03:00"}, Profile: "prod"}.Resolve(&ir.DAG{Name: "etl", Schedule: staging.Schedules}, staging, 0, at(20, 0))
	require.ErrorIs(t, err, backfill.ErrInvalidRequest)
	assert.ErrorContains(t, err, `no recurring schedule for profile "prod"`)
}
