// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type artifactHandler struct {
	dagRunStore    dagrun.DAGRunStore
	ownerID        string
	ownerValidator func(context.Context, string) (bool, error)

	writers   map[*artifactWriter]struct{}
	writersMu sync.Mutex
}

type artifactWriter struct {
	writer    *logWriter
	finalPath string
}

func newArtifactHandler(dagRunStore dagrun.DAGRunStore, ownerID string) *artifactHandler {
	return &artifactHandler{
		dagRunStore: dagRunStore,
		ownerID:     ownerID,
		writers:     make(map[*artifactWriter]struct{}),
	}
}

func (h *artifactHandler) handleStream(stream coordinatorv1.CoordinatorService_StreamArtifactsServer) error {
	ctx := stream.Context()
	var chunksReceived uint64
	var bytesWritten uint64
	var validatedOwnerID string
	var ownerValidated bool
	activeWriters := make(map[string]*artifactWriter)

	defer func() {
		for key := range activeWriters {
			_ = h.closeWriter(activeWriters, key, false)
		}
	}()

	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			return stream.SendAndClose(&coordinatorv1.StreamArtifactsResponse{
				ChunksReceived: chunksReceived,
				BytesWritten:   bytesWritten,
			})
		}
		if err != nil {
			return fmt.Errorf("failed to receive artifact chunk: %w", err)
		}

		chunksReceived++
		key := h.streamKey(chunk)

		if ownerValidated {
			if chunk.OwnerCoordinatorId != validatedOwnerID {
				return status.Error(codes.FailedPrecondition, "artifact chunk sent to non-owner coordinator")
			}
		} else if h.ownerValidator != nil {
			accepted, err := h.ownerValidator(ctx, chunk.OwnerCoordinatorId)
			if err != nil {
				return status.Error(codes.Internal, "failed to validate artifact chunk owner: "+err.Error())
			}
			if !accepted {
				return status.Error(codes.FailedPrecondition, "artifact chunk sent to non-owner coordinator")
			}
			validatedOwnerID = chunk.OwnerCoordinatorId
			ownerValidated = true
		} else if h.ownerID != "" && chunk.OwnerCoordinatorId != h.ownerID {
			return status.Error(codes.FailedPrecondition, "artifact chunk sent to non-owner coordinator")
		} else if h.ownerID != "" {
			validatedOwnerID = chunk.OwnerCoordinatorId
			ownerValidated = true
		}

		if len(chunk.Data) == 0 && !chunk.IsFinal {
			continue
		}

		if len(chunk.Data) > 0 || chunk.IsFinal {
			writer, err := h.getOrCreateWriter(ctx, chunk, activeWriters)
			if err != nil {
				return fmt.Errorf("failed to create artifact writer: %w", err)
			}

			if len(chunk.Data) > 0 {
				n, err := writer.writer.write(chunk.Data)
				if err != nil {
					return fmt.Errorf("failed to write artifact data: %w", err)
				}
				if n > 0 {
					bytesWritten += uint64(n) // #nosec G115 -- n is non-negative from successful Write
				}
			}
		}

		if chunk.IsFinal {
			if err := h.closeWriter(activeWriters, key, true); err != nil {
				return fmt.Errorf("failed to finalize artifact: %w", err)
			}
		}
	}
}

func (h *artifactHandler) streamKey(chunk *coordinatorv1.ArtifactChunk) string {
	relPath := strings.TrimPrefix(path.Clean("/"+strings.ReplaceAll(chunk.RelativePath, "\\", "/")), "/")
	return fmt.Sprintf("%s/%s/%s/%s",
		chunk.DagName,
		chunk.DagRunId,
		chunk.AttemptId,
		relPath,
	)
}

func (h *artifactHandler) getOrCreateWriter(
	ctx context.Context,
	chunk *coordinatorv1.ArtifactChunk,
	activeWriters map[string]*artifactWriter,
) (*artifactWriter, error) {
	key := h.streamKey(chunk)
	if w, ok := activeWriters[key]; ok {
		if _, err := h.archiveDir(ctx, chunk); err != nil {
			_ = h.closeWriter(activeWriters, key, false)
			return nil, err
		}
		return w, nil
	}

	filePath, err := h.artifactFilePath(ctx, chunk)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(filePath), 0o750); err != nil {
		return nil, fmt.Errorf("failed to create artifact directory: %w", err)
	}

	file, err := os.CreateTemp(filepath.Dir(filePath), ".dagu-artifact-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temporary artifact file: %w", err)
	}

	w := &artifactWriter{
		writer: &logWriter{
			file:   file,
			writer: bufio.NewWriterSize(file, 64*1024),
			path:   file.Name(),
		},
		finalPath: filePath,
	}

	h.writersMu.Lock()
	h.writers[w] = struct{}{}
	h.writersMu.Unlock()
	activeWriters[key] = w
	return w, nil
}

func (h *artifactHandler) artifactFilePath(ctx context.Context, chunk *coordinatorv1.ArtifactChunk) (string, error) {
	archiveDir, err := h.archiveDir(ctx, chunk)
	if err != nil {
		return "", err
	}
	filePath, err := fileutil.ResolvePathWithinBase(archiveDir, chunk.RelativePath)
	if err != nil {
		return "", fmt.Errorf("resolve artifact path %q: %w", chunk.RelativePath, err)
	}
	return filePath, nil
}

func (h *artifactHandler) archiveDir(ctx context.Context, chunk *coordinatorv1.ArtifactChunk) (string, error) {
	var (
		attempt dagrun.DAGRunAttempt
		err     error
	)
	if chunk.RootDagRunId != "" && chunk.RootDagRunId != chunk.DagRunId {
		attempt, err = h.dagRunStore.FindSubAttempt(ctx, ir.DAGRunRef{
			Name: chunk.RootDagRunName,
			ID:   chunk.RootDagRunId,
		}, chunk.DagRunId)
	} else {
		attempt, err = h.dagRunStore.FindAttempt(ctx, ir.DAGRunRef{
			Name: chunk.DagName,
			ID:   chunk.DagRunId,
		})
	}
	if err != nil {
		return "", fmt.Errorf("find DAG run attempt for artifacts: %w", err)
	}

	runStatus, err := attempt.ReadStatus(ctx)
	if err != nil {
		return "", fmt.Errorf("read DAG run status for artifacts: %w", err)
	}
	if runStatus == nil || runStatus.ArchiveDir == "" {
		return "", fmt.Errorf("artifact directory is not available for dag run %s", chunk.DagRunId)
	}
	if chunk.AttemptId != "" && runStatus.AttemptID != "" && chunk.AttemptId != runStatus.AttemptID {
		return "", fmt.Errorf("artifact chunk attempt %q does not match latest attempt %q for dag run %s", chunk.AttemptId, runStatus.AttemptID, chunk.DagRunId)
	}

	return runStatus.ArchiveDir, nil
}

func (h *artifactHandler) closeWriter(
	activeWriters map[string]*artifactWriter,
	key string,
	commit bool,
) error {
	w, ok := activeWriters[key]
	if !ok {
		return nil
	}
	delete(activeWriters, key)

	h.writersMu.Lock()
	delete(h.writers, w)
	h.writersMu.Unlock()

	tempPath := w.writer.path
	if err := w.writer.close(); err != nil {
		_ = fileutil.Remove(tempPath)
		return err
	}
	if commit {
		if err := fileutil.ReplaceFile(tempPath, w.finalPath); err != nil {
			_ = fileutil.Remove(tempPath)
			return err
		}
		return nil
	}
	if err := fileutil.Remove(tempPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove temporary artifact file: %w", err)
	}
	return nil
}

func (h *artifactHandler) Close(ctx context.Context) {
	h.writersMu.Lock()
	writers := h.writers
	h.writers = make(map[*artifactWriter]struct{})
	h.writersMu.Unlock()

	for w := range writers {
		tempPath := w.writer.path
		if err := w.writer.close(); err != nil {
			logger.Warn(ctx, "Failed to close artifact writer", tag.Error(err))
		}
		if err := fileutil.Remove(tempPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			logger.Warn(ctx, "Failed to remove temporary artifact file", tag.Error(err))
		}
	}
}
