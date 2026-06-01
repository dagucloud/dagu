// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordreport

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/dagucloud/dagu/internal/service/coordinator"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
)

var _ runtime.ArtifactFinalizer = (*ArtifactUploader)(nil)

// ArtifactUploader uploads DAG run artifacts to the coordinator in shared-nothing mode.
type ArtifactUploader struct {
	client    coordinator.Client
	workerID  string
	dagRunID  string
	dagName   string
	attemptID string
	rootRef   exec.DAGRunRef
	owner     exec.HostInfo
	mu        sync.RWMutex
}

// NewArtifactUploader creates a new ArtifactUploader.
func NewArtifactUploader(
	client coordinator.Client,
	workerID string,
	dagRunID string,
	dagName string,
	attemptID string,
	rootRef exec.DAGRunRef,
	owner ...exec.HostInfo,
) *ArtifactUploader {
	var target exec.HostInfo
	if len(owner) > 0 {
		target = owner[0]
	}
	return &ArtifactUploader{
		client:    client,
		workerID:  workerID,
		dagRunID:  dagRunID,
		dagName:   dagName,
		attemptID: attemptID,
		rootRef:   rootRef,
		owner:     target,
	}
}

// SetAttemptID updates the attempt ID after the agent creates the attempt.
func (u *ArtifactUploader) SetAttemptID(attemptID string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.attemptID = attemptID
}

// Finalize uploads artifacts for the finalized attempt before the terminal status is written.
func (u *ArtifactUploader) Finalize(ctx context.Context, attemptID, dir string) error {
	u.SetAttemptID(attemptID)
	return u.UploadDir(ctx, dir)
}

func (u *ArtifactUploader) getAttemptID() string {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.attemptID
}

func (u *ArtifactUploader) openStream(ctx context.Context) (coordinatorv1.CoordinatorService_StreamArtifactsClient, error) {
	if u.owner.Host != "" {
		return u.client.StreamArtifactsTo(ctx, u.owner)
	}
	return u.client.StreamArtifacts(ctx)
}

// UploadDir uploads every regular file under dir while preserving relative paths.
func (u *ArtifactUploader) UploadDir(ctx context.Context, dir string) error {
	if dir == "" {
		return nil
	}

	attemptID := u.getAttemptID()
	seq := uint64(0)
	var stream coordinatorv1.CoordinatorService_StreamArtifactsClient

	sendChunk := func(chunk *coordinatorv1.ArtifactChunk) error {
		if stream == nil {
			var err error
			stream, err = u.openStream(ctx)
			if err != nil {
				return err
			}
		}
		return stream.Send(chunk)
	}

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}

		relPath, err := filepath.Rel(dir, path)
		if err != nil {
			return fmt.Errorf("resolve artifact relative path: %w", err)
		}
		relPath = filepath.ToSlash(relPath)

		return func() error {
			file, err := os.Open(filepath.Clean(path))
			if err != nil {
				return fmt.Errorf("open artifact %s: %w", path, err)
			}
			defer func() { _ = file.Close() }()

			buf := make([]byte, maxChunkSize)
			for {
				n, readErr := file.Read(buf)
				if n > 0 {
					seq++
					chunk := &coordinatorv1.ArtifactChunk{
						WorkerId:           u.workerID,
						DagRunId:           u.dagRunID,
						DagName:            u.dagName,
						RelativePath:       relPath,
						Data:               append([]byte(nil), buf[:n]...),
						Sequence:           seq,
						RootDagRunName:     u.rootRef.Name,
						RootDagRunId:       u.rootRef.ID,
						AttemptId:          attemptID,
						OwnerCoordinatorId: u.owner.ID,
					}
					if err := sendChunk(chunk); err != nil {
						return fmt.Errorf("send artifact chunk: %w", err)
					}
				}
				if readErr == nil {
					continue
				}
				if readErr != io.EOF {
					return fmt.Errorf("read artifact %s: %w", path, readErr)
				}
				break
			}

			seq++
			if err := sendChunk(&coordinatorv1.ArtifactChunk{
				WorkerId:           u.workerID,
				DagRunId:           u.dagRunID,
				DagName:            u.dagName,
				RelativePath:       relPath,
				IsFinal:            true,
				Sequence:           seq,
				RootDagRunName:     u.rootRef.Name,
				RootDagRunId:       u.rootRef.ID,
				AttemptId:          attemptID,
				OwnerCoordinatorId: u.owner.ID,
			}); err != nil {
				return fmt.Errorf("send artifact final marker: %w", err)
			}

			return nil
		}()
	})
	if err != nil {
		return err
	}

	if stream == nil {
		return nil
	}

	resp, err := stream.CloseAndRecv()
	if err != nil {
		return fmt.Errorf("finalize artifact upload: %w", err)
	}
	if resp != nil && resp.Error != "" {
		return fmt.Errorf("artifact upload failed: %s", resp.Error)
	}
	return nil
}
