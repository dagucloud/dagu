// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	filedagrun "github.com/dagucloud/dagu/v2/internal/persis/file/dagrun"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStepLogArchive(t *testing.T) {
	dir := t.TempDir()
	raw := []byte{0xff, 0, 'x', '\r', '\n'}
	log := filepath.Join(dir, "output")
	empty := filepath.Join(dir, "empty")
	require.NoError(t, os.WriteFile(log, raw, 0o600))
	require.NoError(t, os.WriteFile(empty, nil, 0o600))
	response := stepLogArchiveResponse{
		ctx: t.Context(), filename: "run-steps.zip",
		openLog: filedagrun.NewStore(dir).OpenLog,
		status: &ir.DAGRunStatus{
			OnInit: &ir.Node{Step: ir.Step{Name: "init"}, Stdout: log},
			Nodes: []*ir.Node{
				{Step: ir.Step{Name: "../build"}, Stdout: log, Stderr: empty},
				{Step: ir.Step{Name: "../build"}, Stdout: log},
				{Step: ir.Step{Name: "missing"}, Stdout: filepath.Join(dir, "absent")},
				{Step: ir.Step{Name: "skipped"}},
			},
			OnExit: &ir.Node{Step: ir.Step{Name: "cleanup"}, Stderr: log},
		},
	}
	w := httptest.NewRecorder()
	require.NoError(t, response.VisitDownloadDAGRunStepLogsResponse(w))
	require.Equal(t, stepLogArchiveContentType, w.Header().Get("Content-Type"))
	require.Equal(t, `attachment; filename="run-steps.zip"`, w.Header().Get("Content-Disposition"))
	archive, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	require.NoError(t, err)
	var names []string
	for _, entry := range archive.File {
		names = append(names, entry.Name)
		reader, err := entry.Open()
		require.NoError(t, err)
		data, err := io.ReadAll(reader)
		require.NoError(t, err)
		require.NoError(t, reader.Close())
		if entry.Name == "002-___build/stderr.log" {
			require.Empty(t, data)
		} else {
			require.Equal(t, raw, data)
		}
	}
	require.Equal(t, []string{"001-init/stdout.log", "002-___build/stdout.log", "002-___build/stderr.log", "003-___build/stdout.log", "006-cleanup/stderr.log"}, names)
}

func TestStepLogArchiveEmpty(t *testing.T) {
	response := stepLogArchiveResponse{ctx: t.Context(), status: &ir.DAGRunStatus{}}
	w := httptest.NewRecorder()
	require.NoError(t, response.VisitDownloadSubDAGRunStepLogsResponse(w))
	archive, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	require.NoError(t, err)
	require.Empty(t, archive.File)
}

func TestStepLogArchiveLarge(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "large.log")
	file, err := os.Create(path)
	require.NoError(t, err)
	// A sparse file exceeds the former limit without a large fixture or allocation.
	const size = 65 << 20
	require.NoError(t, file.Truncate(size))
	_, err = file.WriteAt([]byte("tail"), size-4)
	require.NoError(t, err)
	require.NoError(t, file.Close())
	response := stepLogArchiveResponse{
		ctx: t.Context(), openLog: filedagrun.NewStore(dir).OpenLog,
		status: &ir.DAGRunStatus{Nodes: []*ir.Node{{Step: ir.Step{Name: "large"}, Stdout: path}}},
	}
	var buf bytes.Buffer
	require.NoError(t, response.writeArchive(&buf))
	archive, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	require.NoError(t, err)
	require.Len(t, archive.File, 1)
	require.EqualValues(t, size, archive.File[0].UncompressedSize64)
	reader, err := archive.File[0].Open()
	require.NoError(t, err)
	defer func() { _ = reader.Close() }()
	_, err = io.CopyN(io.Discard, reader, size-4)
	require.NoError(t, err)
	tail, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.Equal(t, "tail", string(tail))
}

type archiveTestReader struct {
	io.Reader
	closed bool
}

func (r *archiveTestReader) Close() error {
	r.closed = true
	return nil
}

type archiveErrorReader struct{}

func (archiveErrorReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestStepLogArchiveAbort(t *testing.T) {
	for _, failure := range []string{"open", "read", "cancel"} {
		t.Run(failure, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			reader := &archiveTestReader{Reader: archiveErrorReader{}}
			response := stepLogArchiveResponse{
				ctx:    ctx,
				status: &ir.DAGRunStatus{Nodes: []*ir.Node{{Stdout: "log"}}},
				openLog: func(context.Context, string) (io.ReadCloser, error) {
					if failure == "open" {
						return nil, errors.New("cannot open log")
					}
					return reader, nil
				},
			}
			if failure == "cancel" {
				cancel()
			}
			w := httptest.NewRecorder()
			require.PanicsWithValue(t, http.ErrAbortHandler, func() { _ = response.writeTo(w) })
			_, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
			require.Error(t, err)
			if failure == "read" {
				require.True(t, reader.closed)
			}
		})
	}
}

func TestStepLogArchiveStreams(t *testing.T) {
	first := make([]byte, 256<<10)
	_, err := rand.Read(first)
	require.NoError(t, err)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := stepLogArchiveResponse{
			ctx:    r.Context(),
			status: &ir.DAGRunStatus{Nodes: []*ir.Node{{Stdout: "first", Stderr: "second"}}},
			openLog: func(ctx context.Context, path string) (io.ReadCloser, error) {
				if path == "first" {
					return io.NopCloser(bytes.NewReader(first)), nil
				}
				select {
				case <-release:
					return io.NopCloser(strings.NewReader("second")), nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			},
		}
		_ = response.writeTo(w)
	}))
	defer server.Close()
	defer close(release)
	client := server.Client()
	client.Timeout = 5 * time.Second
	resp, err := client.Get(server.URL)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	// The second file remains blocked until the first bytes reach the client.
	data := make([]byte, 4096)
	_, err = io.ReadFull(resp.Body, data)
	require.NoError(t, err)
	assert.Equal(t, []byte("PK\x03\x04"), data[:4])
}

func TestLogFileDownload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "large.log")
	file, err := os.Create(path)
	require.NoError(t, err)
	// A sparse file provides a large log without a large fixture or allocation.
	tail := []byte{0xff, 0, 'x', '\r', '\n'}
	const size = 65 << 20
	require.NoError(t, file.Truncate(size))
	_, err = file.WriteAt(tail, size-int64(len(tail)))
	require.NoError(t, err)
	require.NoError(t, file.Close())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reader, err := filedagrun.NewStore(dir).OpenLog(r.Context(), path)
		if !assert.NoError(t, err) {
			return
		}
		response := logFileResponse{ctx: r.Context(), reader: reader, filename: "run-step-stdout.log"}
		_ = response.VisitDownloadDAGRunStepLogResponse(w)
	}))
	defer server.Close()
	// Transfer time depends on the runner, so the download is cancelled only
	// when it stops making progress; a total timeout would fail a complete but
	// slow transfer.
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stalled := time.AfterFunc(logDownloadStallTimeout, cancel)
	defer stalled.Stop()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	require.NoError(t, err)
	resp, err := server.Client().Do(request)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, "text/plain", resp.Header.Get("Content-Type"))
	require.Equal(t, `attachment; filename="run-step-stdout.log"`, resp.Header.Get("Content-Disposition"))
	body := progressReader{Reader: resp.Body, progress: func() { stalled.Reset(logDownloadStallTimeout) }}
	_, err = io.CopyN(io.Discard, body, size-int64(len(tail)))
	require.NoError(t, err)
	got, err := io.ReadAll(body)
	require.NoError(t, err)
	require.Equal(t, tail, got)
}

// logDownloadStallTimeout is how long a download may go without receiving any
// bytes before the test treats it as stalled.
const logDownloadStallTimeout = 30 * time.Second

// progressReader calls progress whenever a read returns data.
type progressReader struct {
	io.Reader
	progress func()
}

func (r progressReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if n > 0 {
		r.progress()
	}
	return n, err
}

// blockedReader yields no data until release closes or ctx ends.
type blockedReader struct {
	ctx     context.Context
	release <-chan struct{}
}

func (r blockedReader) Read([]byte) (int, error) {
	select {
	case <-r.release:
		return 0, io.EOF
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	}
}

func TestLogFileStreams(t *testing.T) {
	first := make([]byte, 256<<10)
	_, err := rand.Read(first)
	require.NoError(t, err)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := logFileResponse{
			ctx:      r.Context(),
			reader:   io.NopCloser(io.MultiReader(bytes.NewReader(first), blockedReader{ctx: r.Context(), release: release})),
			filename: "run-scheduler.log",
		}
		_ = response.VisitDownloadSubDAGRunLogResponse(w)
	}))
	defer server.Close()
	defer close(release)
	client := server.Client()
	client.Timeout = 5 * time.Second
	resp, err := client.Get(server.URL)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	// The rest of the log remains blocked until the first bytes reach the client.
	data := make([]byte, 4096)
	_, err = io.ReadFull(resp.Body, data)
	require.NoError(t, err)
	assert.Equal(t, first[:len(data)], data)
}

type closeRecorder struct {
	io.ReadCloser
	closed bool
}

func (r *closeRecorder) Close() error {
	r.closed = true
	return r.ReadCloser.Close()
}

func TestLogFileAbort(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "log")
	require.NoError(t, os.WriteFile(path, []byte("log"), 0o600))
	for _, failure := range []string{"read", "cancel"} {
		t.Run(failure, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			source := io.NopCloser(archiveErrorReader{})
			if failure == "cancel" {
				var err error
				source, err = filedagrun.NewStore(dir).OpenLog(ctx, path)
				require.NoError(t, err)
				// A client disconnect cancels the request context before the log is copied.
				cancel()
			}
			reader := &closeRecorder{ReadCloser: source}
			response := logFileResponse{ctx: ctx, reader: reader, filename: "run.log"}
			w := httptest.NewRecorder()
			require.PanicsWithValue(t, http.ErrAbortHandler, func() { _ = response.writeTo(w) })
			require.Empty(t, w.Body.String())
			require.True(t, reader.closed)
		})
	}
}

func TestLogDownloadDeadline(t *testing.T) {
	for _, suffix := range []string{
		"/log/download",
		"/steps/build/log/download",
		"/steps/log/download",
		"/sub-dag-runs/child/log/download",
		"/sub-dag-runs/child/steps/build/log/download",
		"/sub-dag-runs/child/steps/log/download",
	} {
		t.Run(suffix, func(t *testing.T) {
			server := httptest.NewUnstartedServer(logDownloadDeadline("/api/v1")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, "head")
				_ = http.NewResponseController(w).Flush()
				// Finish after the normal server write deadline.
				time.Sleep(100 * time.Millisecond)
				_, _ = io.WriteString(w, "tail")
			})))
			server.Config.WriteTimeout = 50 * time.Millisecond
			server.Start()
			defer server.Close()
			client := server.Client()
			client.Timeout = 5 * time.Second
			resp, err := client.Get(server.URL + "/api/v1/dag-runs/example/run" + suffix)
			require.NoError(t, err)
			defer func() { _ = resp.Body.Close() }()
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.Equal(t, "headtail", string(body))
		})
	}
}

func TestStepLogFormValidation(t *testing.T) {
	for _, tc := range []struct {
		name, body, contentType, header string
		wantStatus                      int
	}{
		{name: "header wins", body: "token=form-token", contentType: "application/x-www-form-urlencoded", header: "Bearer header-token", wantStatus: http.StatusOK},
		{name: "malformed form", body: "token=%xx", contentType: "application/x-www-form-urlencoded", wantStatus: http.StatusBadRequest},
		{name: "wrong content type", body: "token=form-token", contentType: "text/plain", wantStatus: http.StatusUnsupportedMediaType},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler := stepLogDownloadFormAuth("/api/v1")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, tc.header, r.Header.Get("Authorization"))
				w.WriteHeader(http.StatusOK)
			}))
			request := httptest.NewRequest(http.MethodPost, "/api/v1/dag-runs/example/run/steps/log/download", strings.NewReader(tc.body))
			request.Header.Set("Content-Type", tc.contentType)
			request.Header.Set("Authorization", tc.header)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			require.Equal(t, tc.wantStatus, recorder.Code)
		})
	}
}
