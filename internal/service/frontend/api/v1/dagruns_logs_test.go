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
