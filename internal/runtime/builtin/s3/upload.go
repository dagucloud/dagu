// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package s3

import (
	"context"
	"fmt"
	"mime"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// UploadResult contains the result of an upload operation.
type UploadResult struct {
	Operation    string `json:"operation"`
	Success      bool   `json:"success"`
	Bucket       string `json:"bucket"`
	Key          string `json:"key"`
	URI          string `json:"uri"`
	ETag         string `json:"etag,omitempty"`
	Size         int64  `json:"size"`
	ContentType  string `json:"contentType,omitempty"`
	StorageClass string `json:"storageClass,omitempty"`
	Duration     string `json:"duration"`
}

func (e *executorImpl) runUpload(ctx context.Context) error {
	start := time.Now()

	sourceInfo, err := os.Stat(e.cfg.Source)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: source file %q does not exist", ErrSourceNotFound, e.cfg.Source)
		}
		return fmt.Errorf("%w: cannot access source file %q: %v", ErrSourceNotFound, e.cfg.Source, err)
	}
	if sourceInfo.IsDir() {
		return fmt.Errorf("%w: source %q is a directory, not a file", ErrConfig, e.cfg.Source)
	}

	contentType := e.resolveContentType()

	source, err := os.Open(e.cfg.Source)
	if err != nil {
		return fmt.Errorf("%w: cannot open source file %q: %v", ErrSourceNotFound, e.cfg.Source, err)
	}
	defer func() { _ = source.Close() }()

	input := &awss3.PutObjectInput{
		Bucket:        aws.String(e.cfg.Bucket),
		Key:           aws.String(e.cfg.Key),
		Body:          source,
		ContentLength: aws.Int64(sourceInfo.Size()),
		ContentType:   aws.String(contentType),
		Metadata:      e.cfg.Metadata,
		StorageClass:  types.StorageClass(e.cfg.StorageClass),
	}
	if len(e.cfg.Tags) > 0 {
		tags := url.Values{}
		for key, value := range e.cfg.Tags {
			tags.Set(key, value)
		}
		input.Tagging = aws.String(tags.Encode())
	}
	if e.cfg.ACL != "" {
		input.ACL = types.ObjectCannedACL(e.cfg.ACL)
	}
	if e.cfg.ServerSideEncryption != "" {
		input.ServerSideEncryption = types.ServerSideEncryption(e.cfg.ServerSideEncryption)
	}
	if e.cfg.SSEKMSKeyId != "" {
		input.SSEKMSKeyId = aws.String(e.cfg.SSEKMSKeyId)
	}

	uploader := manager.NewUploader(e.client, func(uploader *manager.Uploader) {
		uploader.PartSize = e.cfg.PartSize * 1024 * 1024
		uploader.Concurrency = e.cfg.Concurrency
	})
	output, err := uploader.Upload(ctx, input)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUploadFailed, err)
	}

	return e.writeResult(UploadResult{
		Operation:    opUpload,
		Success:      true,
		Bucket:       e.cfg.Bucket,
		Key:          e.cfg.Key,
		URI:          fmt.Sprintf("s3://%s/%s", e.cfg.Bucket, e.cfg.Key),
		ETag:         strings.Trim(aws.ToString(output.ETag), `"`),
		Size:         sourceInfo.Size(),
		ContentType:  contentType,
		StorageClass: e.cfg.StorageClass,
		Duration:     time.Since(start).Round(time.Millisecond).String(),
	})
}

// resolveContentType determines the content type for the upload.
// Uses configured content type if set, otherwise detects from file extension.
func (e *executorImpl) resolveContentType() string {
	if e.cfg.ContentType != "" {
		return e.cfg.ContentType
	}
	if ext := filepath.Ext(e.cfg.Source); ext != "" {
		if ct := mime.TypeByExtension(ext); ct != "" {
			return ct
		}
	}
	return "application/octet-stream"
}
