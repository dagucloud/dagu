// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package wait

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

func TestExecutorRunWaitsForRedirectStatus(t *testing.T) {
	t.Parallel()

	var finalHits atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/final", http.StatusFound)
	})
	mux.HandleFunc("/final", func(w http.ResponseWriter, _ *http.Request) {
		finalHits.Add(1)
		w.WriteHeader(http.StatusOK)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	exec := newHTTPWait(t, map[string]any{
		"url":    server.URL + "/ready",
		"status": http.StatusFound,
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	require.NoError(t, exec.Run(ctx))
	require.Zero(t, finalHits.Load(), "redirect should not be followed")
}

func TestExecutorRunSendsHostHeader(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "virtual.example" {
			w.WriteHeader(http.StatusMisdirectedRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	exec := newHTTPWait(t, map[string]any{
		"url":     server.URL,
		"status":  http.StatusNoContent,
		"headers": map[string]string{"Host": "virtual.example"},
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	require.NoError(t, exec.Run(ctx))
}

// Repeated identical results are logged once, so a short poll_interval does
// not flood the step log.
func TestHTTPStatusMismatchLoggedToStderr(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) < 4 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	var stderr bytes.Buffer
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
	exec.SetStderr(&stderr)

	require.NoError(t, exec.Run(context.Background()))
	require.Equal(t, "wait: http GET got status 503, want 204\n", stderr.String())
}

// lineBuffer is a stderr that the test can read while the step still writes
// to it. first is closed on the first write.
type lineBuffer struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	once  sync.Once
	first chan struct{}
}

func (b *lineBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.once.Do(func() { close(b.first) })
	return b.buf.Write(p)
}

func (b *lineBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// newHTTPWait returns a wait.http executor for cfg that polls every 1ms.
func newHTTPWait(t *testing.T, cfg map[string]any) *executorImpl {
	t.Helper()

	cfg["poll_interval"] = "1ms"
	exec, err := newExecutor(context.Background(), ir.Step{
		Commands:       []ir.CommandEntry{{Command: opHTTP}},
		ExecutorConfig: ir.ExecutorConfig{Type: "wait", Config: cfg},
	})
	require.NoError(t, err)
	return exec.(*executorImpl)
}

// runHTTPUntilLogged runs exec until it writes its first stderr line, keeps
// polling 20ms more so a repeated line would show up, then cancels the step
// and returns what it wrote. It waits for the line instead of using a fixed
// deadline, because the first poll can be slow on a busy CI runner with -race.
func runHTTPUntilLogged(t *testing.T, exec *executorImpl) string {
	t.Helper()

	stderr := &lineBuffer{first: make(chan struct{})}
	exec.SetStderr(stderr)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- exec.Run(ctx) }()

	select {
	case <-stderr.first:
		time.Sleep(20 * time.Millisecond)
	case <-time.After(10 * time.Second):
	}
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
	return stderr.String()
}

// A TLS server the client doesn't trust fails the same way, and fast, on
// every OS. The URL is left out of the message because its query string may
// carry a token.
func TestHTTPRequestErrorLoggedToStderr(t *testing.T) {
	t.Parallel()

	server := httptest.NewUnstartedServer(http.NotFoundHandler())
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.StartTLS()
	defer server.Close()

	stderr := runHTTPUntilLogged(t, newHTTPWait(t, map[string]any{"url": server.URL + "/ready?token=secret"}))
	requireOneLine(t, stderr, "wait: http GET request failed: tls: failed to verify certificate: ")
}

func TestHTTPSkipTLSVerify(t *testing.T) {
	t.Parallel()

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	exec := newHTTPWait(t, map[string]any{"url": server.URL, "skip_tls_verify": true})
	var stderr bytes.Buffer
	exec.SetStderr(&stderr)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, exec.Run(ctx))
	require.Empty(t, stderr.String())
}

// A reset connection's error carries the local port, which changes on every
// poll. The line must still be logged once.
func TestHTTPConnResetLoggedOnce(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_, _ = conn.Read(make([]byte, 4096))
			_ = conn.(*net.TCPConn).SetLinger(0)
			_ = conn.Close()
		}
	}()

	stderr := runHTTPUntilLogged(t, newHTTPWait(t, map[string]any{"url": "http://" + ln.Addr().String() + "/ready?token=secret"}))
	requireOneLine(t, stderr, "wait: http GET request failed: read tcp "+ln.Addr().String()+": ")
}

// A request that outlives request_timeout logs the timeout on every OS.
func TestHTTPTimeoutLoggedToStderr(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()

	stderr := runHTTPUntilLogged(t, newHTTPWait(t, map[string]any{
		"url":             server.URL + "/ready?token=secret",
		"request_timeout": "10ms",
	}))
	requireOneLine(t, stderr, "wait: http GET request failed: ")
	require.Contains(t, stderr, "Client.Timeout exceeded")
}

// A proxy that refuses CONNECT fails with text net/http doesn't type, so the
// line names the proxy to tell its failure apart from the target's.
func TestHTTPProxyNamedInStderr(t *testing.T) {
	t.Parallel()

	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusProxyAuthRequired)
	}))
	defer proxy.Close()
	proxyURL, err := url.Parse(proxy.URL)
	require.NoError(t, err)
	proxyURL.User = url.UserPassword("user", "secret")

	exec := newHTTPWait(t, map[string]any{"url": "https://example.invalid/ready"})
	exec.proxy = http.ProxyURL(proxyURL)
	stderr := runHTTPUntilLogged(t, exec)
	requireOneLine(t, stderr, "wait: http GET request via proxy "+proxyURL.Host+" failed: ")
}

// A 502 from the proxy reads the same as one from the target, so the status
// line names the proxy too. An http:// target is proxied without CONNECT, so
// the proxy answers the request itself.
func TestHTTPProxyNamedInStatusLine(t *testing.T) {
	t.Parallel()

	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer proxy.Close()
	proxyURL, err := url.Parse(proxy.URL)
	require.NoError(t, err)

	exec := newHTTPWait(t, map[string]any{"url": "http://example.invalid/ready?token=secret"})
	exec.proxy = http.ProxyURL(proxyURL)
	stderr := runHTTPUntilLogged(t, exec)
	requireOneLine(t, stderr, "wait: http GET via proxy "+proxyURL.Host+" got status 502, want 200")
}

// requireOneLine checks that stderr holds a single line starting with prefix
// and nothing from the URL's query string.
func requireOneLine(t *testing.T, stderr, prefix string) {
	t.Helper()

	require.Equal(t, 1, strings.Count(stderr, "\n"), stderr)
	require.True(t, strings.HasPrefix(stderr, prefix), stderr)
	require.NotContains(t, stderr, "secret")
}

const detailsLeftOutLine = "wait: http GET request failed: details left out, they may contain the URL or response text\n"

// net/http quotes text from the server in these errors, and that text may
// carry a token, so the error text is replaced.
func TestHTTPServerTextLeftOutOfStderr(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name    string
		handler http.HandlerFunc
	}{
		{
			name: "malformedRedirect",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Location", "/ready%zz?token=secret")
				w.WriteHeader(http.StatusFound)
			},
		},
		{
			name: "malformedStatusLine",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				conn, buf, err := http.NewResponseController(w).Hijack()
				if err != nil {
					return
				}
				defer func() { _ = conn.Close() }()
				_, _ = buf.WriteString("HTTP/1.1 token=secret\r\n\r\n")
				_ = buf.Flush()
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(tt.handler)
			defer server.Close()

			stderr := runHTTPUntilLogged(t, newHTTPWait(t, map[string]any{"url": server.URL + "/ready"}))
			require.Equal(t, detailsLeftOutLine, stderr)
		})
	}
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

func TestRequestErrorClearsNestedLocalAddress(t *testing.T) {
	t.Parallel()

	newErr := func(port int) error {
		inner := &net.OpError{
			Op:     "read",
			Net:    "tcp",
			Source: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port},
			Addr:   &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 61431},
			Err:    errors.New("connection reset by peer"),
		}
		return &net.OpError{Op: "proxyconnect", Net: "tcp", Err: inner}
	}

	first, second := requestError(newErr(61432)), requestError(newErr(61433))
	require.Equal(t, first, second)
	require.NotContains(t, first, "61432")
}

// A header value with a trailing newline, such as a token read from a file,
// fails before the request is sent, so the line must not blame the server.
func TestHTTPClientSideErrorNotBlamedOnServer(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer server.Close()

	stderr := runHTTPUntilLogged(t, newHTTPWait(t, map[string]any{
		"url":     server.URL,
		"headers": map[string]any{"Authorization": "Bearer abc\n"},
	}))
	require.Equal(t, detailsLeftOutLine, stderr)
}
