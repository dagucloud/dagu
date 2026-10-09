// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cloudreport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/license"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testTimeout = 5 * time.Second

var testStartedAt = time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)

func TestReportPayload(t *testing.T) {
	t.Parallel()

	console := newFakeConsole(t)
	registry := &fakeRegistry{members: map[serviceregistry.ServiceName][]serviceregistry.HostInfo{
		serviceregistry.ServiceNameScheduler: {
			{ID: "s1", Status: serviceregistry.ServiceStatusActive},
			{ID: "s2", Status: serviceregistry.ServiceStatusInactive},
		},
		serviceregistry.ServiceNameCoordinator: {{ID: "c1"}, {ID: "c2"}},
	}}
	clock := startReporter(t.Context(), t, newReporter(console.credentials, registry), noJitter)

	assert.Equal(t, maxStartDelay/2, clock.wait())
	clock.elapse()
	report := console.next(t)

	assert.Equal(t, http.MethodPost, report.method)
	assert.Equal(t, "/api/v1/servers/report", report.path)
	assert.Equal(t, "application/json", report.header.Get("Content-Type"))
	assert.Equal(t, "dagu-oss/"+config.Version, report.header.Get("User-Agent"))
	assert.JSONEq(t, fmt.Sprintf(`{
		"protocol": 1,
		"license_id": "lic-1",
		"server_id": "srv-1",
		"heartbeat_secret": "hb-secret",
		"health": {
			"version": %q,
			"os": %q,
			"arch": %q,
			"started_at": "2026-10-09T12:00:00Z",
			"services": [
				{"name": "scheduler", "instances": 1},
				{"name": "coordinator", "instances": 2}
			]
		}
	}`, config.Version, runtime.GOOS, runtime.GOARCH), report.body)
}

func TestReportWithoutServices(t *testing.T) {
	t.Parallel()

	registries := map[string]serviceregistry.ServiceRegistry{
		"no registry":         nil,
		"unreadable registry": &fakeRegistry{err: errors.New("registry unavailable")},
	}
	for name, registry := range registries {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			console := newFakeConsole(t)
			clock := startReporter(t.Context(), t, newReporter(console.credentials, registry), noJitter)

			clock.wait()
			clock.elapse()
			var body struct {
				Health map[string]any `json:"health"`
			}
			require.NoError(t, json.Unmarshal([]byte(console.next(t).body), &body))

			assert.Equal(t, config.Version, body.Health["version"])
			assert.NotContains(t, body.Health, "services")
		})
	}
}

func TestReportNextInterval(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want time.Duration
	}{
		{name: "requested", body: `{"next_report_seconds": 120}`, want: 2 * time.Minute},
		{name: "below minimum", body: `{"next_report_seconds": 5}`, want: 30 * time.Second},
		{name: "above maximum", body: `{"next_report_seconds": 7200}`, want: time.Hour},
		{name: "absent", body: `{}`, want: time.Minute},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			console := newFakeConsole(t, consoleResponse{status: http.StatusOK, body: tc.body})
			clock := startReporter(t.Context(), t, newReporter(console.credentials, nil), noJitter)

			clock.wait()
			assert.Equal(t, tc.want, clock.next())
		})
	}
}

func TestReportJitter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		random    float64
		wantFirst time.Duration
		wantNext  time.Duration
	}{
		{name: "low", random: 0, wantFirst: 0, wantNext: 54 * time.Second},
		{name: "high", random: 0.75, wantFirst: 45 * time.Second, wantNext: 63 * time.Second},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			console := newFakeConsole(t)
			clock := startReporter(t.Context(), t, newReporter(console.credentials, nil),
				func() float64 { return tc.random })

			assert.Equal(t, tc.wantFirst, clock.wait())
			assert.Equal(t, tc.wantNext, clock.next())
		})
	}
}

func TestReportWithoutCredentials(t *testing.T) {
	t.Parallel()

	console := newFakeConsole(t)
	var activated atomic.Bool
	credentials := func() (license.CloudCredentials, bool) {
		if !activated.Load() {
			return license.CloudCredentials{}, false
		}
		return console.credentials()
	}
	clock := startReporter(t.Context(), t, newReporter(credentials, nil), noJitter)

	clock.wait()
	assert.Equal(t, time.Minute, clock.next())
	assert.Empty(t, console.reports, "nothing is sent without credentials")

	activated.Store(true)
	clock.elapse()
	assert.Equal(t, "srv-1", decodeReport(t, console.next(t)).ServerID)
}

func TestReportTooManyRequests(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		retryAfter string
		want       time.Duration
	}{
		{name: "retry after", retryAfter: "7", want: 7 * time.Second},
		{name: "missing", retryAfter: "", want: time.Minute},
		{name: "invalid", retryAfter: "soon", want: time.Minute},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			console := newFakeConsole(t, consoleResponse{status: http.StatusTooManyRequests, retryAfter: tc.retryAfter})
			clock := startReporter(t.Context(), t, newReporter(console.credentials, nil), noJitter)

			clock.wait()
			assert.Equal(t, tc.want, clock.next())
		})
	}
}

func TestReportServerErrorsBackOff(t *testing.T) {
	t.Parallel()

	t.Run("console errors", func(t *testing.T) {
		t.Parallel()

		failure := consoleResponse{status: http.StatusServiceUnavailable, body: `{"error":"unavailable","message":"try later"}`}
		console := newFakeConsole(t,
			failure, failure, failure, failure, failure,
			consoleResponse{status: http.StatusOK, body: `{"next_report_seconds": 120}`},
			failure,
		)
		clock := startReporter(t.Context(), t, newReporter(console.credentials, nil), noJitter)

		clock.wait()
		var waits []time.Duration
		for range 7 {
			waits = append(waits, clock.next())
		}

		// A success resets the backoff, so the last failure waits a minute.
		assert.Equal(t, []time.Duration{
			time.Minute, 2 * time.Minute, 4 * time.Minute, 5 * time.Minute, 5 * time.Minute,
			2 * time.Minute,
			time.Minute,
		}, waits)
	})

	t.Run("any answer from the console ends the failures", func(t *testing.T) {
		t.Parallel()

		failure := consoleResponse{status: http.StatusServiceUnavailable}
		console := newFakeConsole(t,
			failure, failure, failure,
			consoleResponse{status: http.StatusUnauthorized, body: `{"message":"unauthorized"}`},
			failure,
			consoleResponse{status: http.StatusTooManyRequests, retryAfter: "7"},
			failure,
		)
		clock := startReporter(t.Context(), t, newReporter(console.credentials, nil), noJitter)

		clock.wait()
		var waits []time.Duration
		for range 7 {
			waits = append(waits, clock.next())
		}

		assert.Equal(t, []time.Duration{
			time.Minute, 2 * time.Minute, 4 * time.Minute,
			time.Minute,
			time.Minute,
			7 * time.Second,
			time.Minute,
		}, waits)
	})

	t.Run("unreachable console", func(t *testing.T) {
		t.Parallel()

		credentials := func() (license.CloudCredentials, bool) {
			return license.CloudCredentials{CloudURL: "http://127.0.0.1:0", LicenseID: "lic-1", ServerID: "srv-1"}, true
		}
		clock := startReporter(t.Context(), t, newReporter(credentials, nil), noJitter)

		clock.wait()
		assert.Equal(t, time.Minute, clock.next())
		assert.Equal(t, 2*time.Minute, clock.next())
	})
}

// Rejections are left to the license manager's heartbeat, so the reporter
// keeps its normal schedule and logs only when the rejection changes.
func TestReportRejected(t *testing.T) {
	t.Parallel()

	rejection := func(status int) consoleResponse {
		return consoleResponse{status: status, body: `{"error":"rejected","message":"activation not found"}`}
	}
	console := newFakeConsole(t,
		rejection(http.StatusUnauthorized), rejection(http.StatusUnauthorized), rejection(http.StatusUnauthorized),
		rejection(http.StatusGone), rejection(http.StatusGone),
		rejection(http.StatusBadRequest),
	)
	var logs bytes.Buffer
	ctx := logger.WithLogger(t.Context(), logger.NewLogger(
		logger.WithFormat("text"),
		logger.WithWriter(&logs),
		logger.WithQuiet(),
	))
	clock := startReporter(ctx, t, newReporter(console.credentials, nil), noJitter)

	clock.wait()
	for range 6 {
		assert.Equal(t, time.Minute, clock.next())
	}

	assert.Equal(t, 3, strings.Count(logs.String(), "Dagu Console rejected the server report"))
	assert.Contains(t, logs.String(), "activation not found")
}

// noJitter makes every wait exact and the first report wait half of
// maxStartDelay.
func noJitter() float64 { return 0.5 }

// startReporter runs r with fixed randomness and a clock the test controls.
func startReporter(ctx context.Context, t *testing.T, r *Reporter, random func() float64) *fakeClock {
	t.Helper()
	clock := &fakeClock{t: t, waits: make(chan time.Duration, 16), fire: make(chan time.Time)}
	r.after = clock.after
	r.random = random
	r.startedAt = testStartedAt
	r.start(ctx)
	t.Cleanup(r.Stop)
	return clock
}

// fakeClock hands each wait the reporter asks for to the test, and ends it
// only when the test says so.
type fakeClock struct {
	t     *testing.T
	waits chan time.Duration
	fire  chan time.Time
}

func (c *fakeClock) after(d time.Duration) <-chan time.Time {
	c.waits <- d
	return c.fire
}

// wait returns the reporter's current wait.
func (c *fakeClock) wait() time.Duration {
	c.t.Helper()
	select {
	case d := <-c.waits:
		return d
	case <-time.After(testTimeout):
		c.t.Fatal("reporter did not wait")
		return 0
	}
}

// elapse ends the reporter's current wait.
func (c *fakeClock) elapse() {
	c.t.Helper()
	select {
	case c.fire <- time.Time{}:
	case <-time.After(testTimeout):
		c.t.Fatal("reporter is not waiting")
	}
}

// next ends the current wait and returns the wait that follows the report.
func (c *fakeClock) next() time.Duration {
	c.t.Helper()
	c.elapse()
	return c.wait()
}

type consoleResponse struct {
	status     int
	retryAfter string
	body       string
}

type receivedReport struct {
	method string
	path   string
	header http.Header
	body   string
}

// fakeConsole stands in for Dagu Console. It answers reports with the queued
// responses in order, then with 200 and an empty object.
type fakeConsole struct {
	url     string
	reports chan receivedReport

	mu        sync.Mutex
	responses []consoleResponse
}

func newFakeConsole(t *testing.T, responses ...consoleResponse) *fakeConsole {
	t.Helper()
	c := &fakeConsole{reports: make(chan receivedReport, 16), responses: responses}
	srv := httptest.NewServer(http.HandlerFunc(c.serve))
	t.Cleanup(srv.Close)
	c.url = srv.URL
	return c
}

func (c *fakeConsole) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	c.reports <- receivedReport{method: r.Method, path: r.URL.Path, header: r.Header.Clone(), body: string(body)}

	resp := consoleResponse{status: http.StatusOK, body: `{}`}
	c.mu.Lock()
	if len(c.responses) > 0 {
		resp, c.responses = c.responses[0], c.responses[1:]
	}
	c.mu.Unlock()

	if resp.retryAfter != "" {
		w.Header().Set("Retry-After", resp.retryAfter)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.status)
	_, _ = io.WriteString(w, resp.body)
}

func (c *fakeConsole) credentials() (license.CloudCredentials, bool) {
	return license.CloudCredentials{
		CloudURL:        c.url,
		LicenseID:       "lic-1",
		ServerID:        "srv-1",
		HeartbeatSecret: "hb-secret",
	}, true
}

// next returns the next report the console received.
func (c *fakeConsole) next(t *testing.T) receivedReport {
	t.Helper()
	select {
	case report := <-c.reports:
		return report
	case <-time.After(testTimeout):
		t.Fatal("console received no report")
		return receivedReport{}
	}
}

func decodeReport(t *testing.T, report receivedReport) reportRequest {
	t.Helper()
	var req reportRequest
	require.NoError(t, json.Unmarshal([]byte(report.body), &req))
	return req
}

type fakeRegistry struct {
	members map[serviceregistry.ServiceName][]serviceregistry.HostInfo
	err     error
}

func (r *fakeRegistry) Register(context.Context, serviceregistry.ServiceName, serviceregistry.HostInfo) error {
	return nil
}

func (r *fakeRegistry) Unregister(context.Context) {}

func (r *fakeRegistry) GetServiceMembers(_ context.Context, name serviceregistry.ServiceName) ([]serviceregistry.HostInfo, error) {
	return r.members[name], r.err
}

func (r *fakeRegistry) UpdateStatus(context.Context, serviceregistry.ServiceName, serviceregistry.ServiceStatus) error {
	return nil
}
