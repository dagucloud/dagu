// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package clickhouse

import (
	"context"
	"encoding/pem"
	"fmt"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// echoHandler answers every statement with its own text on one line.
func echoHandler(w http.ResponseWriter, _ *http.Request, req request) {
	_, _ = fmt.Fprintf(w, "%s\n", strings.ReplaceAll(req.Body, "\n", " "))
}

func runFake(t *testing.T, fs *fakeServer, extra map[string]any, sql string) (string, string, error) {
	t.Helper()
	cfg := map[string]any{"url": fs.URL}
	maps.Copy(cfg, extra)
	return runStep(context.Background(), t, cfg, sql)
}

// exceptionReport builds the text the server appends to a response that
// failed after it started streaming.
func exceptionReport(tag, message string) string {
	return fmt.Sprintf("\r\n__exception__\r\n%s\r\n%s%d %s\r\n__exception__\r\n", tag, message, len(message), tag)
}

func TestRequestShape(t *testing.T) {
	t.Parallel()

	fs := newFakeServer(t, echoHandler)
	out, stderr, err := runFake(t, fs, map[string]any{
		"user":     "etl",
		"password": "s3cr3t-pw",
		"database": "analytics",
		"params":   map[string]any{"day": "2024-01-02", "ids": []any{1, 2}},
		"settings": map[string]any{"readonly": 1, "max_execution_time": 30},
	}, "SELECT {day:Date}, {ids:Array(UInt8)}")
	require.NoError(t, err)
	assert.Equal(t, "SELECT {day:Date}, {ids:Array(UInt8)}\n", out)
	assert.Contains(t, stderr, `"status":"success"`)

	reqs := fs.all()
	require.Len(t, reqs, 1)
	r := reqs[0]
	assert.Equal(t, http.MethodPost, r.Method)
	assert.Equal(t, "SELECT {day:Date}, {ids:Array(UInt8)}", r.Body)
	assert.Equal(t, "JSONEachRow", r.Query.Get("default_format"))
	assert.Equal(t, "analytics", r.Query.Get("database"))
	assert.Equal(t, "1", r.Query.Get("readonly"))
	assert.Equal(t, "30", r.Query.Get("max_execution_time"))
	assert.Equal(t, "2024-01-02", r.Query.Get("param_day"))
	assert.Equal(t, "[1,2]", r.Query.Get("param_ids"))
	assert.True(t, strings.HasPrefix(r.Query.Get("query_id"), "dagu-"))
	assert.Empty(t, r.Query.Get("session_id"), "a single statement needs no session")

	// Credentials travel only in headers.
	assert.Equal(t, "etl", r.Header.Get("X-ClickHouse-User"))
	assert.Equal(t, "s3cr3t-pw", r.Header.Get("X-ClickHouse-Key"))
	assert.Empty(t, r.Header.Get("Authorization"))
	assert.NotContains(t, r.RawURL, "s3cr3t-pw")
	assert.NotContains(t, r.RawURL, "etl")
	assert.NotContains(t, stderr, "s3cr3t-pw")
}

func TestRequestWithoutCredentials(t *testing.T) {
	t.Parallel()

	fs := newFakeServer(t, echoHandler)
	_, _, err := runFake(t, fs, nil, "SELECT 1")
	require.NoError(t, err)
	r := fs.all()[0]
	assert.Empty(t, r.Header.Get("X-ClickHouse-User"))
	assert.Empty(t, r.Header.Get("X-ClickHouse-Key"))
	assert.Empty(t, r.Query.Get("database"))
}

func TestRequestKeepsEndpointPath(t *testing.T) {
	t.Parallel()

	var path string
	fs := newFakeServer(t, func(w http.ResponseWriter, r *http.Request, _ request) { path = r.URL.Path })
	_, _, err := runStep(context.Background(), t, map[string]any{"url": fs.URL + "/clickhouse/"}, "SELECT 1")
	require.NoError(t, err)
	assert.Equal(t, "/clickhouse/", path)
}

func TestOutputFormatMapping(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name         string
		cfg          map[string]any
		wantFormat   string
		wantArrayRow string
	}{
		{"Default", nil, "JSONEachRow", ""},
		{"JSONL", map[string]any{"output_format": "jsonl"}, "JSONEachRow", ""},
		{"JSON", map[string]any{"output_format": "json"}, "JSONEachRow", "1"},
		{"CSV", map[string]any{"output_format": "csv"}, "CSV", ""},
		{"CSVHeaders", map[string]any{"output_format": "csv", "headers": true}, "CSVWithNames", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fs := newFakeServer(t, echoHandler)
			_, _, err := runFake(t, fs, tt.cfg, "SELECT 1")
			require.NoError(t, err)
			r := fs.all()[0]
			assert.Equal(t, tt.wantFormat, r.Query.Get("default_format"))
			assert.Equal(t, tt.wantArrayRow, r.Query.Get("output_format_json_array_of_rows"))
		})
	}
}

func TestSettingsOverrideDerivedOnes(t *testing.T) {
	t.Parallel()

	fs := newFakeServer(t, echoHandler)
	_, _, err := runFake(t, fs, map[string]any{
		"output_format": "json",
		"settings":      map[string]any{"output_format_json_array_of_rows": 0},
	}, "SELECT 1")
	require.NoError(t, err)
	assert.Equal(t, "0", fs.all()[0].Query.Get("output_format_json_array_of_rows"))
}

// A script selects the configured database once with USE and sends no database
// parameter afterwards, so a USE statement of the script takes effect.
func TestScriptSelectsDatabaseOnce(t *testing.T) {
	t.Parallel()

	fs := newFakeServer(t, echoHandler)
	_, stderr, err := runFake(t, fs, map[string]any{"database": "ana`lytics"}, "SELECT 1; USE other; SELECT 2")
	require.NoError(t, err)
	reqs := fs.all()
	require.Len(t, reqs, 4)
	assert.Equal(t, "USE `ana\\`lytics`", reqs[0].Body)
	for i, r := range reqs {
		assert.False(t, r.Query.Has("database"), "request %d", i)
		assert.Equal(t, reqs[0].Query.Get("session_id"), r.Query.Get("session_id"))
	}
	assert.Equal(t, 4, strings.Count(stderr, `"status":"success"`), "the USE request is metered as statement 0")
}

// A single statement has no session and carries the database itself.
func TestSingleStatementSendsDatabase(t *testing.T) {
	t.Parallel()

	fs := newFakeServer(t, echoHandler)
	_, _, err := runFake(t, fs, map[string]any{"database": "analytics"}, "SELECT 1")
	require.NoError(t, err)
	require.Len(t, fs.all(), 1)
	assert.Equal(t, "analytics", fs.all()[0].Query.Get("database"))
}

// A failing USE fails the step before any statement runs.
func TestScriptFailsWhenDatabaseIsMissing(t *testing.T) {
	t.Parallel()

	fs := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request, _ request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("Code: 81. DB::Exception: Database nope does not exist\n"))
	})
	_, _, err := runFake(t, fs, map[string]any{"database": "nope"}, "SELECT 1; SELECT 2")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `failed to select database "nope"`)
	assert.Contains(t, err.Error(), "does not exist")
	assert.Len(t, fs.all(), 1)
}

// An exception written into the data would end the body normally and hide the
// failure, so every statement asks for out-of-band reporting.
func TestInBandExceptionsAreOff(t *testing.T) {
	t.Parallel()

	fs := newFakeServer(t, echoHandler)
	_, _, err := runFake(t, fs, map[string]any{"settings": map[string]any{"max_threads": 1}}, "SELECT 1; SELECT 2")
	require.NoError(t, err)
	for _, r := range fs.all() {
		assert.Equal(t, []string{"0"}, r.Query["http_write_exception_in_output_format"])
	}

	_, err = ParseConfig(map[string]any{
		"url":      "http://h",
		"settings": map[string]any{"http_write_exception_in_output_format": 1},
	})
	require.Error(t, err)
}

func TestMultipleStatements(t *testing.T) {
	t.Parallel()

	fs := newFakeServer(t, echoHandler)
	out, stderr, err := runFake(t, fs, nil, "SET max_threads = 1;\nSELECT 'a;b'; -- done\nSELECT 2")
	require.NoError(t, err)
	assert.Equal(t, "SET max_threads = 1\nSELECT 'a;b'\n-- done SELECT 2\n", out)

	reqs := fs.all()
	require.Len(t, reqs, 3)
	session := reqs[0].Query.Get("session_id")
	assert.True(t, strings.HasPrefix(session, "dagu-"))
	ids := map[string]bool{}
	for i, r := range reqs {
		assert.Equal(t, session, r.Query.Get("session_id"), "request %d", i)
		ids[r.Query.Get("query_id")] = true
	}
	assert.Len(t, ids, 3, "every statement has its own query_id")
	assert.Equal(t, 3, strings.Count(stderr, `"status":"success"`))
}

func TestStopsAtFirstError(t *testing.T) {
	t.Parallel()

	fs := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request, req request) {
		if strings.Contains(req.Body, "boom") {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("Code: 62. DB::Exception: Syntax error near boom\n"))
			return
		}
		_, _ = fmt.Fprintln(w, "ok")
	})
	out, stderr, err := runFake(t, fs, nil, "SELECT 1; SELECT boom; SELECT 3")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "statement 2 failed")
	assert.Contains(t, err.Error(), "HTTP 400")
	assert.Contains(t, err.Error(), "Syntax error near boom")
	assert.Equal(t, "ok\n", out)
	assert.Len(t, fs.all(), 2, "the third statement is not sent")
	assert.Contains(t, stderr, `"status":"error"`)
}

// A single statement fails without the "statement N" prefix of a script.
func TestSingleStatementError(t *testing.T) {
	t.Parallel()

	fs := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request, _ request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Code: 60. DB::Exception: Table x doesn't exist\n"))
	})
	_, _, err := runFake(t, fs, nil, "SELECT * FROM x")
	require.Error(t, err)
	assert.Equal(t, "HTTP 500: Code: 60. DB::Exception: Table x doesn't exist", err.Error())
}

func TestEmptyScript(t *testing.T) {
	t.Parallel()

	fs := newFakeServer(t, echoHandler)
	_, _, err := runFake(t, fs, nil, " -- nothing\n ; ")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no clickhouse statement")
	assert.Empty(t, fs.all())
}

func TestErrorTextIsMasked(t *testing.T) {
	t.Parallel()

	fs := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request, req request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = fmt.Fprintf(w, "Code: 516. Authentication failed for key %s\n", req.Header.Get("X-ClickHouse-Key"))
	})
	_, stderr, err := runFake(t, fs, map[string]any{"user": "u", "password": "hunter2-pw"}, "SELECT 1")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "hunter2-pw")
	assert.Contains(t, err.Error(), "***")
	assert.NotContains(t, stderr, "hunter2-pw")
}

// A transport error names the endpoint but neither the parameters in the URL
// nor the password.
// A password that straddles the truncation point is replaced before the text
// is cut, so no part of it reaches the error.
func TestStatusErrorMasksBeforeTruncating(t *testing.T) {
	t.Parallel()

	const password = "hunter2-pw-abcdef"
	fs := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request, _ request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(strings.Repeat("x", maxErrorBody-5) + password + strings.Repeat("y", 100)))
	})
	_, _, err := runFake(t, fs, map[string]any{"user": "u", "password": password}, "SELECT 1")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "hunter2")
	assert.Contains(t, err.Error(), "(truncated)")
	assert.Less(t, len(err.Error()), maxErrorBody+100)
}

func TestTransportErrorIsMasked(t *testing.T) {
	t.Parallel()

	fs := newFakeServer(t, echoHandler)
	url := fs.URL
	fs.Close()
	_, stderr, err := runStep(context.Background(), t, map[string]any{
		"url": url, "user": "u", "password": "hunter2-pw",
		"params": map[string]any{"token": "param-secret-value"},
	}, "SELECT {token:String}")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "param-secret-value")
	assert.NotContains(t, err.Error(), "hunter2-pw")
	assert.NotContains(t, stderr, "param-secret-value")
	assert.Contains(t, err.Error(), url)
}

func TestRedirectIsNotFollowed(t *testing.T) {
	t.Parallel()

	target := newFakeServer(t, echoHandler)
	fs := newFakeServer(t, func(w http.ResponseWriter, r *http.Request, _ request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	})
	_, _, err := runFake(t, fs, map[string]any{"user": "u", "password": "pw"}, "SELECT 1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 307")
	assert.Empty(t, target.all(), "credentials must not be replayed on the redirect target")
}

func TestStreamsResponseAsItArrives(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	fs := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request, _ request) {
		w.Header().Set("X-ClickHouse-Exception-Tag", "tagtagtagtagtagt")
		_, _ = w.Write([]byte("first\n"))
		w.(http.Flusher).Flush()
		<-release
		_, _ = w.Write([]byte("second\n"))
	})

	exec, err := newTestExecutor(context.Background(), map[string]any{"url": fs.URL}, "SELECT 1")
	require.NoError(t, err)
	stdout := &syncBuffer{}
	exec.SetStdout(stdout)
	exec.SetStderr(&syncBuffer{})

	done := make(chan error, 1)
	go func() { done <- exec.Run(context.Background()) }()

	require.Eventually(t, func() bool { return stdout.String() == "first\n" }, 5*time.Second, 10*time.Millisecond,
		"the first rows reach stdout before the statement ends")
	close(release)
	require.NoError(t, <-done)
	assert.Equal(t, "first\nsecond\n", stdout.String())
}

// A large result passes through the exception filter without being buffered.
func TestLargeResultStreams(t *testing.T) {
	t.Parallel()

	const lines = 200000
	fs := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request, _ request) {
		w.Header().Set("X-ClickHouse-Exception-Tag", "tagtagtagtagtagt")
		for i := range lines {
			_, _ = fmt.Fprintf(w, "{\"n\":%d}\n", i)
		}
	})
	out, _, err := runFake(t, fs, nil, "SELECT n")
	require.NoError(t, err)
	assert.Equal(t, lines, strings.Count(out, "\n"))
	assert.True(t, strings.HasSuffix(out, fmt.Sprintf("{\"n\":%d}\n", lines-1)))
}

func TestMidStreamException(t *testing.T) {
	t.Parallel()

	const tag = "abcdefghijklmnop"
	message := "Code: 395. DB::Exception: Value passed to 'throwIf' function is non-zero. (FUNCTION_THROW_IF_VALUE_IS_NON_ZERO) (version 26.9.17.8 (official build))\n"

	t.Run("ReportIsRemovedFromOutput", func(t *testing.T) {
		t.Parallel()
		fs := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request, _ request) {
			w.Header().Set("X-ClickHouse-Exception-Tag", tag)
			_, _ = w.Write([]byte("1\n2\n3\n" + exceptionReport(tag, message)))
		})
		out, stderr, err := runFake(t, fs, nil, "SELECT 1")
		require.Error(t, err)
		assert.Equal(t, "failed after the result started streaming: "+strings.TrimSpace(message), err.Error())
		assert.Equal(t, "1\n2\n3\n", out)
		assert.Contains(t, stderr, `"status":"error"`)
	})

	t.Run("ReportSplitAcrossWrites", func(t *testing.T) {
		t.Parallel()
		// The client often reads flushed chunks in one go, so the filter is
		// driven directly. The body is split at every byte, and both writes
		// reuse one buffer as io.Copy does.
		body := "1\n" + exceptionReport(tag, message)
		buf := make([]byte, len(body))
		for cut := 1; cut < len(body); cut++ {
			var out strings.Builder
			filter := newExceptionFilter(&out, tag)
			for _, part := range []string{body[:cut], body[cut:]} {
				n := copy(buf, part)
				_, err := filter.Write(buf[:n])
				require.NoError(t, err)
			}
			msg, failed, err := filter.finish()
			require.NoError(t, err)
			require.True(t, failed, "cut at %d", cut)
			require.Equal(t, strings.TrimSpace(message), msg, "cut at %d", cut)
			require.Equal(t, "1\n", out.String(), "cut at %d", cut)
		}
	})

	t.Run("AbortedStream", func(t *testing.T) {
		t.Parallel()
		// The server ends the chunked body without the final chunk.
		fs := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request, _ request) {
			w.Header().Set("X-ClickHouse-Exception-Tag", tag)
			_, _ = w.Write([]byte("1\n" + exceptionReport(tag, message)))
			w.(http.Flusher).Flush()
			panic(http.ErrAbortHandler)
		})
		out, _, err := runFake(t, fs, nil, "SELECT 1")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "FUNCTION_THROW_IF_VALUE_IS_NON_ZERO")
		assert.Equal(t, "1\n", out)
	})

	t.Run("MessageIsMasked", func(t *testing.T) {
		t.Parallel()
		fs := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request, _ request) {
			w.Header().Set("X-ClickHouse-Exception-Tag", tag)
			_, _ = w.Write([]byte(exceptionReport(tag, "Code: 1. password is hunter2-pw\n")))
		})
		_, _, err := runFake(t, fs, map[string]any{"password": "hunter2-pw"}, "SELECT 1")
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "hunter2-pw")
	})

	t.Run("LongMessageIsTruncated", func(t *testing.T) {
		t.Parallel()
		long := "Code: 1. DB::Exception: " + strings.Repeat("x", 200000) + "\n"
		fs := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request, _ request) {
			w.Header().Set("X-ClickHouse-Exception-Tag", tag)
			_, _ = w.Write([]byte("1\n" + exceptionReport(tag, long)))
		})
		out, stderr, err := runFake(t, fs, nil, "SELECT 1")
		require.Error(t, err)
		assert.Equal(t, "1\n", out)
		assert.Contains(t, err.Error(), strings.Repeat("x", 1000))
		assert.Contains(t, err.Error(), "(truncated)")
		assert.Less(t, len(err.Error()), maxErrorBody+200)
		assert.Less(t, len(stderr), maxErrorBody+1000)
	})

	t.Run("ForeignTagIsData", func(t *testing.T) {
		t.Parallel()
		data := "a\r\n__exception__\r\nother-tag\r\nnot an error\n"
		fs := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request, _ request) {
			w.Header().Set("X-ClickHouse-Exception-Tag", tag)
			_, _ = w.Write([]byte(data))
		})
		out, _, err := runFake(t, fs, nil, "SELECT 1")
		require.NoError(t, err)
		assert.Equal(t, data, out)
	})

	t.Run("PartialMarkerAtEndIsKept", func(t *testing.T) {
		t.Parallel()
		data := "row\r\n__exception"
		fs := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request, _ request) {
			w.Header().Set("X-ClickHouse-Exception-Tag", tag)
			_, _ = w.Write([]byte(data))
		})
		out, _, err := runFake(t, fs, nil, "SELECT 1")
		require.NoError(t, err)
		assert.Equal(t, data, out)
	})

	t.Run("WithoutTagHeaderBodyIsData", func(t *testing.T) {
		t.Parallel()
		data := "1\n" + exceptionReport(tag, message)
		fs := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request, _ request) {
			_, _ = w.Write([]byte(data))
		})
		out, _, err := runFake(t, fs, nil, "SELECT 1")
		require.NoError(t, err)
		assert.Equal(t, data, out)
	})

	t.Run("AbortWithoutReportIsAnError", func(t *testing.T) {
		t.Parallel()
		fs := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request, _ request) {
			_, _ = w.Write([]byte("1\n"))
			w.(http.Flusher).Flush()
			panic(http.ErrAbortHandler)
		})
		out, _, err := runFake(t, fs, nil, "SELECT 1")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "the server may have failed while streaming")
		assert.Equal(t, "1\n", out)
	})
}

func TestCancelStopsStatementOnServer(t *testing.T) {
	t.Parallel()

	started := make(chan struct{}, 1)
	fs := newFakeServer(t, func(w http.ResponseWriter, r *http.Request, req request) {
		if req.isKill() {
			_, _ = w.Write([]byte("waiting\tid\tuser\tquery\n"))
			return
		}
		started <- struct{}{}
		<-r.Context().Done()
	})

	exec, err := newTestExecutor(context.Background(), map[string]any{
		"url": fs.URL, "user": "u", "password": "pw",
		"settings": map[string]any{"readonly": 1}, "timeout": 0,
	}, "SELECT sleep(3); SELECT 2")
	require.NoError(t, err)
	exec.SetStdout(&syncBuffer{})
	stderr := &syncBuffer{}
	exec.SetStderr(stderr)

	done := make(chan error, 1)
	go func() { done <- exec.Run(context.Background()) }()
	<-started
	require.NoError(t, exec.Kill(os.Interrupt))

	select {
	case err := <-done:
		require.Error(t, err)
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return")
	}

	stmts, kills := fs.statements(), fs.kills()
	require.Len(t, stmts, 1, "the second statement is not started")
	require.Len(t, kills, 1)
	k := kills[0]
	assert.Equal(t, "KILL QUERY WHERE query_id = {query_id:String}", k.Body)
	assert.Equal(t, stmts[0].Query.Get("query_id"), k.Query.Get("param_query_id"))
	assert.Equal(t, "u", k.Header.Get("X-ClickHouse-User"))
	assert.Equal(t, "pw", k.Header.Get("X-ClickHouse-Key"))
	// The running statement may hold the session; the kill request carries
	// neither it nor the step settings.
	assert.Empty(t, k.Query.Get("session_id"))
	assert.Empty(t, k.Query.Get("readonly"))
	assert.Empty(t, k.Query.Get("database"))
	assert.Contains(t, stderr.String(), `"status":"error"`)
}

func TestTimeoutStopsStatementOnServer(t *testing.T) {
	t.Parallel()

	fs := newFakeServer(t, func(w http.ResponseWriter, r *http.Request, req request) {
		if req.isKill() {
			_, _ = w.Write([]byte("waiting\n"))
			return
		}
		<-r.Context().Done()
	})
	start := time.Now()
	_, _, err := runFake(t, fs, map[string]any{"timeout": 1}, "SELECT sleep(3)")
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Contains(t, err.Error(), "timed out after 1s")
	assert.Less(t, time.Since(start), 5*time.Second)
	assert.Len(t, fs.kills(), 1)
}

// KILL QUERY is retried while the server answers that it knows no such query.
func TestKillRetry(t *testing.T) {
	t.Parallel()

	var killCount atomic.Int32
	started := make(chan struct{}, 1)
	fs := newFakeServer(t, func(w http.ResponseWriter, r *http.Request, req request) {
		if req.isKill() {
			if killCount.Add(1) >= 3 {
				_, _ = w.Write([]byte("waiting\n"))
			}
			return
		}
		started <- struct{}{}
		<-r.Context().Done()
	})

	exec, err := newTestExecutor(context.Background(), map[string]any{"url": fs.URL, "timeout": 0}, "SELECT 1")
	require.NoError(t, err)
	exec.(*clickhouseExecutor).client.killRetryDelay = 10 * time.Millisecond
	exec.SetStdout(&syncBuffer{})
	exec.SetStderr(&syncBuffer{})
	done := make(chan error, 1)
	go func() { done <- exec.Run(context.Background()) }()
	<-started
	require.NoError(t, exec.Kill(nil))
	require.Error(t, <-done)
	assert.Len(t, fs.kills(), 3)
}

// A refused KILL QUERY is not retried and does not hide the cancellation.
func TestKillFailure(t *testing.T) {
	t.Parallel()

	started := make(chan struct{}, 1)
	fs := newFakeServer(t, func(w http.ResponseWriter, r *http.Request, req request) {
		if req.isKill() {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte("Code: 497. Not enough privileges to KILL\n"))
			return
		}
		started <- struct{}{}
		<-r.Context().Done()
	})
	exec, err := newTestExecutor(context.Background(), map[string]any{"url": fs.URL, "timeout": 0}, "SELECT 1")
	require.NoError(t, err)
	exec.SetStdout(&syncBuffer{})
	exec.SetStderr(&syncBuffer{})
	done := make(chan error, 1)
	go func() { done <- exec.Run(context.Background()) }()
	<-started
	require.NoError(t, exec.Kill(nil))
	err = <-done
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Len(t, fs.kills(), 1, "a refused kill is not retried")
}

func TestParentContextCancelStopsStatement(t *testing.T) {
	t.Parallel()

	started := make(chan struct{}, 1)
	fs := newFakeServer(t, func(w http.ResponseWriter, r *http.Request, req request) {
		if req.isKill() {
			_, _ = w.Write([]byte("waiting\n"))
			return
		}
		started <- struct{}{}
		<-r.Context().Done()
	})
	exec, err := newTestExecutor(context.Background(), map[string]any{"url": fs.URL, "timeout": 0}, "SELECT 1")
	require.NoError(t, err)
	exec.SetStdout(&syncBuffer{})
	exec.SetStderr(&syncBuffer{})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- exec.Run(ctx) }()
	<-started
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
	assert.Len(t, fs.kills(), 1)
}

func TestFileOption(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "load.sql"), []byte("-- a; b\nSELECT 1;\nSELECT 'x;y';\n"), 0o600))
	fs := newFakeServer(t, echoHandler)

	run := func(t *testing.T, file string) (string, error) {
		step := ir.Step{
			Name:           "file-step",
			ExecutorConfig: ir.ExecutorConfig{Type: executorType, Config: map[string]any{"url": fs.URL, "file": file}},
		}
		exec, err := newClickHouseExecutor(testContext(t, dir, step), step)
		require.NoError(t, err)
		out := &syncBuffer{}
		exec.SetStdout(out)
		exec.SetStderr(&syncBuffer{})
		err = exec.Run(context.Background())
		return out.String(), err
	}

	t.Run("RelativeToWorkingDirectory", func(t *testing.T) {
		out, err := run(t, "load.sql")
		require.NoError(t, err)
		assert.Equal(t, "-- a; b SELECT 1\nSELECT 'x;y'\n", out)
	})
	t.Run("Absolute", func(t *testing.T) {
		out, err := run(t, filepath.Join(dir, "load.sql"))
		require.NoError(t, err)
		assert.Equal(t, "-- a; b SELECT 1\nSELECT 'x;y'\n", out)
	})
	t.Run("ByteOrderMarkIsIgnored", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "bom.sql"), []byte("\ufeffSELECT 1"), 0o600))
		out, err := run(t, "bom.sql")
		require.NoError(t, err)
		assert.Equal(t, "SELECT 1\n", out)
	})
	t.Run("Missing", func(t *testing.T) {
		_, err := run(t, "missing.sql")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to read sql file")
	})
}

// A step with a command and a file option is rejected.
func TestQueryAndFileConflict(t *testing.T) {
	t.Parallel()

	step := ir.Step{
		Commands:       []ir.CommandEntry{{Command: "SELECT", CmdWithArgs: "SELECT 1"}},
		ExecutorConfig: ir.ExecutorConfig{Type: executorType, Config: map[string]any{"url": "http://h", "file": "q.sql"}},
	}
	_, err := newClickHouseExecutor(context.Background(), step)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mutually exclusive")
}

func TestScriptFieldIsUsed(t *testing.T) {
	t.Parallel()

	fs := newFakeServer(t, echoHandler)
	step := ir.Step{
		Script:         "SELECT 1;\nSELECT 2;",
		ExecutorConfig: ir.ExecutorConfig{Type: executorType, Config: map[string]any{"url": fs.URL}},
	}
	exec, err := newClickHouseExecutor(context.Background(), step)
	require.NoError(t, err)
	out := &syncBuffer{}
	exec.SetStdout(out)
	exec.SetStderr(&syncBuffer{})
	require.NoError(t, exec.Run(context.Background()))
	assert.Equal(t, "SELECT 1\nSELECT 2\n", out.String())
}

func TestTLSSkipVerify(t *testing.T) {
	t.Parallel()

	tlsServer := newTLSFakeServer(t)

	_, _, err := runStep(context.Background(), t, map[string]any{"url": tlsServer.URL}, "SELECT 1")
	require.Error(t, err, "an untrusted certificate is rejected by default")

	out, _, err := runStep(context.Background(), t, map[string]any{"url": tlsServer.URL, "tls_skip_verify": true}, "SELECT 1")
	require.NoError(t, err)
	assert.Equal(t, "SELECT 1\n", out)
}

// A server certificate is trusted when its CA is in the tls_ca bundle.
func TestTLSCA(t *testing.T) {
	t.Parallel()

	srv := newTLSFakeServer(t)
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0o600))

	out, _, err := runStep(context.Background(), t, map[string]any{"url": srv.URL, "tls_ca": caFile}, "SELECT 1")
	require.NoError(t, err)
	assert.Equal(t, "SELECT 1\n", out)

	_, err = newTestExecutor(context.Background(), map[string]any{"url": srv.URL, "tls_ca": filepath.Join(t.TempDir(), "missing.pem")}, "SELECT 1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tls_ca")

	empty := filepath.Join(t.TempDir(), "empty.pem")
	require.NoError(t, os.WriteFile(empty, []byte("not a certificate"), 0o600))
	_, err = newTestExecutor(context.Background(), map[string]any{"url": srv.URL, "tls_ca": empty}, "SELECT 1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no PEM certificate")
}

func TestCloseIsIdempotent(t *testing.T) {
	t.Parallel()

	fs := newFakeServer(t, echoHandler)
	exec, err := newTestExecutor(context.Background(), map[string]any{"url": fs.URL}, "SELECT 1")
	require.NoError(t, err)
	c := exec.(*clickhouseExecutor)
	require.NoError(t, c.Close())
	require.NoError(t, c.Close())
}
