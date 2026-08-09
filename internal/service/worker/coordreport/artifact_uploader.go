// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordreport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/backoff"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	artifactUploadRetryInterval = 100 * time.Millisecond
	artifactUploadRetryTimeout  = 30 * time.Second
)

var _ runtime.ArtifactFinalizer = (*ArtifactUploader)(nil)

// ArtifactUploader uploads DAG run artifacts from a worker to the coordinator.
type ArtifactUploader struct {
	client    coordinator.Client
	workerID  string
	dagRunID  string
	dagName   string
	attemptID string
	claimKey  string
	rootRef   ir.DAGRunRef
	owner     serviceregistry.HostInfo
	mu        sync.RWMutex
}

// NewArtifactUploader creates a new ArtifactUploader.
func NewArtifactUploader(
	client coordinator.Client,
	workerID string,
	dagRunID string,
	dagName string,
	attemptID string,
	rootRef ir.DAGRunRef,
	owner ...serviceregistry.HostInfo,
) *ArtifactUploader {
	var target serviceregistry.HostInfo
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

// SetClaimKey binds uploaded artifacts to the task claim that authorizes the run.
func (u *ArtifactUploader) SetClaimKey(claimKey string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.claimKey = claimKey
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

func (u *ArtifactUploader) attemptKey(attemptID string) string {
	u.mu.RLock()
	defer u.mu.RUnlock()
	if u.claimKey != "" {
		return u.claimKey
	}
	root := u.rootRef
	if root.Zero() {
		root = ir.NewDAGRunRef(u.dagName, u.dagRunID)
	}
	return ir.GenerateAttemptKey(root.Name, root.ID, u.dagName, u.dagRunID, attemptID)
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
	err := u.uploadDir(ctx, dir, attemptID)
	if !isRetryableArtifactUploadError(err) {
		return err
	}

	retryCtx, cancel := context.WithTimeout(ctx, artifactUploadRetryTimeout)
	defer cancel()
	return backoff.Retry(retryCtx, func(ctx context.Context) error {
		return u.uploadDir(ctx, dir, attemptID)
	}, backoff.NewConstantBackoffPolicy(artifactUploadRetryInterval), isRetryableArtifactUploadError)
}

func (u *ArtifactUploader) uploadDir(ctx context.Context, dir, attemptID string) error {
	seq := uint64(0)
	var stream coordinatorv1.CoordinatorService_StreamArtifactsClient
	defer func() {
		if stream != nil {
			_ = stream.CloseSend()
		}
	}()

	sendChunk := func(chunk *coordinatorv1.ArtifactChunk) error {
		if stream == nil {
			var err error
			stream, err = u.openStream(ctx)
			if err != nil {
				return err
			}
		}
		err := stream.Send(chunk)
		if errors.Is(err, io.EOF) {
			if _, terminalErr := stream.CloseAndRecv(); terminalErr != nil {
				return terminalErr
			}
		}
		return err
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
						AttemptKey:         u.attemptKey(attemptID),
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
				AttemptKey:         u.attemptKey(attemptID),
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

func isRetryableArtifactUploadError(err error) bool {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	code := status.Code(err)
	return code == codes.Unavailable || code == codes.DeadlineExceeded
}
