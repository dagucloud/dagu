// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package s3

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
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

	output, err := e.client.GetObject(ctx, &awss3.GetObjectInput{
		Bucket: aws.String(e.cfg.Bucket),
		Key:    aws.String(e.cfg.Key),
	})
	if err != nil {
		return fmt.Errorf("%w: %v", ErrDownloadFailed, err)
	}
	defer func() { _ = output.Body.Close() }()

	tmpFile, err := os.CreateTemp(filepath.Dir(e.cfg.Destination), "."+filepath.Base(e.cfg.Destination)+".*")
	if err != nil {
		return fmt.Errorf("%w: failed to create temporary file: %v", ErrDownloadFailed, err)
	}
	tmpPath := tmpFile.Name()
	defer func() { _ = fileutil.Remove(tmpPath) }()

	size, copyErr := io.Copy(tmpFile, output.Body)
	closeErr := tmpFile.Close()
	if copyErr != nil {
		return fmt.Errorf("%w: failed to write destination file: %v", ErrDownloadFailed, copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("%w: failed to close destination file: %v", ErrDownloadFailed, closeErr)
	}

	if err := fileutil.ReplaceFile(tmpPath, e.cfg.Destination); err != nil {
		return fmt.Errorf("%w: failed to move file to destination: %v", ErrDownloadFailed, err)
	}

	return e.writeResult(DownloadResult{
		Operation:   opDownload,
		Success:     true,
		Bucket:      e.cfg.Bucket,
		Key:         e.cfg.Key,
		URI:         fmt.Sprintf("s3://%s/%s", e.cfg.Bucket, e.cfg.Key),
		Destination: e.cfg.Destination,
		Size:        size,
		ContentType: aws.ToString(output.ContentType),
		ETag:        strings.Trim(aws.ToString(output.ETag), `"`),
		Duration:    time.Since(start).Round(time.Millisecond).String(),
	})
}
