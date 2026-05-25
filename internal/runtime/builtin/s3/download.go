// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package s3

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/minio/minio-go/v7"
)

// DownloadResult contains the result of a download operation.
type DownloadResult struct {
	Operation   string `json:"operation"`
	Success     bool   `json:"success"`
	Bucket      string `json:"bucket"`
	Key         string `json:"key"`
	URI         string `json:"uri"`
	Destination string `json:"destination"`
	Size        int64  `json:"size"`
	ContentType string `json:"contentType,omitempty"`
	ETag        string `json:"etag,omitempty"`
	Duration    string `json:"duration"`
}

func (e *executorImpl) runDownload(ctx context.Context) error {
	start := time.Now()

	if err := os.MkdirAll(filepath.Dir(e.cfg.Destination), 0o755); err != nil {
		return fmt.Errorf("%w: failed to create destination directory: %v", ErrDownloadFailed, err)
	}

	objInfo, err := e.client.StatObject(ctx, e.cfg.Bucket, e.cfg.Key, minio.StatObjectOptions{})
	if err != nil {
		return fmt.Errorf("%w: %v", ErrDownloadFailed, err)
	}

	// Download to temp file for atomic write
	tmpFile := e.cfg.Destination + ".tmp"
	defer func() { _ = fileutil.Remove(tmpFile) }()

	if err := e.client.FGetObject(ctx, e.cfg.Bucket, e.cfg.Key, tmpFile, minio.GetObjectOptions{}); err != nil {
		return fmt.Errorf("%w: %v", ErrDownloadFailed, err)
	}

	if err := fileutil.ReplaceFile(tmpFile, e.cfg.Destination); err != nil {
		return fmt.Errorf("%w: failed to move file to destination: %v", ErrDownloadFailed, err)
	}

	fileInfo, err := os.Stat(e.cfg.Destination)
	if err != nil {
		return fmt.Errorf("%w: failed to stat destination file: %v", ErrDownloadFailed, err)
	}

	return e.writeResult(DownloadResult{
		Operation:   opDownload,
		Success:     true,
		Bucket:      e.cfg.Bucket,
		Key:         e.cfg.Key,
		URI:         fmt.Sprintf("s3://%s/%s", e.cfg.Bucket, e.cfg.Key),
		Destination: e.cfg.Destination,
		Size:        fileInfo.Size(),
		ContentType: objInfo.ContentType,
		ETag:        objInfo.ETag,
		Duration:    time.Since(start).Round(time.Millisecond).String(),
	})
}
