// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package wait

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	cmnconfig "github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/stretchr/testify/require"
)

func TestExecutorRunWaitsForDuration(t *testing.T) {
	t.Parallel()

	exec, err := newExecutor(context.Background(), ir.Step{
		Commands: []ir.CommandEntry{{Command: opDuration}},
		ExecutorConfig: ir.ExecutorConfig{
			Type: "wait",
			Config: map[string]any{
				"duration": "5ms",
			},
		},
	})
	require.NoError(t, err)

	start := time.Now()
	require.NoError(t, exec.Run(context.Background()))
	require.GreaterOrEqual(t, time.Since(start), 5*time.Millisecond)
}

func TestExecutorRunWaitsUntilFileExists(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	target := filepath.Join(dir, "ready.flag")
	exec, err := newExecutor(context.Background(), ir.Step{
		Commands: []ir.CommandEntry{{Command: opFile}},
		ExecutorConfig: ir.ExecutorConfig{
			Type: "wait",
			Config: map[string]any{
				"path":          target,
				"poll_interval": "1ms",
			},
		},
	})
	require.NoError(t, err)

	errCh := make(chan error, 1)
	go func() {
		time.Sleep(5 * time.Millisecond)
		errCh <- os.WriteFile(target, []byte("ready"), 0o600)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	require.NoError(t, exec.Run(ctx))
	require.NoError(t, <-errCh)
}

func TestExecutorRunWaitsForHTTPStatus(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	exec, err := newExecutor(context.Background(), ir.Step{
		Commands: []ir.CommandEntry{{Command: opHTTP}},
		ExecutorConfig: ir.ExecutorConfig{
			Type: "wait",
			Config: map[string]any{
				"url":           server.URL,
				"status":        http.StatusNoContent,
				"poll_interval": "1ms",
			},
		},
	})
	require.NoError(t, err)

	require.NoError(t, exec.Run(context.Background()))
	require.GreaterOrEqual(t, calls.Load(), int32(3))
}

func TestValidateStepDefersReferenceFields(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name   string
		op     string
		config map[string]any
	}{
		{name: "duration", op: opDuration, config: map[string]any{"duration": "${params.delay}"}},
		{name: "until", op: opUntil, config: map[string]any{"until": "${steps.plan.outputs.deadline}"}},
		{name: "state", op: opFile, config: map[string]any{"path": "ready.flag", "state": "${params.state}"}},
		{
			name:   "http",
			op:     opHTTP,
			config: map[string]any{"url": "${steps.plan.outputs.url}", "status": "${steps.plan.outputs.code}"},
		},
		{name: "pollInterval", op: opDuration, config: map[string]any{"duration": "5ms", "poll_interval": "${params.interval}"}},
		{name: "braceEnvDuration", op: opDuration, config: map[string]any{"duration": "${DELAY}"}},
		{name: "bareEnvURL", op: opHTTP, config: map[string]any{"url": "$SERVICE_URL", "status": "${STATUS_CODE}"}},
		{name: "embeddedBraceEnvURL", op: opHTTP, config: map[string]any{"url": "${BASE_URL}/health"}},
		{name: "embeddedBareEnvURL", op: opHTTP, config: map[string]any{"url": "$BASE_URL/health"}},
		{name: "embeddedEnvDuration", op: opDuration, config: map[string]any{"duration": "${SECS}s"}},
		{
			name: "untilOptions",
			op:   opUntil,
			config: map[string]any{
				"until":    "${params.release_at}",
				"offset":   "${params.shift}",
				"timezone": "${params.zone}",
				"if_past":  "${params.on_past}",
				"window":   "${params.grace}",
			},
		},
		{name: "bareEnvOffset", op: opUntil, config: map[string]any{"until": "09:00", "offset": "$SHIFT"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.NoError(t, validateStep(ir.Step{
				Commands:       []ir.CommandEntry{{Command: tt.op}},
				ExecutorConfig: ir.ExecutorConfig{Type: "wait", Config: tt.config},
			}))
		})
	}
}

func TestValidateStepStillRejectsLiteralValues(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name   string
		op     string
		config map[string]any
		want   string
	}{
		{name: "duration", op: opDuration, config: map[string]any{"duration": "bogus"}, want: "duration must be a duration"},
		{name: "until", op: opUntil, config: map[string]any{"until": "tomorrow"}, want: "until must be RFC3339 timestamp"},
		{name: "state", op: opFile, config: map[string]any{"path": "ready.flag", "state": "soon"}, want: `state must be "exists" or "missing"`},
		{name: "url", op: opHTTP, config: map[string]any{"url": "ftp://example.com/ready"}, want: "url must be an absolute HTTP URL"},
		{name: "status", op: opHTTP, config: map[string]any{"url": "http://example.com/", "status": 99}, want: "status must be between 100 and 599"},
		{name: "clock", op: opUntil, config: map[string]any{"until": "25:00"}, want: "until must be RFC3339 timestamp or HH:MM"},
		{name: "timezone", op: opUntil, config: map[string]any{"until": "09:00", "timezone": "Mars/Olympus"}, want: "timezone must be an IANA name"},
		{name: "offset", op: opUntil, config: map[string]any{"until": "09:00", "offset": "bogus"}, want: "offset must be a duration"},
		{name: "ifPast", op: opUntil, config: map[string]any{"until": "09:00", "if_past": "retry"}, want: "if_past must be"},
		{name: "ifPastCase", op: opUntil, config: map[string]any{"until": "09:00", "if_past": "Fail"}, want: "if_past must be"},
		{name: "windowMissing", op: opUntil, config: map[string]any{"until": "09:00", "if_past": "window"}, want: "window is required"},
		{name: "windowWithoutIfPast", op: opUntil, config: map[string]any{"until": "09:00", "window": "5m"}, want: "window requires if_past"},
		{
			name:   "deferredWindowWithLiteralIfPast",
			op:     opUntil,
			config: map[string]any{"until": "09:00", "if_past": "fail", "window": "${params.grace}"},
			want:   "window requires if_past",
		},
		{name: "windowBad", op: opUntil, config: map[string]any{"until": "09:00", "if_past": "window", "window": "bogus"}, want: "window must be a duration"},
		{name: "offsetOnDuration", op: opDuration, config: map[string]any{"duration": "5s", "offset": "1m"}, want: "offset is only valid for until"},
		{
			name: "untilOptionsReportedInOrder",
			op:   opDuration,
			config: map[string]any{
				"duration": "5s",
				"offset":   "1m",
				"timezone": "UTC",
				"if_past":  "fail",
				"window":   "1m",
			},
			want: "offset is only valid for until",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateStep(ir.Step{
				Commands:       []ir.CommandEntry{{Command: tt.op}},
				ExecutorConfig: ir.ExecutorConfig{Type: "wait", Config: tt.config},
			})
			require.ErrorContains(t, err, tt.want)
		})
	}
}

func TestParseStatus(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name          string
		raw           any
		want          int
		wantErrSubstr string
	}{
		{name: "unset", raw: nil, want: http.StatusOK},
		{name: "integer", raw: http.StatusNoContent, want: http.StatusNoContent},
		{name: "yamlInteger", raw: uint64(http.StatusNoContent), want: http.StatusNoContent},
		{name: "int32", raw: int32(http.StatusNoContent), want: http.StatusNoContent},
		{name: "jsonNumber", raw: json.Number("204"), want: http.StatusNoContent},
		{name: "wholeFloat", raw: float64(http.StatusNoContent), want: http.StatusNoContent},
		{name: "negativeInteger", raw: int64(-1), wantErrSubstr: "status must be between 100 and 599"},
		{name: "overflowingUint", raw: uint64(1<<32 + http.StatusOK), wantErrSubstr: "status must be between 100 and 599"},
		{name: "fractional", raw: 204.5, wantErrSubstr: "status must be between 100 and 599"},
		{name: "numericString", raw: "204", want: http.StatusNoContent},
		{name: "badString", raw: "soon", wantErrSubstr: "status must be between 100 and 599"},
		{name: "outOfRange", raw: 99, wantErrSubstr: "status must be between 100 and 599"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseStatus(tt.raw)
			if tt.wantErrSubstr != "" {
				require.ErrorContains(t, err, tt.wantErrSubstr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestClockDeadlineAppliesOffset(t *testing.T) {
	t.Parallel()

	loc := time.UTC
	base := time.Date(2026, 1, 2, 0, 0, 0, 0, loc)
	for _, tt := range []struct {
		name   string
		clock  clockTime
		offset time.Duration
		want   time.Time
	}{
		{name: "positive", clock: clockTime{hour: 9}, offset: 10 * time.Minute, want: time.Date(2026, 1, 2, 9, 10, 0, 0, loc)},
		{name: "negative", clock: clockTime{hour: 9}, offset: -3 * time.Hour, want: time.Date(2026, 1, 2, 6, 0, 0, 0, loc)},
		{name: "crossesMidnight", clock: clockTime{minute: 30}, offset: -time.Hour, want: time.Date(2026, 1, 1, 23, 30, 0, 0, loc)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, clockDeadline(base, tt.clock, loc, tt.offset))
		})
	}
}

// Clock times that a daylight-saving transition skips or repeats resolve as in
// RFC 5545: a skipped time is read with the UTC offset before the gap, and a
// repeated time resolves to its first occurrence.
func TestClockDeadlineAcrossDST(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name  string
		zone  string
		day   time.Time
		clock clockTime
		want  time.Time
	}{
		// 02:30 EST does not exist; it reads as 03:30 EDT.
		{name: "gap", zone: "America/New_York", day: time.Date(2026, 3, 8, 0, 0, 0, 0, time.UTC), clock: clockTime{hour: 2, minute: 30}, want: time.Date(2026, 3, 8, 7, 30, 0, 0, time.UTC)},
		// The gap starts at midnight; 00:30 reads as 01:30 CDT on the same day.
		{name: "gapAtMidnight", zone: "America/Havana", day: time.Date(2026, 3, 8, 0, 0, 0, 0, time.UTC), clock: clockTime{minute: 30}, want: time.Date(2026, 3, 8, 5, 30, 0, 0, time.UTC)},
		// 02:30 occurs in AEDT and then in AEST; the AEDT one comes first.
		{name: "overlap", zone: "Australia/Sydney", day: time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC), clock: clockTime{hour: 2, minute: 30}, want: time.Date(2026, 4, 4, 15, 30, 0, 0, time.UTC)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			loc, err := time.LoadLocation(tt.zone)
			require.NoError(t, err)
			base := time.Date(tt.day.Year(), tt.day.Month(), tt.day.Day(), 12, 0, 0, 0, loc)
			require.Equal(t, tt.want, clockDeadline(base, tt.clock, loc, 0).UTC())
		})
	}
}

// A clock time that already passed on the logical day follows if_past rather
// than rolling over to the next day. 00:00 minus 1s on today's logical day is
// always past, while the same time a day later is always ahead.
func TestExecutorIfPastAppliesToClockTime(t *testing.T) {
	t.Parallel()

	ctx := runtime.NewContext(context.Background(), &ir.DAG{Name: "test"}, "run-1", "",
		runtime.WithScheduleTime(time.Now().UTC().Format(time.RFC3339)))

	for _, tt := range []struct {
		name    string
		config  map[string]any
		wantErr string
	}{
		{name: "continue", config: map[string]any{"until": "00:00", "offset": "-1s"}},
		{name: "fail", config: map[string]any{"until": "00:00", "offset": "-1s", "if_past": "fail"}, wantErr: "in the past"},
		{name: "window", config: map[string]any{"until": "00:00", "offset": "-1s", "if_past": "window", "window": "25h"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			exec, err := newExecutor(ctx, ir.Step{
				Commands:       []ir.CommandEntry{{Command: opUntil}},
				ExecutorConfig: ir.ExecutorConfig{Type: "wait", Config: tt.config},
			})
			require.NoError(t, err)

			runCtx, cancel := context.WithTimeout(ctx, time.Second)
			defer cancel()
			err = exec.Run(runCtx)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}

// A clock time resolves on the logical day in with.timezone, else in the
// run-configured zone. The deadline is past, so if_past: fail reports it.
func TestExecutorResolvesClockTimeZone(t *testing.T) {
	t.Parallel()

	newYork, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)
	ctx := cmnconfig.WithConfig(context.Background(), &cmnconfig.Config{Core: cmnconfig.Core{Location: newYork}})
	// 20:00 UTC is still January 2 in New York but already January 3 in Tokyo.
	ctx = runtime.NewContext(ctx, &ir.DAG{Name: "test"}, "run-1", "",
		runtime.WithScheduleTime("2026-01-02T20:00:00Z"))

	for _, tt := range []struct {
		name     string
		timezone string
		want     string
	}{
		{name: "configuredZone", want: "2026-01-02T09:00:00-05:00"},
		{name: "stepTimezone", timezone: "Asia/Tokyo", want: "2026-01-03T09:00:00+09:00"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			config := map[string]any{"until": "09:00", "if_past": "fail"}
			if tt.timezone != "" {
				config["timezone"] = tt.timezone
			}
			exec, err := newExecutor(ctx, ir.Step{
				Commands:       []ir.CommandEntry{{Command: opUntil}},
				ExecutorConfig: ir.ExecutorConfig{Type: "wait", Config: config},
			})
			require.NoError(t, err)
			require.ErrorContains(t, exec.Run(ctx), tt.want)
		})
	}
}

func TestExecutorRFC3339OffsetAppliedOnce(t *testing.T) {
	t.Parallel()

	// Deadline lands 1m in the past after a single +2m shift; a doubled
	// offset would land 1m in the future and block instead of succeeding.
	until := time.Now().Add(-3 * time.Minute).UTC().Format(time.RFC3339Nano)
	exec, err := newExecutor(context.Background(), ir.Step{
		Commands: []ir.CommandEntry{{Command: opUntil}},
		ExecutorConfig: ir.ExecutorConfig{
			Type: "wait",
			Config: map[string]any{
				"until":   until,
				"offset":  "2m",
				"if_past": "window",
				"window":  "2m",
			},
		},
	})
	require.NoError(t, err)
	require.NoError(t, exec.Run(context.Background()))
}

func TestExecutorIfPastFail(t *testing.T) {
	t.Parallel()

	exec, err := newExecutor(context.Background(), ir.Step{
		Commands: []ir.CommandEntry{{Command: opUntil}},
		ExecutorConfig: ir.ExecutorConfig{
			Type: "wait",
			Config: map[string]any{
				"until":   "2000-01-01T00:00:00Z",
				"if_past": "fail",
			},
		},
	})
	require.NoError(t, err)
	require.ErrorContains(t, exec.Run(context.Background()), "in the past")
}

func TestExecutorIfPastWindow(t *testing.T) {
	t.Parallel()

	recent := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)
	exec, err := newExecutor(context.Background(), ir.Step{
		Commands: []ir.CommandEntry{{Command: opUntil}},
		ExecutorConfig: ir.ExecutorConfig{
			Type: "wait",
			Config: map[string]any{
				"until":   recent,
				"if_past": "window",
				"window":  "5m",
			},
		},
	})
	require.NoError(t, err)
	require.NoError(t, exec.Run(context.Background()))
}

// A canceled step reports the cancellation even when a past deadline is
// inside the window, as the other past-deadline paths do.
func TestExecutorIfPastWindowStopsWhenCanceled(t *testing.T) {
	t.Parallel()

	recent := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)
	exec, err := newExecutor(context.Background(), ir.Step{
		Commands: []ir.CommandEntry{{Command: opUntil}},
		ExecutorConfig: ir.ExecutorConfig{
			Type: "wait",
			Config: map[string]any{
				"until":   recent,
				"if_past": "window",
				"window":  "5m",
			},
		},
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, exec.Run(ctx), context.Canceled)
}

func TestNewExecutorRejectsNonHTTPURL(t *testing.T) {
	t.Parallel()

	_, err := newExecutor(context.Background(), ir.Step{
		Commands: []ir.CommandEntry{{Command: opHTTP}},
		ExecutorConfig: ir.ExecutorConfig{
			Type: "wait",
			Config: map[string]any{
				"url": "ftp://example.com/ready",
			},
		},
	})
	require.ErrorContains(t, err, "url must be an absolute HTTP URL")
}

// A reference that survives run-time resolution means it did not resolve, so
// the error says so instead of only reporting the field as malformed.
func TestNewExecutorRejectsUnresolvedReference(t *testing.T) {
	t.Parallel()

	_, err := newExecutor(context.Background(), ir.Step{
		Commands: []ir.CommandEntry{{Command: opHTTP}},
		ExecutorConfig: ir.ExecutorConfig{
			Type: "wait",
			Config: map[string]any{
				"url": "${BASE_URL}/health",
			},
		},
	})
	require.ErrorContains(t, err, "url must be an absolute HTTP URL")
	require.ErrorContains(t, err, "unresolved reference")
}

func TestDeadlineLoggedToStderr(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	exec, err := newExecutor(context.Background(), ir.Step{
		Commands: []ir.CommandEntry{{Command: opUntil}},
		ExecutorConfig: ir.ExecutorConfig{
			Type: "wait",
			Config: map[string]any{
				"until": "2000-01-01T00:00:00Z",
			},
		},
	})
	require.NoError(t, err)
	exec.SetStdout(&stdout)
	exec.SetStderr(&stderr)

	require.NoError(t, exec.Run(context.Background()))
	require.Empty(t, stdout.String())
	require.Contains(t, stderr.String(), "wait: deadline")
}

func TestExecutorRunStopsWhenContextIsCanceled(t *testing.T) {
	t.Parallel()

	exec, err := newExecutor(context.Background(), ir.Step{
		Commands: []ir.CommandEntry{{Command: opDuration}},
		ExecutorConfig: ir.ExecutorConfig{
			Type: "wait",
			Config: map[string]any{
				"duration": "1h",
			},
		},
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	require.ErrorIs(t, exec.Run(ctx), context.Canceled)
}
