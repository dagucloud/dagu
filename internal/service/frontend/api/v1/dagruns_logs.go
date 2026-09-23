// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/go-chi/chi/v5/middleware"
)

const (
	stepLogArchiveContentType = "application/zip"
	stepLogFormContentType    = "application/x-www-form-urlencoded"
)

type stepLogArchiveResponse struct {
	ctx      context.Context
	status   *ir.DAGRunStatus
	openLog  func(context.Context, string) (io.ReadCloser, error)
	filename string
}

func (r *stepLogArchiveResponse) VisitDownloadDAGRunStepLogsResponse(w http.ResponseWriter) error {
	return r.writeTo(w)
}

func (r *stepLogArchiveResponse) VisitDownloadSubDAGRunStepLogsResponse(w http.ResponseWriter) error {
	return r.writeTo(w)
}

func (r *stepLogArchiveResponse) writeTo(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", stepLogArchiveContentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", r.filename))
	w.WriteHeader(http.StatusOK)
	if err := r.writeArchive(w); err != nil {
		logger.Error(r.ctx, "Failed to stream step log archive", tag.Error(err))
		// Headers are committed; abort instead of appending JSON or finalizing a partial ZIP.
		panic(http.ErrAbortHandler)
	}
	return nil
}

func (r *stepLogArchiveResponse) writeArchive(w io.Writer) error {
	archive := zip.NewWriter(w)
	for i, node := range r.status.NodesInRunOrder() {
		if node == nil {
			continue
		}
		directory := fmt.Sprintf("%03d-%s", i+1, fileutil.SafeName(node.Step.Name))
		for _, stream := range []struct{ name, path string }{
			{"stdout", node.Stdout}, {"stderr", node.Stderr},
		} {
			if err := r.ctx.Err(); err != nil {
				return err
			}
			if stream.path == "" {
				continue
			}
			reader, err := r.openLog(r.ctx, stream.path)
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				return err
			}
			entry, err := archive.Create(directory + "/" + stream.name + ".log")
			if err == nil {
				_, err = io.Copy(entry, reader)
			}
			closeErr := reader.Close()
			if err != nil {
				return err
			}
			if closeErr != nil {
				return closeErr
			}
		}
	}
	if err := r.ctx.Err(); err != nil {
		return err
	}
	return archive.Close()
}

// logFileResponse streams one log file as a text attachment and closes it afterwards.
type logFileResponse struct {
	ctx      context.Context
	reader   io.ReadCloser
	filename string
}

func (r *logFileResponse) VisitDownloadDAGRunLogResponse(w http.ResponseWriter) error {
	return r.writeTo(w)
}

func (r *logFileResponse) VisitDownloadDAGRunStepLogResponse(w http.ResponseWriter) error {
	return r.writeTo(w)
}

func (r *logFileResponse) VisitDownloadSubDAGRunLogResponse(w http.ResponseWriter) error {
	return r.writeTo(w)
}

func (r *logFileResponse) VisitDownloadSubDAGRunStepLogResponse(w http.ResponseWriter) error {
	return r.writeTo(w)
}

func (r *logFileResponse) writeTo(w http.ResponseWriter) error {
	defer func() { _ = r.reader.Close() }()
	w.Header().Set("Content-Type", "text/plain")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", r.filename))
	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(w, r.reader); err != nil {
		logger.Error(r.ctx, "Failed to stream log download", tag.Error(err))
		// Headers are committed; abort so clients cannot mistake a partial log for a complete one.
		panic(http.ErrAbortHandler)
	}
	return nil
}

func isStepLogDownload(r *http.Request, apiBasePath string) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		return false
	}
	suffix, ok := strings.CutPrefix(r.URL.Path, strings.TrimRight(apiBasePath, "/")+"/dag-runs/")
	if !ok {
		return false
	}
	parts := strings.Split(suffix, "/")
	return (len(parts) == 5 && strings.Join(parts[2:], "/") == "steps/log/download") ||
		(len(parts) == 7 && parts[2] == "sub-dag-runs" && strings.Join(parts[4:], "/") == "steps/log/download")
}

// isLogDownload matches scheduler, step, and all-step log downloads.
func isLogDownload(r *http.Request, apiBasePath string) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		return false
	}
	suffix, ok := strings.CutPrefix(r.URL.Path, strings.TrimRight(apiBasePath, "/")+"/dag-runs/")
	return ok && strings.HasSuffix(suffix, "/log/download")
}

func logDownloadDeadline(apiBasePath string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isLogDownload(r, apiBasePath) {
				if err := http.NewResponseController(w).SetWriteDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
					logger.Error(r.Context(), "Failed to clear log download deadline", tag.Error(err))
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// stepLogFormResponse preserves the GET response contract for browser form downloads.
type stepLogFormResponse func(http.ResponseWriter) error

func (r stepLogFormResponse) VisitDownloadDAGRunStepLogsFormResponse(w http.ResponseWriter) error {
	return r(w)
}

func (r stepLogFormResponse) VisitDownloadSubDAGRunStepLogsFormResponse(w http.ResponseWriter) error {
	return r(w)
}

func (a *API) DownloadDAGRunStepLogsForm(ctx context.Context, request api.DownloadDAGRunStepLogsFormRequestObject) (api.DownloadDAGRunStepLogsFormResponseObject, error) {
	response, err := a.DownloadDAGRunStepLogs(ctx, api.DownloadDAGRunStepLogsRequestObject{
		Name: request.Name, DagRunId: request.DagRunId,
	})
	if err != nil {
		return nil, err
	}
	return stepLogFormResponse(response.VisitDownloadDAGRunStepLogsResponse), nil
}

func (a *API) DownloadSubDAGRunStepLogsForm(ctx context.Context, request api.DownloadSubDAGRunStepLogsFormRequestObject) (api.DownloadSubDAGRunStepLogsFormResponseObject, error) {
	response, err := a.DownloadSubDAGRunStepLogs(ctx, api.DownloadSubDAGRunStepLogsRequestObject{
		Name: request.Name, DagRunId: request.DagRunId, SubDAGRunId: request.SubDAGRunId,
	})
	if err != nil {
		return nil, err
	}
	return stepLogFormResponse(response.VisitDownloadSubDAGRunStepLogsResponse), nil
}

func stepLogDownloadFormAuth(apiBasePath string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost && isStepLogDownload(r, apiBasePath) {
				mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
				if err != nil || mediaType != stepLogFormContentType {
					WriteErrorResponse(w, &Error{HTTPStatus: http.StatusUnsupportedMediaType, Code: api.ErrorCodeBadRequest, Message: "Expected a URL-encoded download form"})
					return
				}
				if err := r.ParseForm(); err != nil {
					WriteErrorResponse(w, &Error{HTTPStatus: http.StatusBadRequest, Code: api.ErrorCodeBadRequest, Message: "Invalid download form"})
					return
				}
				if r.Header.Get("Authorization") == "" {
					if token := r.PostForm.Get("token"); token != "" {
						r.Header.Set("Authorization", "Bearer "+token)
					}
				}
				// OpenAPI validation still needs the form body after authentication.
				body := r.PostForm.Encode()
				r.Body = io.NopCloser(strings.NewReader(body))
				r.ContentLength = int64(len(body))
				writer := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
				next.ServeHTTP(writer, r)
				// Native downloads need a visible error instead of an empty new tab.
				if writer.Status() == http.StatusUnauthorized && writer.BytesWritten() == 0 {
					_, _ = io.WriteString(writer, "Unauthorized\n")
				}
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
