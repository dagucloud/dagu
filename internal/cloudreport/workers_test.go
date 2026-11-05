// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cloudreport

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/license"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Workers go only to paid licenses, and only while a registered coordinator
// keeps their heartbeats current: the count at health, the list from runs.
func TestReportCarriesWorkers(t *testing.T) {
	t.Parallel()

	sentAt := testClockStart.Add(maxStartDelay / 2)
	cpu := 40.0
	records := []dispatch.WorkerHeartbeatRecord{
		{
			WorkerID: "w-b", Host: "gpu-1", Labels: map[string]string{"gpu": "true"},
			Stats:           &dispatch.WorkerStats{TotalPollers: 4, BusyPollers: 1},
			LastHeartbeatAt: sentAt.Add(-5 * time.Second).UnixMilli(),
			Machine: &dispatch.MachineSample{CPUs: 8, CPUPercent: &cpu,
				Memory: &dispatch.MachineUsage{UsedPercent: 25, UsedBytes: 1, TotalBytes: 4}},
		},
		{WorkerID: "w-a", LastHeartbeatAt: sentAt.Add(-time.Second).UnixMilli()},
		// A stale record the coordinator has yet to remove.
		{WorkerID: "w-gone", LastHeartbeatAt: sentAt.Add(-time.Minute).UnixMilli()},
	}
	coordinator := &fakeRegistry{members: map[serviceregistry.ServiceName][]serviceregistry.HostInfo{
		serviceregistry.ServiceNameCoordinator: {{ID: "c1"}},
	}}
	list := `[
		{"id": "w-a", "total_pollers": 0, "busy_pollers": 0, "last_heartbeat_at": "2026-10-10T09:30:29Z"},
		{"id": "w-b", "host": "gpu-1", "labels": {"gpu": "true"}, "total_pollers": 4, "busy_pollers": 1,
			"last_heartbeat_at": "2026-10-10T09:30:25Z",
			"resources": {"cpus": 8, "cpu_percent": 40,
				"memory": {"used_percent": 25, "used_bytes": 1, "total_bytes": 4}}}
	]`

	for _, tc := range []struct {
		name     string
		level    config.ReportLevel
		registry serviceregistry.ServiceRegistry
		workers  *stubWorkers
		free     bool
		// wantList and wantTotal are the JSON sent, empty when left out.
		wantList  string
		wantTotal string
	}{
		{"Runs", config.ReportRuns, coordinator, &stubWorkers{records: records}, false, list, "2"},
		{"Health", config.ReportHealth, coordinator, &stubWorkers{records: records}, false, "", "2"},
		{"NoWorkers", config.ReportRuns, coordinator, &stubWorkers{}, false, "[]", "0"},
		{"FreeMonitoring", config.ReportRuns, coordinator, &stubWorkers{records: records}, true, "", ""},
		{"NoCoordinator", config.ReportRuns, &fakeRegistry{}, &stubWorkers{records: records}, false, "", ""},
		{"Unreadable", config.ReportRuns, coordinator, &stubWorkers{err: errors.New("unavailable")}, false, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			console := newFakeConsole(t)
			credentials := func() (license.CloudCredentials, bool) {
				creds, ok := console.credentials()
				creds.Free = tc.free
				return creds, ok
			}
			r := newReporter(credentials, tc.registry, fixedLevel(tc.level))
			r.workers = tc.workers
			clock := startReporter(t.Context(), t, r, noJitter)

			clock.wait()
			clock.elapse()
			var body struct {
				Health map[string]json.RawMessage `json:"health"`
			}
			require.NoError(t, json.Unmarshal([]byte(console.next(t).body), &body))
			assertField(t, body.Health, "workers", tc.wantList)
			assertField(t, body.Health, "workers_total", tc.wantTotal)
		})
	}
}

// assertField checks that fields holds want under key, or no key when want is
// empty.
func assertField(t *testing.T, fields map[string]json.RawMessage, key, want string) {
	t.Helper()
	got, ok := fields[key]
	if want == "" {
		assert.False(t, ok, "%s sent: %s", key, got)
		return
	}
	require.True(t, ok, "%s left out", key)
	assert.JSONEq(t, want, string(got))
}

type stubWorkers struct {
	records []dispatch.WorkerHeartbeatRecord
	err     error
}

// List returns a copy, as a store does, since the reporter keeps only the
// fresh records in the slice it gets.
func (s *stubWorkers) List(context.Context) ([]dispatch.WorkerHeartbeatRecord, error) {
	return slices.Clone(s.records), s.err
}
